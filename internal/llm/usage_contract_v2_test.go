package llm

import (
	"encoding/json"
	"github.com/dndungu/ferro/internal/core"
	"testing"
)

func TestUsageContractV2Nullable(t *testing.T) {
	var u core.RequestUsageV2
	if err := json.Unmarshal([]byte(`{"input_tokens":0}`), &u); err != nil {
		t.Fatal(err)
	}
	if u.InputTokens == nil || *u.InputTokens != 0 || u.OutputTokens != nil {
		t.Fatalf("nullable usage lost: %+v", u)
	}
}
func TestTransmissionV2Enums(t *testing.T) {
	got := []core.TransmissionV2{core.TransmissionNotSentV2, core.TransmissionSentUnknownV2, core.TransmissionResponseReceivedV2}
	if got[0] == got[1] || got[1] == got[2] {
		t.Fatal("transmission states collide")
	}
}
