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
	receiptStoreFile     = "receipts-v2.json"
	receiptLockFile      = "receipts-v2.lock"
	receiptMaxBytes      = int64(256 << 20)
	receiptMaxArtifact   = int64(4 << 20)
	receiptMaxRecord     = int64(4 << 20)
	receiptReadChunk     = int64(64 << 10)
	receiptKeep          = 30 * 24 * time.Hour
	receiptMaxTombstones = 100000
)

type storedArtifact struct {
	Metadata Artifact `json:"metadata"`
	Data     []byte   `json:"data"`
}
type storedReceipt struct {
	Receipt    Receipt                   `json:"receipt"`
	Artifacts  map[string]storedArtifact `json:"artifact_data,omitempty"`
	Reconciled bool                      `json:"reconciled"`
	UpdatedAt  time.Time                 `json:"updated_at"`
}
type receiptTombstone struct {
	Owner, TaskID, Digest string
	ExpiredAt             time.Time
}
type receiptDisk struct {
	Version    int                      `json:"version"`
	Receipts   map[string]storedReceipt `json:"receipts"`
	Tombstones []receiptTombstone       `json:"tombstones"`
}
type receiptStore struct {
	mu        sync.Mutex
	dir       string
	maxBytes  int64
	lock      *os.File
	closed    bool
	disk      receiptDisk
	writeFile func(string, []byte) error
}

// OpenReceiptStore opens a private, single-process durable receipt store.
func OpenReceiptStore(directory string, maxBytes int64) (ReceiptStore, error) {
	if directory == "" || maxBytes <= 0 || maxBytes > receiptMaxBytes {
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
	lockPath := filepath.Join(abs, receiptLockFile)
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
	s := &receiptStore{dir: abs, maxBytes: maxBytes, lock: lf, writeFile: atomicReceiptWrite, disk: receiptDisk{Version: 1, Receipts: map[string]storedReceipt{}, Tombstones: []receiptTombstone{}}}
	if err = s.load(); err != nil {
		_ = s.Close()
		return nil, err
	}
	changed := false
	for key, entry := range s.disk.Receipts {
		if entry.Receipt.State == ReceiptAdmitted || entry.Receipt.State == ReceiptRunning {
			entry.Receipt.State = ReceiptUncertain
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

func (s *receiptStore) load() error {
	path := filepath.Join(s.dir, receiptStoreFile)
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
	var d receiptDisk
	if err = json.Unmarshal(b, &d); err != nil || d.Version != 1 || d.Receipts == nil || len(d.Tombstones) > receiptMaxTombstones {
		return fmt.Errorf("decode receipt store: invalid store data")
	}
	s.disk = d
	for key, entry := range d.Receipts {
		if key != receiptKey(entry.Receipt.Owner, entry.Receipt.TaskID) || !validStoredReceipt(entry) {
			return fmt.Errorf("decode receipt store: invalid receipt entry")
		}
	}
	return nil
}

func (s *receiptStore) check(ctx context.Context) error {
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

func (s *receiptStore) Close() error {
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
func receiptKey(owner, task string) string { return owner + "\x00" + task }
func validOwner(owner string) bool {
	return owner != "" && len(owner) <= 256 && utf8Valid([]byte(owner)) && strings.TrimSpace(owner) == owner && !strings.ContainsRune(owner, '\x00')
}
func validDigest(d string) bool {
	if len(d) != 64 {
		return false
	}
	_, err := hex.DecodeString(d)
	return err == nil && d == strings.ToLower(d)
}
func canonicalRequestDigest(r RunTaskRequest) (string, error) {
	validated, err := validateTaskRequestSemantics(r)
	if err != nil {
		return "", err
	}
	canonicalSchema, err := canonicalJSONBytes(validated.OutputSchema)
	if err != nil {
		return "", fmt.Errorf("canonicalize output schema: %w", err)
	}
	validated.OutputSchema = canonicalSchema
	b, err := marshalJSONNoHTMLEscape(validated)
	if err != nil {
		return "", fmt.Errorf("encode canonical request: %w", err)
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

func canonicalJSONBytes(raw []byte) ([]byte, error) {
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
	encoded, err := marshalJSONNoHTMLEscape(value)
	if err != nil {
		return nil, fmt.Errorf("encode canonical JSON: %w", err)
	}
	return encoded, nil
}

func marshalJSONNoHTMLEscape(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	encoded := buffer.Bytes()
	return unescapeJSONLineSeparators(encoded[:len(encoded)-1]), nil
}

// encoding/json escapes U+2028 and U+2029 even with HTML escaping disabled.
// Restore only those escapes in JSON strings, while preserving escaped
// backslashes so literal text such as "\\u2028" retains its meaning.
func unescapeJSONLineSeparators(encoded []byte) []byte {
	result := make([]byte, 0, len(encoded))
	inString := false
	for i := 0; i < len(encoded); {
		c := encoded[i]
		if !inString {
			result = append(result, c)
			if c == '"' {
				inString = true
			}
			i++
			continue
		}
		if c == '"' {
			result = append(result, c)
			inString = false
			i++
			continue
		}
		if c == '\\' && i+1 < len(encoded) {
			if encoded[i+1] == 'u' && i+6 <= len(encoded) {
				switch string(encoded[i+2 : i+6]) {
				case "2028":
					result = append(result, 0xe2, 0x80, 0xa8)
					i += 6
					continue
				case "2029":
					result = append(result, 0xe2, 0x80, 0xa9)
					i += 6
					continue
				}
			}
			result = append(result, c, encoded[i+1])
			i += 2
			continue
		}
		result = append(result, c)
		i++
	}
	return result
}

func (s *receiptStore) Admit(ctx context.Context, owner string, request RunTaskRequest, requestDigest string) (Receipt, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(ctx); err != nil {
		return Receipt{}, false, err
	}
	if !validOwner(owner) || !validTaskID(request.TaskID) || !validDigest(requestDigest) {
		return Receipt{}, false, fmt.Errorf("invalid receipt admission")
	}
	digest, err := canonicalRequestDigest(request)
	if err != nil {
		return Receipt{}, false, fmt.Errorf("validate receipt request: %w", err)
	}
	if digest != requestDigest {
		return Receipt{}, false, ErrReceiptConflict
	}
	key := receiptKey(owner, request.TaskID)
	if old, ok := s.disk.Receipts[key]; ok {
		if old.Receipt.RequestDigest != digest {
			return Receipt{}, false, ErrReceiptConflict
		}
		return cloneReceipt(old.Receipt), false, nil
	}
	for _, tomb := range s.disk.Tombstones {
		if tomb.Owner == owner && tomb.TaskID == request.TaskID {
			return Receipt{}, false, ErrReceiptExpired
		}
	}
	if len(s.disk.Tombstones) >= receiptMaxTombstones {
		return Receipt{}, false, ErrReceiptCapacity
	}
	id, err := randomTaskID()
	if err != nil {
		return Receipt{}, false, fmt.Errorf("generate execution ID: %w", err)
	}
	now := time.Now().UTC()
	receipt := Receipt{Owner: owner, ExecutionID: id, TaskID: request.TaskID, RequestDigest: digest, State: ReceiptAdmitted, CreatedAt: now}
	next := cloneDisk(s.disk)
	next.Receipts[key] = storedReceipt{Receipt: receipt, Artifacts: map[string]storedArtifact{}, UpdatedAt: now}
	if err := s.persist(next); err != nil {
		return Receipt{}, false, err
	}
	s.disk = next
	return cloneReceipt(receipt), true, nil
}

func randomTaskID() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "exec_" + hex.EncodeToString(b), nil
}
func (s *receiptStore) Get(ctx context.Context, owner, executionID string) (Receipt, error) {
	return s.find(ctx, owner, executionID, false)
}
func (s *receiptStore) Lookup(ctx context.Context, owner, taskID string) (Receipt, error) {
	return s.find(ctx, owner, taskID, true)
}
func (s *receiptStore) find(ctx context.Context, owner, id string, byTask bool) (Receipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(ctx); err != nil {
		return Receipt{}, err
	}
	if !validOwner(owner) || !validTaskID(id) {
		return Receipt{}, ErrReceiptOwnerDenied
	}
	for _, e := range s.disk.Receipts {
		if e.Receipt.Owner != owner {
			continue
		}
		if (byTask && e.Receipt.TaskID == id) || (!byTask && e.Receipt.ExecutionID == id) {
			return cloneReceipt(e.Receipt), nil
		}
	}
	for _, t := range s.disk.Tombstones {
		if t.Owner == owner && (byTask && t.TaskID == id) {
			return Receipt{}, ErrReceiptExpired
		}
	}
	return Receipt{}, ErrReceiptNotFound
}

func (s *receiptStore) PutArtifact(ctx context.Context, owner, executionID string, data []byte, mediaType string) (Artifact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(ctx); err != nil {
		return Artifact{}, err
	}
	if !validOwner(owner) || !validTaskID(executionID) {
		return Artifact{}, ErrReceiptOwnerDenied
	}
	if len(data) == 0 || int64(len(data)) > receiptMaxArtifact || mediaType == "" || len(mediaType) > 128 || !utf8Valid([]byte(mediaType)) {
		return Artifact{}, fmt.Errorf("invalid artifact")
	}
	key, entry, err := s.byExecution(owner, executionID)
	if err != nil {
		return Artifact{}, err
	}
	if entry.Receipt.State != ReceiptAdmitted && entry.Receipt.State != ReceiptRunning {
		return Artifact{}, fmt.Errorf("receipt is not active")
	}
	if len(entry.Artifacts) >= 128 {
		return Artifact{}, ErrReceiptCapacity
	}
	id, err := randomTaskID()
	if err != nil {
		return Artifact{}, err
	}
	sum := sha256.Sum256(data)
	a := Artifact{ID: id, SHA256: hex.EncodeToString(sum[:]), MediaType: mediaType, Size: int64(len(data))}
	next := cloneDisk(s.disk)
	entry = next.Receipts[key]
	if entry.Artifacts == nil {
		entry.Artifacts = map[string]storedArtifact{}
	}
	entry.Artifacts[id] = storedArtifact{Metadata: a, Data: append([]byte(nil), data...)}
	entry.Receipt.Artifacts = append(entry.Receipt.Artifacts, a)
	entry.Receipt.State = ReceiptRunning
	entry.UpdatedAt = time.Now().UTC()
	next.Receipts[key] = entry
	if err := s.persist(next); err != nil {
		return Artifact{}, err
	}
	s.disk = next
	return a, nil
}

func (s *receiptStore) byExecution(owner, id string) (string, storedReceipt, error) {
	for k, e := range s.disk.Receipts {
		if e.Receipt.ExecutionID == id {
			if e.Receipt.Owner != owner {
				return "", storedReceipt{}, ErrReceiptOwnerDenied
			}
			return k, e, nil
		}
	}
	return "", storedReceipt{}, ErrReceiptNotFound
}
func (s *receiptStore) Finalize(ctx context.Context, owner, executionID string, result TaskResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(ctx); err != nil {
		return err
	}
	if !validOwner(owner) || !validTaskID(executionID) {
		return ErrReceiptOwnerDenied
	}
	key, entry, err := s.byExecution(owner, executionID)
	if err != nil {
		return err
	}
	if terminalReceipt(entry.Receipt.State) && entry.Receipt.State != ReceiptUncertain {
		return fmt.Errorf("receipt is already terminal")
	}
	if result.ExecutionID != entry.Receipt.ExecutionID || result.TaskID != entry.Receipt.TaskID {
		return fmt.Errorf("receipt identity mismatch")
	}
	if err := ValidateTaskResult(result); err != nil {
		return fmt.Errorf("invalid task result: %w", err)
	}
	encodedResult, err := json.Marshal(result)
	if err != nil || int64(len(encodedResult)) > receiptMaxRecord {
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
	c := cloneResult(result)
	if err := ValidateTaskResult(c); err != nil {
		return fmt.Errorf("invalid serialized task result: %w", err)
	}
	terminal := map[TaskStatus]ReceiptState{TaskSucceeded: ReceiptSucceeded, TaskFailed: ReceiptFailed, TaskBlocked: ReceiptBlocked, TaskCancelled: ReceiptCancelled, TaskBudgetExhausted: ReceiptBudgetExhausted, TaskOutcomeUncertain: ReceiptUncertain}
	next := cloneDisk(s.disk)
	entry = next.Receipts[key]
	entry.Receipt.Result = &c
	entry.Receipt.State = terminal[result.Status]
	entry.Receipt.Artifacts = entry.Receipt.Artifacts[:0]
	for _, artifact := range entry.Artifacts {
		entry.Receipt.Artifacts = append(entry.Receipt.Artifacts, artifact.Metadata)
	}
	sort.Slice(entry.Receipt.Artifacts, func(i, j int) bool { return entry.Receipt.Artifacts[i].ID < entry.Receipt.Artifacts[j].ID })
	entry.UpdatedAt = time.Now().UTC()
	if err := ValidateTaskResult(*entry.Receipt.Result); err != nil {
		return fmt.Errorf("invalid serialized task result in receipt: %w", err)
	}
	if encodedReceipt, err := json.Marshal(entry.Receipt); err != nil || int64(len(encodedReceipt)) > receiptMaxRecord {
		return fmt.Errorf("task receipt exceeds 4 MiB")
	}
	entry.Reconciled = receiptResultReconciled(result)
	next.Receipts[key] = entry
	if err := s.persist(next); err != nil {
		return err
	}
	s.disk = next
	return nil
}

func (s *receiptStore) ReadArtifact(ctx context.Context, owner, executionID, artifactID string, offset, length int64) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(ctx); err != nil {
		return nil, err
	}
	if !validOwner(owner) || !validTaskID(executionID) || !validTaskID(artifactID) {
		return nil, ErrReceiptOwnerDenied
	}
	if offset < 0 || length < 0 || length > receiptReadChunk {
		return nil, fmt.Errorf("invalid artifact range")
	}
	_, entry, err := s.byExecution(owner, executionID)
	if err != nil {
		return nil, err
	}
	a, ok := entry.Artifacts[artifactID]
	if !ok {
		return nil, ErrReceiptNotFound
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

func (s *receiptStore) Cleanup(ctx context.Context, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(ctx); err != nil {
		return err
	}
	next := cloneDisk(s.disk)
	keys := make([]string, 0)
	for k, e := range next.Receipts {
		if !terminalReceipt(e.Receipt.State) || !e.Reconciled || e.Receipt.Result == nil || !receiptResultReconciled(*e.Receipt.Result) || now.Sub(e.Receipt.CreatedAt) <= receiptKeep {
			continue
		}
		keys = append(keys, k)
	}
	for _, k := range keys {
		e := next.Receipts[k]
		delete(next.Receipts, k)
		next.Tombstones = append(next.Tombstones, receiptTombstone{Owner: e.Receipt.Owner, TaskID: e.Receipt.TaskID, Digest: e.Receipt.RequestDigest, ExpiredAt: now.UTC()})
	}
	if len(next.Tombstones) > receiptMaxTombstones {
		return ErrReceiptCapacity
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

func receiptResultReconciled(result TaskResult) bool {
	if result.SideEffectState == SideEffectUnknown || result.Budget.UncertainRequests != 0 || result.Usage.BilledMicroUSD == nil {
		return false
	}
	return result.Budget.UnresolvedMicroUSD == nil || *result.Budget.UnresolvedMicroUSD == 0
}
func terminalReceipt(s ReceiptState) bool {
	switch s {
	case ReceiptSucceeded, ReceiptFailed, ReceiptBlocked, ReceiptCancelled, ReceiptBudgetExhausted, ReceiptUncertain, ReceiptExpired:
		return true
	}
	return false
}

func (s *receiptStore) persist(d receiptDisk) error {
	b, err := json.Marshal(d)
	if err != nil {
		return fmt.Errorf("encode receipt store: %w", err)
	}
	if int64(len(b)) > s.maxBytes {
		return ErrReceiptCapacity
	}
	if int64(len(marshalRecoveryProjection(d))) > s.maxBytes {
		return ErrReceiptCapacity
	}
	if err = s.writeFile(filepath.Join(s.dir, receiptStoreFile), b); err != nil {
		// A failed durable boundary can leave the rename outcome uncertain.
		// Poison the live instance; reopening will reconcile the actual disk state.
		s.closed = true
		return fmt.Errorf("persist receipt store: %w", err)
	}
	return nil
}

func marshalRecoveryProjection(d receiptDisk) []byte {
	projected := d
	projected.Receipts = make(map[string]storedReceipt, len(d.Receipts))
	maxRecoveryTime := time.Date(9999, time.December, 31, 23, 59, 59, 999999999, time.UTC)
	for key, entry := range d.Receipts {
		if entry.Receipt.State == ReceiptAdmitted || entry.Receipt.State == ReceiptRunning {
			entry.Receipt.State = ReceiptUncertain
			entry.UpdatedAt = maxRecoveryTime
			if entry.Receipt.Result != nil {
				entry.Reconciled = false
			}
		}
		projected.Receipts[key] = entry
	}
	b, _ := json.Marshal(projected)
	return b
}
func atomicReceiptWrite(path string, data []byte) error {
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
func cloneDisk(d receiptDisk) receiptDisk {
	b, _ := json.Marshal(d)
	var c receiptDisk
	_ = json.Unmarshal(b, &c)
	return c
}
func cloneReceipt(r Receipt) Receipt {
	b, _ := json.Marshal(r)
	var c Receipt
	_ = json.Unmarshal(b, &c)
	return c
}
func cloneResult(r TaskResult) TaskResult {
	b, _ := json.Marshal(r)
	var c TaskResult
	_ = json.Unmarshal(b, &c)
	return c
}
func validStoredReceipt(e storedReceipt) bool {
	r := e.Receipt
	if !validOwner(r.Owner) || !validTaskID(r.TaskID) || !validTaskID(r.ExecutionID) || !validDigest(r.RequestDigest) || r.CreatedAt.IsZero() {
		return false
	}
	switch r.State {
	case ReceiptAdmitted, ReceiptRunning, ReceiptSucceeded, ReceiptFailed, ReceiptBlocked, ReceiptCancelled, ReceiptBudgetExhausted, ReceiptUncertain:
	default:
		return false
	}
	if r.Result != nil && (r.Result.ExecutionID != r.ExecutionID || r.Result.TaskID != r.TaskID || ValidateTaskResult(*r.Result) != nil) {
		return false
	}
	encoded, err := json.Marshal(r)
	if err != nil || int64(len(encoded)) > receiptMaxRecord {
		return false
	}
	for id, a := range e.Artifacts {
		if id != a.Metadata.ID || !validTaskID(id) || int64(len(a.Data)) != a.Metadata.Size || a.Metadata.Size <= 0 || a.Metadata.Size > receiptMaxArtifact || a.Metadata.MediaType == "" || len(a.Metadata.MediaType) > 128 || !utf8Valid([]byte(a.Metadata.MediaType)) {
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
