package page

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Element is one interactive item in a page snapshot. Refs are per-snapshot
// integers; the Executor maps them back to live CDP nodes.
type Element struct {
	Ref          int    `json:"ref"`
	Tag          string `json:"tag"`
	Role         string `json:"role,omitempty"` // e.g. "button", "textbox"
	Text         string `json:"text,omitempty"` // visible label, truncated
	Name         string `json:"name,omitempty"` // form control name/label/placeholder
	HREF         string `json:"href,omitempty"` // links only, path-only
	Selector     string `json:"-"`              // optional backend-provided unique selector
	InputType    string `json:"-"`
	Autocomplete string `json:"-"`
	AbsHREF      string `json:"-"`
	FormAction   string `json:"-"`
	FormMethod   string `json:"-"`
	BackendID    int    `json:"-"` // CDP DOM node backend ID
}

// UnmarshalJSON accepts an execution selector from the extension snapshot
// without returning that DOM detail in public snapshot responses.
func (e *Element) UnmarshalJSON(data []byte) error {
	var raw struct {
		Ref          int    `json:"ref"`
		Tag          string `json:"tag"`
		Role         string `json:"role,omitempty"`
		Text         string `json:"text,omitempty"`
		Name         string `json:"name,omitempty"`
		HREF         string `json:"href,omitempty"`
		Selector     string `json:"selector,omitempty"`
		InputType    string `json:"input_type,omitempty"`
		Autocomplete string `json:"autocomplete,omitempty"`
		AbsHREF      string `json:"abs_href,omitempty"`
		FormAction   string `json:"form_action,omitempty"`
		FormMethod   string `json:"form_method,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*e = Element{Ref: raw.Ref, Tag: raw.Tag, Role: raw.Role, Text: raw.Text, Name: raw.Name, HREF: raw.HREF, Selector: raw.Selector, InputType: raw.InputType, Autocomplete: raw.Autocomplete, AbsHREF: raw.AbsHREF, FormAction: raw.FormAction, FormMethod: raw.FormMethod}
	return nil
}

// Snapshot is the compact page representation sent to the planner.
type Snapshot struct {
	URL       string    `json:"url"`
	Title     string    `json:"title"`
	Elements  []Element `json:"elements"`
	Truncated bool      `json:"truncated,omitempty"` // element cap hit; planner should know
}

// Render produces the text form used in planner prompts. Kept terse:
// every token here costs money on every call.
func (s *Snapshot) Render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "url: %s\ntitle: %s\n", s.URL, s.Title)
	for _, e := range s.Elements {
		parts := []string{fmt.Sprintf("[%d]", e.Ref), e.Tag}
		if e.Role != "" && e.Role != e.Tag {
			parts = append(parts, e.Role)
		}
		if e.Name != "" {
			parts = append(parts, strconv.Quote(e.Name))
		}
		if e.Text != "" {
			parts = append(parts, strconv.Quote(e.Text))
		}
		if e.HREF != "" {
			parts = append(parts, e.HREF)
		}
		b.WriteString(strings.Join(parts, " "))
		b.WriteByte('\n')
	}
	return b.String()
}

// RefByHint finds an element matching loose criteria — used by the repair
// path to remap a stale ref after a re-snapshot.
func (s *Snapshot) RefByHint(tag, text string) (int, bool) {
	text = strings.ToLower(strings.TrimSpace(text))
	for _, e := range s.Elements {
		if e.Tag != tag {
			continue
		}
		if strings.Contains(strings.ToLower(e.Text), text) ||
			strings.Contains(strings.ToLower(e.Name), text) {
			return e.Ref, true
		}
	}
	return 0, false
}
