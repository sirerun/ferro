package mcp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"
)

// Status reports the daemon state for cfg.Home: "not running" if no owner
// is reachable at cfg.SocketPath(), or the owner's PID and socket path.
// Never attempts to become the owner itself -- a pure read.
func Status(cfg Config) string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	text, isError, err := relayCall(ctx, cfg.SocketPath(), controlStatus, nil)
	if err != nil || isError {
		return "not running"
	}
	return text
}

// Stop asks the owner (if any) to close the browser pool and remove the
// lock/socket files, then waits briefly for it to actually finish before
// reporting. Never attempts to become the owner itself.
func Stop(cfg Config) string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, isError, err := relayCall(ctx, cfg.SocketPath(), controlStop, nil)
	if err != nil {
		return "not running"
	}
	if isError {
		return "stop request was rejected"
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(cfg.SocketPath()); errors.Is(err, os.ErrNotExist) {
			return "stopped"
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Sprintf("stop requested but %s is still present after 3s", cfg.SocketPath())
}
