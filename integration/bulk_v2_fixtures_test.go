package integration

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/dndungu/ferro/internal/core"
)

type pageFixtureV2 struct {
	ID            string             `json:"id"`
	Kind          string             `json:"kind"`
	Path          string             `json:"path"`
	File          string             `json:"file,omitempty"`
	RedirectTo    string             `json:"redirect_to,omitempty"`
	FinalPath     string             `json:"final_path,omitempty"`
	Facts         map[string]*string `json:"facts"`
	FactMarkers   map[string]string  `json:"fact_markers"`
	RequiredText  []string           `json:"required_text"`
	ForbiddenText []string           `json:"forbidden_text"`
}

type providerFixtureIndexV2 struct {
	ID         string `json:"id"`
	File       string `json:"file"`
	Case       string `json:"case"`
	UsageKnown bool   `json:"usage_known"`
}

type providerMessageV2 struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type providerResponseV2 struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage json.RawMessage `json:"usage"`
}

type providerTranscriptV2 struct {
	ID              string              `json:"id"`
	RequestMessages []providerMessageV2 `json:"request_messages"`
	Response        json.RawMessage     `json:"response"`
}

func bulkV2FixtureRoot(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate fixture test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(source), "..", "testdata", "bulk-v2"))
}

func readPageFixturesV2(t *testing.T, root string) []pageFixtureV2 {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, "pages", "fixtures.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []pageFixtureV2
	if err := json.Unmarshal(b, &fixtures); err != nil {
		t.Fatalf("decode page fixture manifest: %v", err)
	}
	return fixtures
}

func readProviderFixturesV2(t *testing.T, root string) ([]providerFixtureIndexV2, map[string]providerTranscriptV2) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, "provider-transcripts", "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	var index []providerFixtureIndexV2
	if err := json.Unmarshal(b, &index); err != nil {
		t.Fatalf("decode provider transcript index: %v", err)
	}
	transcripts := make(map[string]providerTranscriptV2, len(index))
	for _, entry := range index {
		body, err := os.ReadFile(filepath.Join(root, "provider-transcripts", entry.File))
		if err != nil {
			t.Fatalf("read transcript %s: %v", entry.ID, err)
		}
		var transcript providerTranscriptV2
		if err := json.Unmarshal(body, &transcript); err != nil {
			t.Fatalf("decode transcript %s: %v", entry.ID, err)
		}
		transcripts[entry.ID] = transcript
	}
	return index, transcripts
}

func TestBulkV2Fixtures_Complete(t *testing.T) {
	root := bulkV2FixtureRoot(t)
	pages := readPageFixturesV2(t, root)
	wantPageKinds := map[string]bool{
		"stable_fields": false, "missing_fields": false, "stale_layout": false,
		"redirect": false, "login_gate": false, "hostile_instructions": false,
	}
	pageIDs := make(map[string]struct{}, len(pages))
	for _, fixture := range pages {
		if fixture.ID == "" || fixture.Path == "" || len(fixture.Facts) == 0 || len(fixture.FactMarkers) != len(fixture.Facts) {
			t.Fatalf("incomplete page fixture: %+v", fixture)
		}
		if _, exists := pageIDs[fixture.ID]; exists {
			t.Fatalf("duplicate page fixture ID %q", fixture.ID)
		}
		pageIDs[fixture.ID] = struct{}{}
		if _, ok := wantPageKinds[fixture.Kind]; !ok {
			t.Fatalf("unexpected page fixture kind %q", fixture.Kind)
		}
		wantPageKinds[fixture.Kind] = true
		if fixture.Kind == "redirect" {
			if fixture.RedirectTo == "" || fixture.FinalPath == "" {
				t.Fatalf("redirect fixture lacks target evidence: %+v", fixture)
			}
		} else {
			if fixture.File == "" {
				t.Fatalf("page fixture lacks HTML file: %+v", fixture)
			}
			body, err := os.ReadFile(filepath.Join(root, "pages", fixture.File))
			if err != nil || len(body) == 0 {
				t.Fatalf("page HTML missing for %s: %v", fixture.ID, err)
			}
		}
		for fact := range fixture.Facts {
			if fixture.FactMarkers[fact] == "" {
				t.Fatalf("fixture %s fact %q has no presence marker", fixture.ID, fact)
			}
		}
	}
	for kind, found := range wantPageKinds {
		if !found {
			t.Errorf("missing required page fixture kind %q", kind)
		}
	}

	index, transcripts := readProviderFixturesV2(t, root)
	wantProviderCases := map[string]bool{
		"valid_plan": false, "malformed_json": false, "schema_mismatch": false,
		"repair_response": false, "missing_usage": false,
	}
	providerIDs := make(map[string]struct{}, len(index))
	for _, entry := range index {
		if _, exists := providerIDs[entry.ID]; exists {
			t.Fatalf("duplicate provider fixture ID %q", entry.ID)
		}
		providerIDs[entry.ID] = struct{}{}
		if _, ok := wantProviderCases[entry.Case]; !ok {
			t.Fatalf("unexpected provider case %q", entry.Case)
		}
		wantProviderCases[entry.Case] = true
		transcript, ok := transcripts[entry.ID]
		if !ok || transcript.ID != entry.ID || len(transcript.RequestMessages) == 0 {
			t.Fatalf("provider transcript %s has no request/response evidence", entry.ID)
		}
		var response providerResponseV2
		if err := json.Unmarshal(transcript.Response, &response); err != nil || len(response.Choices) != 1 || response.Choices[0].FinishReason != "stop" {
			t.Fatalf("provider transcript %s has invalid response: %v", entry.ID, err)
		}
		if entry.UsageKnown != (len(response.Usage) > 0) {
			t.Fatalf("provider transcript %s usage_known=%v usage=%s", entry.ID, entry.UsageKnown, response.Usage)
		}
		validateProviderTranscriptCaseV2(t, entry, transcript, response)
	}
	for name, found := range wantProviderCases {
		if !found {
			t.Errorf("missing required provider transcript case %q", name)
		}
	}
}

func validateProviderTranscriptCaseV2(t *testing.T, entry providerFixtureIndexV2, transcript providerTranscriptV2, response providerResponseV2) {
	t.Helper()
	content := response.Choices[0].Message.Content
	switch entry.Case {
	case "valid_plan", "repair_response", "missing_usage":
		var plan core.Plan
		if err := json.Unmarshal([]byte(content), &plan); err != nil {
			t.Fatalf("transcript %s plan JSON invalid: %q (%v)", entry.ID, content, err)
		}
		if err := plan.Validate(); err != nil {
			t.Fatalf("transcript %s plan invalid: %v", entry.ID, err)
		}
	case "malformed_json":
		var value any
		if err := json.Unmarshal([]byte(content), &value); err == nil {
			t.Fatalf("transcript %s expected malformed model JSON", entry.ID)
		}
	case "schema_mismatch":
		var plan struct {
			Steps json.RawMessage `json:"steps"`
		}
		if err := json.Unmarshal([]byte(content), &plan); err != nil || len(plan.Steps) == 0 || bytes.TrimSpace(plan.Steps)[0] == '[' {
			t.Fatalf("transcript %s does not demonstrate steps schema mismatch: %q", entry.ID, content)
		}
	default:
		t.Fatalf("unhandled provider case %q", entry.Case)
	}
	if entry.Case == "repair_response" {
		var found bool
		for _, message := range transcript.RequestMessages {
			if strings.Contains(strings.ToLower(message.Content), "repair") {
				found = true
			}
		}
		if !found {
			t.Fatalf("repair transcript %s lacks a repair request", entry.ID)
		}
	}
}

func TestBulkV2Fixtures_LocalServer(t *testing.T) {
	root := bulkV2FixtureRoot(t)
	pages := readPageFixturesV2(t, root)
	providerIndex, transcripts := readProviderFixturesV2(t, root)
	pageByPath := make(map[string]pageFixtureV2, len(pages))
	for _, fixture := range pages {
		pageByPath[fixture.Path] = fixture
	}
	providerByID := make(map[string]providerTranscriptV2, len(providerIndex))
	for _, entry := range providerIndex {
		providerByID[entry.ID] = transcripts[entry.ID]
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/provider/") {
			id := strings.TrimPrefix(r.URL.Path, "/provider/")
			transcript, ok := providerByID[id]
			if !ok {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(transcript.Response)
			return
		}
		fixture, ok := pageByPath[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if fixture.Kind == "redirect" {
			http.Redirect(w, r, fixture.RedirectTo, http.StatusFound)
			return
		}
		body, err := os.ReadFile(filepath.Join(root, "pages", fixture.File))
		if err != nil {
			http.Error(w, "fixture missing", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(body)
	}))
	defer server.Close()

	client := server.Client()
	for _, fixture := range pages {
		t.Run(fixture.ID, func(t *testing.T) {
			response, err := client.Get(server.URL + fixture.Path)
			if err != nil {
				t.Fatal(err)
			}
			body, readErr := io.ReadAll(response.Body)
			closeErr := response.Body.Close()
			if readErr != nil || closeErr != nil {
				t.Fatalf("read fixture response: read=%v close=%v", readErr, closeErr)
			}
			if response.StatusCode != http.StatusOK {
				t.Fatalf("fixture HTTP status %d", response.StatusCode)
			}
			if got, want := response.Request.URL.Path, fixture.FinalPath; want != "" && got != want {
				t.Fatalf("redirect final path %q, want %q", got, want)
			}
			if fixture.Kind != "redirect" && !strings.HasPrefix(response.Header.Get("Content-Type"), "text/html") {
				t.Fatalf("page content type %q", response.Header.Get("Content-Type"))
			}
			page := string(body)
			for fact, value := range fixture.Facts {
				marker := fixture.FactMarkers[fact]
				if value == nil {
					if strings.Contains(page, marker) {
						t.Errorf("fact %q expected absent, marker %q is present", fact, marker)
					}
					continue
				}
				if !strings.Contains(page, marker) || !strings.Contains(page, *value) {
					t.Errorf("fact %q missing marker/value %q/%q", fact, marker, *value)
				}
			}
			for _, text := range fixture.RequiredText {
				if !strings.Contains(page, text) {
					t.Errorf("required page text %q absent", text)
				}
			}
			for _, text := range fixture.ForbiddenText {
				if strings.Contains(page, text) {
					t.Errorf("forbidden page text %q present", text)
				}
			}
		})
	}

	for _, entry := range providerIndex {
		t.Run(entry.ID, func(t *testing.T) {
			response, err := client.Get(server.URL + "/provider/" + entry.ID)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "application/json" {
				t.Fatalf("provider fixture response status/content-type: %d %q", response.StatusCode, response.Header.Get("Content-Type"))
			}
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			var got providerResponseV2
			if err := json.Unmarshal(body, &got); err != nil || len(got.Choices) != 1 {
				t.Fatalf("provider fixture response invalid: %v", err)
			}
			transcript := transcripts[entry.ID]
			var expected providerResponseV2
			if err := json.Unmarshal(transcript.Response, &expected); err != nil {
				t.Fatal(err)
			}
			if got.ID != expected.ID || got.Choices[0].Message.Content != expected.Choices[0].Message.Content {
				t.Fatalf("served provider transcript differs from fixture %q", entry.ID)
			}
		})
	}
}

func TestBulkV2Fixtures_NoSecrets(t *testing.T) {
	root := bulkV2FixtureRoot(t)
	var files []string
	for _, directory := range []string{"pages", "provider-transcripts"} {
		base := filepath.Join(root, directory)
		err := filepath.WalkDir(base, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	secretMarkers := []*regexp.Regexp{
		regexp.MustCompile(`sk-[A-Za-z0-9]{20,}`),
		regexp.MustCompile(`ghp_[A-Za-z0-9]{20,}`),
		regexp.MustCompile(`xox[baprs]-[A-Za-z0-9-]{20,}`),
		regexp.MustCompile(`(?i)"api[_-]?key"\s*:\s*"[^"]+"`),
		regexp.MustCompile(`(?i)authorization\s*:\s*bearer\s+[A-Za-z0-9._-]{12,}`),
	}
	for _, path := range files {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, marker := range secretMarkers {
			if match := marker.Find(body); match != nil {
				t.Errorf("fixture %s contains credential-shaped data matching %s", filepath.Base(path), marker)
			}
		}
	}
}
