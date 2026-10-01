package page

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestTargetSelectorGoldenMatchesCore(t *testing.T) {
	element := Element{Selector: "#continue", Tag: "button", Role: "button", Name: "Continue", Text: "Next", HREF: "/next"}
	got, err := TargetSelector(element)
	if err != nil {
		t.Fatal(err)
	}
	golden := `{"selector":"#continue","tag":"button","role":"button","name":"Continue","text":"Next","href":"/next","input_type":"","autocomplete":"","abs_href":"","form_action":"","form_method":""}`
	want := "ferro-target:" + base64.RawURLEncoding.EncodeToString([]byte(golden))
	if got != want {
		t.Fatalf("target = %s, want core payload with pinned empty identity %s", got, want)
	}
}

func TestTargetSelectorEmptySelectorErrors(t *testing.T) {
	for _, selector := range []string{"", " \t"} {
		if _, err := TargetSelector(Element{Tag: "button", Selector: selector, Name: "Continue"}); err == nil {
			t.Fatal("missing selector fell back to a guessed target")
		}
	}
}

func TestOriginOf(t *testing.T) {
	for raw, want := range map[string]string{
		"https://EXAMPLE.test:443/path?x=1#fragment": "https://example.test",
		"http://example.test:80/path":                "http://example.test",
		"https://example.test:8443/path":             "https://example.test:8443",
		"http://[::1]:80/path":                       "http://[::1]",
	} {
		got, err := OriginOf(raw)
		if err != nil || got != want {
			t.Errorf("OriginOf(%q) = %q, %v, want %q", raw, got, err, want)
		}
	}
	for _, raw := range []string{"about:blank", "file:///tmp/example", "https:///path", "https://user:password@example.test/path"} {
		if _, err := OriginOf(raw); err == nil {
			t.Errorf("unsupported origin %q accepted", raw)
		}
	}
}

func TestSnapshotRefByHint(t *testing.T) {
	snapshot := &Snapshot{Elements: []Element{{Ref: 1, Tag: "button", Name: "Continue to checkout"}, {Ref: 2, Tag: "a", Text: "Continue"}}}
	if ref, found := snapshot.RefByHint("button", " CONTINUE "); !found || ref != 1 {
		t.Fatalf("hint found %d, %v", ref, found)
	}
	if _, found := snapshot.RefByHint("input", "Continue"); found {
		t.Fatal("hint matched an element of a different kind")
	}
}

func TestMarshalExecutionRoundTrip(t *testing.T) {
	element := Element{Ref: 1, Tag: "input", Selector: "#payment", Name: "Payment", InputType: "text", Autocomplete: "cc-number", AbsHREF: "https://example.test/payment", FormAction: "https://example.test/submit", FormMethod: "post"}
	snapshot := &Snapshot{URL: "https://example.test", Title: "Example", Elements: []Element{element}}
	raw, err := MarshalExecution(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var restored Snapshot
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	if len(restored.Elements) != 1 || restored.Elements[0] != element {
		t.Fatalf("identity lost: %+v", restored.Elements)
	}
	public, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"selector", "input_type", "autocomplete", "abs_href", "form_action", "form_method"} {
		if strings.Contains(string(public), `"`+key+`"`) {
			t.Errorf("public snapshot leaked %s", key)
		}
	}
	if strings.Contains(snapshot.Render(), "cc-number") || strings.Contains(snapshot.Render(), "/submit") {
		t.Fatal("planner rendering leaked execution identity")
	}
}

func TestUnmarshalKeepsIdentityFields(t *testing.T) {
	raw := []byte(`{"ref":3,"tag":"a","selector":"#link","input_type":"text","autocomplete":"off","abs_href":"https://example.test/new","form_action":"https://example.test/submit","form_method":"post"}`)
	var element Element
	if err := json.Unmarshal(raw, &element); err != nil {
		t.Fatal(err)
	}
	if element.Selector != "#link" || element.InputType != "text" || element.Autocomplete != "off" || element.AbsHREF != "https://example.test/new" || element.FormAction != "https://example.test/submit" || element.FormMethod != "post" {
		t.Fatalf("execution identity lost: %+v", element)
	}
	target, err := TargetSelector(element)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(target, "ferro-target:"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(decoded), `"abs_href":"https://example.test/new"`) || !strings.Contains(string(decoded), `"form_method":"post"`) {
		t.Fatalf("target omitted execution identity: %s", decoded)
	}
}
