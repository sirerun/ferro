package page

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// TargetSelector encodes an exact extension target. A missing selector is
// refused, never replaced by a guessed CSS selector.
func TargetSelector(element Element) (string, error) {
	if strings.TrimSpace(element.Selector) == "" {
		return "", fmt.Errorf("page: target has no exact selector")
	}
	target, err := json.Marshal(struct {
		Selector     string `json:"selector"`
		Tag          string `json:"tag"`
		Role         string `json:"role,omitempty"`
		Name         string `json:"name,omitempty"`
		Text         string `json:"text,omitempty"`
		HREF         string `json:"href,omitempty"`
		InputType    string `json:"input_type"`
		Autocomplete string `json:"autocomplete"`
		AbsHREF      string `json:"abs_href"`
		FormAction   string `json:"form_action"`
		FormMethod   string `json:"form_method"`
	}{element.Selector, element.Tag, element.Role, element.Name, element.Text, element.HREF, element.InputType, element.Autocomplete, element.AbsHREF, element.FormAction, element.FormMethod})
	if err != nil {
		return "", err
	}
	return "ferro-target:" + base64.RawURLEncoding.EncodeToString(target), nil
}
