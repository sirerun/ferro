package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// searchAndExtractPlan drives the fixtureServer's shop page to the results
// page and relays the results heading back through
// {{extract.last.<field>}}, so run_task's final result matches exactly what
// an equivalent hand-driven navigate/snapshot/fill/click/wait/extract
// sequence would read off the page.
const searchAndExtractPlan = `{"steps":[
	{"kind":"fill","ref":2,"text":"coffee"},
	{"kind":"click","ref":3},
	{"kind":"wait","for":"dom_settle"},
	{"kind":"extract","fields":{"result":"h1"}},
	{"kind":"done","result":"{{extract.last.result}}"}
]}`

// TestPrimitiveTools_MatchRunTaskResult is T11.4's acceptance test: a
// scripted navigate/snapshot/fill/click/wait/extract sequence against the
// fixture must produce the same result run_task produces for an equivalent
// goal against the same fixture.
func TestPrimitiveTools_MatchRunTaskResult(t *testing.T) {
	if os.Getenv("FERRO_TEST_BROWSER") == "" {
		t.Skip("set FERRO_TEST_BROWSER=1 to run browser tests (requires Chrome)")
	}

	home := shortTempDir(t)
	fixture := fixtureServer(t) // fixture.URL is itself "scheme://host:port" -- exactly an origin
	llm := fakeLLMServer(t, searchAndExtractPlan)

	if err := os.WriteFile(filepath.Join(home, "allowlist.json"),
		[]byte(`["`+fixture.URL+`"]`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := Config{Home: home, LLMBaseURL: llm.URL, LLMModel: "fake", Headless: true}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	o, err := NewOwner(ctx, cfg)
	if err != nil {
		t.Fatalf("NewOwner: %v", err)
	}
	defer func() { _ = o.Close() }()

	call := func(tool string, args any) map[string]any {
		t.Helper()
		argsJSON, err := json.Marshal(args)
		if err != nil {
			t.Fatalf("%s: marshal args: %v", tool, err)
		}
		text, isError, err := o.Call(ctx, tool, argsJSON)
		if err != nil {
			t.Fatalf("%s: Call: %v", tool, err)
		}
		if isError {
			t.Fatalf("%s: returned an error result: %s", tool, text)
		}
		var out map[string]any
		if err := json.Unmarshal([]byte(text), &out); err != nil {
			// Some tools (extract's raw string field values wrapped in an
			// object) decode fine as a map; snapshot/wait/click/fill do too
			// (Snapshot, or executeAction's result, both marshal as objects
			// or null). Fall back to a single "value" wrapper for anything
			// that isn't a JSON object.
			out = map[string]any{"value": text}
		}
		return out
	}

	call("navigate", navigateArgs{URL: fixture.URL})
	call("snapshot", snapshotArgs{})
	call("fill", fillArgs{Ref: 2, Text: "coffee"})
	call("click", clickArgs{Ref: 3})
	call("wait", waitArgs{For: "dom_settle"})
	extracted := call("extract", extractArgs{Fields: map[string]string{"result": "h1"}})

	primitiveResult, ok := extracted["result"].(string)
	if !ok || primitiveResult == "" {
		t.Fatalf("extract did not return a non-empty result field: %+v", extracted)
	}

	runTaskArgsJSON, err := json.Marshal(RunTaskArgs{
		Goal:     "search for coffee and report the results heading",
		StartURL: fixture.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	text, isError, err := o.Call(ctx, "run_task", runTaskArgsJSON)
	if err != nil {
		t.Fatalf("run_task: Call: %v", err)
	}
	if isError {
		t.Fatalf("run_task returned an error result: %s", text)
	}
	var runTaskOut RunTaskOutput
	if err := json.Unmarshal([]byte(text), &runTaskOut); err != nil {
		t.Fatalf("decode run_task result: %v\n%s", err, text)
	}

	if runTaskOut.Result != primitiveResult {
		t.Errorf("run_task result = %v, want %q (the primitive-tool sequence's result)", runTaskOut.Result, primitiveResult)
	}
}
