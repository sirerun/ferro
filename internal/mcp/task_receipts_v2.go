package mcp

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	receiptStoreFileV2     = "receipts-v2.json"
	receiptLockFileV2      = "receipts-v2.lock"
	receiptMaxBytesV2      = int64(256 << 20)
	receiptMaxArtifactV2   = int64(4 << 20)
	receiptMaxRecordV2     = int64(4 << 20)
	receiptReadChunkV2     = int64(64 << 10)
	receiptKeepV2          = 30 * 24 * time.Hour
	receiptMaxTombstonesV2 = 100000
)

type storedArtifactV2 struct {
	Metadata ArtifactV2 `json:"metadata"`
	Data     []byte     `json:"data"`
}
type storedReceiptV2 struct {
	Receipt    ReceiptV2                   `json:"receipt"`
	Artifacts  map[string]storedArtifactV2 `json:"artifact_data,omitempty"`
	Reconciled bool                        `json:"reconciled"`
	UpdatedAt  time.Time                   `json:"updated_at"`
}
type receiptTombstoneV2 struct {
	Owner, TaskID, Digest string
	ExpiredAt             time.Time
}
type receiptDiskV2 struct {
	Version    int                        `json:"version"`
	Receipts   map[string]storedReceiptV2 `json:"receipts"`
	Tombstones []receiptTombstoneV2       `json:"tombstones"`
}
type receiptStoreV2 struct {
	mu        sync.Mutex
	dir       string
	maxBytes  int64
	lock      *os.File
	closed    bool
	disk      receiptDiskV2
	writeFile func(string, []byte) error
}

// OpenReceiptStoreV2 opens a private, single-process durable receipt store.
func OpenReceiptStoreV2(directory string, maxBytes int64) (ReceiptStoreV2, error) {
	if directory == "" || maxBytes <= 0 || maxBytes > receiptMaxBytesV2 {
		return nil, fmt.Errorf("invalid receipt store directory or capacity")
	}
	abs, err := filepath.Abs(directory)
	if err != nil {
		return nil, fmt.Errorf("resolve receipt directory: %w", err)
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return nil, fmt.Errorf("create receipt directory: %w", err)
	}
	if info, err := os.Lstat(abs); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("receipt directory must be a real directory")
	}
	if err := os.Chmod(abs, 0o700); err != nil {
		return nil, fmt.Errorf("secure receipt directory: %w", err)
	}
	lockPath := filepath.Join(abs, receiptLockFileV2)
	if info, statErr := os.Lstat(lockPath); statErr == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
		return nil, fmt.Errorf("invalid receipt lock file")
	} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect receipt lock: %w", statErr)
	}
	lf, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open receipt lock: %w", err)
	}
	if err = syscall.Flock(int(lf.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = lf.Close()
		return nil, fmt.Errorf("lock receipt store: %w", err)
	}
	if err = lf.Chmod(0o600); err != nil {
		_ = syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)
		_ = lf.Close()
		return nil, fmt.Errorf("secure receipt lock: %w", err)
	}
	s := &receiptStoreV2{dir: abs, maxBytes: maxBytes, lock: lf, writeFile: atomicReceiptWriteV2, disk: receiptDiskV2{Version: 1, Receipts: map[string]storedReceiptV2{}, Tombstones: []receiptTombstoneV2{}}}
	if err = s.load(); err != nil {
		_ = s.Close()
		return nil, err
	}
	changed := false
	for key, entry := range s.disk.Receipts {
		if entry.Receipt.State == ReceiptAdmittedV2 || entry.Receipt.State == ReceiptRunningV2 {
			entry.Receipt.State = ReceiptUncertainV2
			entry.UpdatedAt = time.Now().UTC()
			if entry.Receipt.Result != nil {
				entry.Reconciled = false
			}
			s.disk.Receipts[key] = entry
			changed = true
		}
	}
	if changed {
		if err = s.persist(s.disk); err != nil {
			_ = s.Close()
			return nil, err
		}
	}
	return s, nil
}

func (s *receiptStoreV2) load() error {
	path := filepath.Join(s.dir, receiptStoreFileV2)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat receipt store: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > s.maxBytes {
		return fmt.Errorf("invalid or oversized receipt store")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read receipt store: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("secure receipt data: %w", err)
	}
	var d receiptDiskV2
	if err = json.Unmarshal(b, &d); err != nil || d.Version != 1 || d.Receipts == nil || len(d.Tombstones) > receiptMaxTombstonesV2 {
		return fmt.Errorf("decode receipt store: invalid store data")
	}
	s.disk = d
	for key, entry := range d.Receipts {
		if key != receiptKeyV2(entry.Receipt.Owner, entry.Receipt.TaskID) || !validStoredReceiptV2(entry) {
			return fmt.Errorf("decode receipt store: invalid receipt entry")
		}
	}
	return nil
}

func (s *receiptStoreV2) check(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("nil context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.closed {
		return fmt.Errorf("receipt store is closed")
	}
	return nil
}

func (s *receiptStoreV2) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed && s.lock == nil {
		return nil
	}
	s.closed = true
	if s.lock == nil {
		return nil
	}
	err := syscall.Flock(int(s.lock.Fd()), syscall.LOCK_UN)
	closeErr := s.lock.Close()
	s.lock = nil
	if err != nil {
		return fmt.Errorf("unlock receipt store: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("close receipt lock: %w", closeErr)
	}
	return nil
}
func receiptKeyV2(owner, task string) string { return owner + "\x00" + task }
func validOwnerV2(owner string) bool {
	return owner != "" && len(owner) <= 256 && utf8Valid([]byte(owner)) && strings.TrimSpace(owner) == owner && !strings.ContainsRune(owner, '\x00')
}
func validDigestV2(d string) bool {
	if len(d) != 64 {
		return false
	}
	_, err := hex.DecodeString(d)
	return err == nil && d == strings.ToLower(d)
}
func canonicalRequestDigestV2(r RunTaskV2Request) (string, error) {
	b, err := json.Marshal(r)
	if err != nil {
		return "", fmt.Errorf("encode request: %w", err)
	}
	validated, err := ValidateTaskRequestV2(b)
	if err != nil {
		return "", err
	}
	canonicalSchema, err := canonicalJSONBytesV2(validated.OutputSchema)
	if err != nil {
		return "", fmt.Errorf("canonicalize output schema: %w", err)
	}
	validated.OutputSchema = canonicalSchema
	b, err = json.Marshal(validated)
	if err != nil {
		return "", fmt.Errorf("encode canonical request: %w", err)
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

func canonicalJSONBytesV2(raw []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("decode JSON value: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("JSON contains multiple values")
		}
		return nil, fmt.Errorf("decode trailing JSON data: %w", err)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode canonical JSON: %w", err)
	}
	return encoded, nil
}

func (s *receiptStoreV2) Admit(ctx context.Context, owner string, request RunTaskV2Request, requestDigest string) (ReceiptV2, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(ctx); err != nil {
		return ReceiptV2{}, false, err
	}
	if !validOwnerV2(owner) || !validTaskIDV2(request.TaskID) || !validDigestV2(requestDigest) {
		return ReceiptV2{}, false, fmt.Errorf("invalid receipt admission")
	}
	digest, err := canonicalRequestDigestV2(request)
	if err != nil {
		return ReceiptV2{}, false, fmt.Errorf("validate receipt request: %w", err)
	}
	if digest != requestDigest {
		return ReceiptV2{}, false, ErrReceiptConflictV2
	}
	key := receiptKeyV2(owner, request.TaskID)
	if old, ok := s.disk.Receipts[key]; ok {
		if old.Receipt.RequestDigest != digest {
			return ReceiptV2{}, false, ErrReceiptConflictV2
		}
		return cloneReceiptV2(old.Receipt), false, nil
	}
	for _, tomb := range s.disk.Tombstones {
		if tomb.Owner == owner && tomb.TaskID == request.TaskID {
			return ReceiptV2{}, false, ErrReceiptExpiredV2
		}
	}
	if len(s.disk.Tombstones) >= receiptMaxTombstonesV2 {
		return ReceiptV2{}, false, ErrReceiptCapacityV2
	}
	id, err := randomTaskIDV2()
	if err != nil {
		return ReceiptV2{}, false, fmt.Errorf("generate execution ID: %w", err)
	}
	now := time.Now().UTC()
	receipt := ReceiptV2{Owner: owner, ExecutionID: id, TaskID: request.TaskID, RequestDigest: digest, State: ReceiptAdmittedV2, CreatedAt: now}
	next := cloneDiskV2(s.disk)
	next.Receipts[key] = storedReceiptV2{Receipt: receipt, Artifacts: map[string]storedArtifactV2{}, UpdatedAt: now}
	if err := s.persist(next); err != nil {
		return ReceiptV2{}, false, err
	}
	s.disk = next
	return cloneReceiptV2(receipt), true, nil
}

func randomTaskIDV2() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "exec_" + hex.EncodeToString(b), nil
}
func (s *receiptStoreV2) Get(ctx context.Context, owner, executionID string) (ReceiptV2, error) {
	return s.find(ctx, owner, executionID, false)
}
func (s *receiptStoreV2) Lookup(ctx context.Context, owner, taskID string) (ReceiptV2, error) {
	return s.find(ctx, owner, taskID, true)
}
func (s *receiptStoreV2) find(ctx context.Context, owner, id string, byTask bool) (ReceiptV2, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(ctx); err != nil {
		return ReceiptV2{}, err
	}
	if !validOwnerV2(owner) || !validTaskIDV2(id) {
		return ReceiptV2{}, ErrReceiptOwnerDeniedV2
	}
	for _, e := range s.disk.Receipts {
		if e.Receipt.Owner != owner {
			continue
		}
		if (byTask && e.Receipt.TaskID == id) || (!byTask && e.Receipt.ExecutionID == id) {
			return cloneReceiptV2(e.Receipt), nil
		}
	}
	for _, t := range s.disk.Tombstones {
		if t.Owner == owner && (byTask && t.TaskID == id) {
			return ReceiptV2{}, ErrReceiptExpiredV2
		}
	}
	return ReceiptV2{}, ErrReceiptNotFoundV2
}

func (s *receiptStoreV2) PutArtifact(ctx context.Context, owner, executionID string, data []byte, mediaType string) (ArtifactV2, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(ctx); err != nil {
		return ArtifactV2{}, err
	}
	if !validOwnerV2(owner) || !validTaskIDV2(executionID) {
		return ArtifactV2{}, ErrReceiptOwnerDeniedV2
	}
	if len(data) == 0 || int64(len(data)) > receiptMaxArtifactV2 || mediaType == "" || len(mediaType) > 128 || !utf8Valid([]byte(mediaType)) {
		return ArtifactV2{}, fmt.Errorf("invalid artifact")
	}
	key, entry, err := s.byExecution(owner, executionID)
	if err != nil {
		return ArtifactV2{}, err
	}
	if entry.Receipt.State != ReceiptAdmittedV2 && entry.Receipt.State != ReceiptRunningV2 {
		return ArtifactV2{}, fmt.Errorf("receipt is not active")
	}
	if len(entry.Artifacts) >= 128 {
		return ArtifactV2{}, ErrReceiptCapacityV2
	}
	id, err := randomTaskIDV2()
	if err != nil {
		return ArtifactV2{}, err
	}
	sum := sha256.Sum256(data)
	a := ArtifactV2{ID: id, SHA256: hex.EncodeToString(sum[:]), MediaType: mediaType, Size: int64(len(data))}
	next := cloneDiskV2(s.disk)
	entry = next.Receipts[key]
	if entry.Artifacts == nil {
		entry.Artifacts = map[string]storedArtifactV2{}
	}
	entry.Artifacts[id] = storedArtifactV2{Metadata: a, Data: append([]byte(nil), data...)}
	entry.Receipt.Artifacts = append(entry.Receipt.Artifacts, a)
	entry.Receipt.State = ReceiptRunningV2
	entry.UpdatedAt = time.Now().UTC()
	next.Receipts[key] = entry
	if err := s.persist(next); err != nil {
		return ArtifactV2{}, err
	}
	s.disk = next
	return a, nil
}

func (s *receiptStoreV2) byExecution(owner, id string) (string, storedReceiptV2, error) {
	for k, e := range s.disk.Receipts {
		if e.Receipt.ExecutionID == id {
			if e.Receipt.Owner != owner {
				return "", storedReceiptV2{}, ErrReceiptOwnerDeniedV2
			}
			return k, e, nil
		}
	}
	return "", storedReceiptV2{}, ErrReceiptNotFoundV2
}
func (s *receiptStoreV2) Finalize(ctx context.Context, owner, executionID string, result TaskResultV2) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(ctx); err != nil {
		return err
	}
	if !validOwnerV2(owner) || !validTaskIDV2(executionID) {
		return ErrReceiptOwnerDeniedV2
	}
	key, entry, err := s.byExecution(owner, executionID)
	if err != nil {
		return err
	}
	if terminalReceiptV2(entry.Receipt.State) && entry.Receipt.State != ReceiptUncertainV2 {
		return fmt.Errorf("receipt is already terminal")
	}
	if result.ExecutionID != entry.Receipt.ExecutionID || result.TaskID != entry.Receipt.TaskID {
		return fmt.Errorf("receipt identity mismatch")
	}
	if err := ValidateTaskResultV2(result); err != nil {
		return fmt.Errorf("invalid task result: %w", err)
	}
	encodedResult, err := json.Marshal(result)
	if err != nil || int64(len(encodedResult)) > receiptMaxRecordV2 {
		return fmt.Errorf("task receipt exceeds 4 MiB")
	}
	for _, a := range result.Artifacts {
		stored, ok := entry.Artifacts[a.ID]
		if !ok || stored.Metadata != a {
			return fmt.Errorf("result references unavailable artifact")
		}
		sum := sha256.Sum256(stored.Data)
		if hex.EncodeToString(sum[:]) != a.SHA256 || int64(len(stored.Data)) != a.Size {
			return fmt.Errorf("artifact integrity failure")
		}
	}
	terminal := map[TaskStatusV2]ReceiptStateV2{TaskSucceededV2: ReceiptSucceededV2, TaskFailedV2: ReceiptFailedV2, TaskBlockedV2: ReceiptBlockedV2, TaskCancelledV2: ReceiptCancelledV2, TaskBudgetExhaustedV2: ReceiptBudgetExhaustedV2, TaskOutcomeUncertainV2: ReceiptUncertainV2}
	next := cloneDiskV2(s.disk)
	entry = next.Receipts[key]
	c := cloneResultV2(result)
	entry.Receipt.Result = &c
	entry.Receipt.State = terminal[result.Status]
	entry.Receipt.Artifacts = entry.Receipt.Artifacts[:0]
	for _, artifact := range entry.Artifacts {
		entry.Receipt.Artifacts = append(entry.Receipt.Artifacts, artifact.Metadata)
	}
	sort.Slice(entry.Receipt.Artifacts, func(i, j int) bool { return entry.Receipt.Artifacts[i].ID < entry.Receipt.Artifacts[j].ID })
	entry.UpdatedAt = time.Now().UTC()
	if encodedReceipt, err := json.Marshal(entry.Receipt); err != nil || int64(len(encodedReceipt)) > receiptMaxRecordV2 {
		return fmt.Errorf("task receipt exceeds 4 MiB")
	}
	entry.Reconciled = receiptResultReconciledV2(result)
	next.Receipts[key] = entry
	if err := s.persist(next); err != nil {
		return err
	}
	s.disk = next
	return nil
}

func (s *receiptStoreV2) ReadArtifact(ctx context.Context, owner, executionID, artifactID string, offset, length int64) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(ctx); err != nil {
		return nil, err
	}
	if !validOwnerV2(owner) || !validTaskIDV2(executionID) || !validTaskIDV2(artifactID) {
		return nil, ErrReceiptOwnerDeniedV2
	}
	if offset < 0 || length < 0 || length > receiptReadChunkV2 {
		return nil, fmt.Errorf("invalid artifact range")
	}
	_, entry, err := s.byExecution(owner, executionID)
	if err != nil {
		return nil, err
	}
	a, ok := entry.Artifacts[artifactID]
	if !ok {
		return nil, ErrReceiptNotFoundV2
	}
	if offset > int64(len(a.Data)) {
		return nil, fmt.Errorf("artifact offset out of range")
	}
	end := offset + length
	if end < offset || end > int64(len(a.Data)) {
		end = int64(len(a.Data))
	}
	return append([]byte(nil), a.Data[offset:end]...), nil
}

func (s *receiptStoreV2) Cleanup(ctx context.Context, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(ctx); err != nil {
		return err
	}
	next := cloneDiskV2(s.disk)
	keys := make([]string, 0)
	for k, e := range next.Receipts {
		if !terminalReceiptV2(e.Receipt.State) || !e.Reconciled || e.Receipt.Result == nil || !receiptResultReconciledV2(*e.Receipt.Result) || now.Sub(e.Receipt.CreatedAt) <= receiptKeepV2 {
			continue
		}
		keys = append(keys, k)
	}
	for _, k := range keys {
		e := next.Receipts[k]
		delete(next.Receipts, k)
		next.Tombstones = append(next.Tombstones, receiptTombstoneV2{Owner: e.Receipt.Owner, TaskID: e.Receipt.TaskID, Digest: e.Receipt.RequestDigest, ExpiredAt: now.UTC()})
	}
	if len(next.Tombstones) > receiptMaxTombstonesV2 {
		return ErrReceiptCapacityV2
	}
	if len(keys) == 0 {
		return nil
	}
	if err := s.persist(next); err != nil {
		return err
	}
	s.disk = next
	return nil
}

func receiptResultReconciledV2(result TaskResultV2) bool {
	if result.SideEffectState == SideEffectUnknownV2 || result.Budget.UncertainRequests != 0 || result.Usage.BilledMicroUSD == nil {
		return false
	}
	return result.Budget.UnresolvedMicroUSD == nil || *result.Budget.UnresolvedMicroUSD == 0
}
func terminalReceiptV2(s ReceiptStateV2) bool {
	switch s {
	case ReceiptSucceededV2, ReceiptFailedV2, ReceiptBlockedV2, ReceiptCancelledV2, ReceiptBudgetExhaustedV2, ReceiptUncertainV2, ReceiptExpiredV2:
		return true
	}
	return false
}

func (s *receiptStoreV2) persist(d receiptDiskV2) error {
	b, err := json.Marshal(d)
	if err != nil {
		return fmt.Errorf("encode receipt store: %w", err)
	}
	if int64(len(b)) > s.maxBytes {
		return ErrReceiptCapacityV2
	}
	if err = s.writeFile(filepath.Join(s.dir, receiptStoreFileV2), b); err != nil {
		// A failed durable boundary can leave the rename outcome uncertain.
		// Poison the live instance; reopening will reconcile the actual disk state.
		s.closed = true
		return fmt.Errorf("persist receipt store: %w", err)
	}
	return nil
}
func atomicReceiptWriteV2(path string, data []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".receipts-v2-*.tmp")
	if err != nil {
		return err
	}
	temp := f.Name()
	defer os.Remove(temp)
	if err = f.Chmod(0o600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(temp, path); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	syncErr := d.Sync()
	closeErr = d.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}
func cloneDiskV2(d receiptDiskV2) receiptDiskV2 {
	b, _ := json.Marshal(d)
	var c receiptDiskV2
	_ = json.Unmarshal(b, &c)
	return c
}
func cloneReceiptV2(r ReceiptV2) ReceiptV2 {
	b, _ := json.Marshal(r)
	var c ReceiptV2
	_ = json.Unmarshal(b, &c)
	return c
}
func cloneResultV2(r TaskResultV2) TaskResultV2 {
	b, _ := json.Marshal(r)
	var c TaskResultV2
	_ = json.Unmarshal(b, &c)
	return c
}
func validStoredReceiptV2(e storedReceiptV2) bool {
	r := e.Receipt
	if !validOwnerV2(r.Owner) || !validTaskIDV2(r.TaskID) || !validTaskIDV2(r.ExecutionID) || !validDigestV2(r.RequestDigest) || r.CreatedAt.IsZero() {
		return false
	}
	switch r.State {
	case ReceiptAdmittedV2, ReceiptRunningV2, ReceiptSucceededV2, ReceiptFailedV2, ReceiptBlockedV2, ReceiptCancelledV2, ReceiptBudgetExhaustedV2, ReceiptUncertainV2:
	default:
		return false
	}
	if r.Result != nil && (r.Result.ExecutionID != r.ExecutionID || r.Result.TaskID != r.TaskID || ValidateTaskResultV2(*r.Result) != nil) {
		return false
	}
	encoded, err := json.Marshal(r)
	if err != nil || int64(len(encoded)) > receiptMaxRecordV2 {
		return false
	}
	for id, a := range e.Artifacts {
		if id != a.Metadata.ID || !validTaskIDV2(id) || int64(len(a.Data)) != a.Metadata.Size || a.Metadata.Size <= 0 || a.Metadata.Size > receiptMaxArtifactV2 || a.Metadata.MediaType == "" || len(a.Metadata.MediaType) > 128 || !utf8Valid([]byte(a.Metadata.MediaType)) {
			return false
		}
		sum := sha256.Sum256(a.Data)
		if hex.EncodeToString(sum[:]) != a.Metadata.SHA256 {
			return false
		}
	}
	if len(e.Artifacts) > 128 || len(r.Artifacts) > 128 {
		return false
	}
	for _, a := range r.Artifacts {
		stored, ok := e.Artifacts[a.ID]
		if !ok || stored.Metadata != a {
			return false
		}
	}
	return true
}
