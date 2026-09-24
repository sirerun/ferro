package mcp

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/dndungu/ferro/internal/core"
	"github.com/dndungu/ferro/internal/extbridge"
)

func TestPinSnapshotRejectsRefsFromPreviousPairing(t *testing.T) {
	bridge, err := extbridge.New()
	if err != nil {
		t.Fatal(err)
	}
	if err := bridge.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = bridge.Stop(ctx)
	})
	pair := func(tab string) {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, "http://"+bridge.Addr()+"/pair", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+bridge.Token())
		req.Header.Set(extbridge.TabIDHeader, tab)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("pair %s status = %d", tab, resp.StatusCode)
		}
	}
	pair("old-tab")
	owner := &Owner{bridge: bridge, snap: &core.Snapshot{}, snapGeneration: bridge.Generation()}
	pair("new-tab")
	_, err = owner.pinSnapshot(context.Background())
	var stopped *core.StopError
	if !errors.As(err, &stopped) || stopped.Code != "pairing_changed" {
		t.Fatalf("pinSnapshot() error = %v, want pairing_changed", err)
	}
	if owner.snap != nil {
		t.Fatal("stale snapshot was retained")
	}
}
