package extbridge

import (
	"testing"
)

func TestHasPendingCommandsTracksQueuedAndAwaitingReplies(t *testing.T) {
	b, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if b.HasPendingCommands() {
		t.Fatal("new bridge reports pending commands")
	}

	ch := make(chan Reply, 1)
	b.mu.Lock()
	b.waiting["queued"] = ch
	b.queue <- &pendingAction{id: "queued", generation: b.generation}
	b.mu.Unlock()
	if !b.HasPendingCommands() {
		t.Fatal("queued command not reported")
	}

	b.mu.Lock()
	b.dispatched["queued"] = true
	b.mu.Unlock()
	if !b.HasPendingCommands() {
		t.Fatal("dispatched command not reported")
	}
	if !b.deliver("queued", Reply{}) {
		t.Fatal("could not complete fixture command")
	}
	if b.HasPendingCommands() {
		t.Fatal("completed command reported pending despite stale queue entry")
	}
}
