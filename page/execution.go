package page

import "encoding/json"

// MarshalExecution includes exact target identity for a trusted executor.
// Ordinary json.Marshal deliberately excludes these DOM details.
func MarshalExecution(snapshot *Snapshot) ([]byte, error) {
	if snapshot == nil {
		return []byte("null"), nil
	}
	type executionElement struct {
		Element
		Selector     string `json:"selector,omitempty"`
		InputType    string `json:"input_type,omitempty"`
		Autocomplete string `json:"autocomplete,omitempty"`
		AbsHREF      string `json:"abs_href,omitempty"`
		FormAction   string `json:"form_action,omitempty"`
		FormMethod   string `json:"form_method,omitempty"`
	}
	elements := make([]executionElement, len(snapshot.Elements))
	for i, element := range snapshot.Elements {
		elements[i] = executionElement{Element: element, Selector: element.Selector, InputType: element.InputType, Autocomplete: element.Autocomplete, AbsHREF: element.AbsHREF, FormAction: element.FormAction, FormMethod: element.FormMethod}
	}
	return json.Marshal(struct {
		URL       string             `json:"url"`
		Title     string             `json:"title"`
		Elements  []executionElement `json:"elements"`
		Truncated bool               `json:"truncated,omitempty"`
	}{snapshot.URL, snapshot.Title, elements, snapshot.Truncated})
}
