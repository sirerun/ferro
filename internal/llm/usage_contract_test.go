package llm

import (
	"encoding/json"
	"github.com/sirerun/ferro/internal/core"
	"testing"
)

func TestUsageContractNullable(t *testing.T) {
	var u core.RequestUsage
	if err := json.Unmarshal([]byte(`{"input_tokens":0}`), &u); err != nil {
		t.Fatal(err)
	}
	if u.InputTokens == nil || *u.InputTokens != 0 || u.OutputTokens != nil {
		t.Fatalf("nullable usage lost: %+v", u)
	}
}
func TestTransmissionEnums(t *testing.T) {
	got := []core.Transmission{core.TransmissionNotSent, core.TransmissionSentUnknown, core.TransmissionResponseReceived}
	if got[0] == got[1] || got[1] == got[2] {
		t.Fatal("transmission states collide")
	}
}
