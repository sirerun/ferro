package mcp

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestExtensionServiceOwnerShimWithoutModel(t *testing.T) {
	bin := buildFerroMCPBinary(t)
	home := shortTempDir(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	env := append(os.Environ(), "FERRO_MCP_HOME="+home, "FERRO_MCP_BACKEND=extension", "FERRO_MCP_BRIDGE_ADDR=127.0.0.1:0", "FERRO_MCP_REMOTE=false", "FERRO_MCP_LLM_BASE_URL=", "FERRO_MCP_LLM_MODEL=")
	connect := func() *sdk.ClientSession {
		t.Helper()
		cmd := exec.Command(bin)
		cmd.Env = env
		cmd.Stderr = os.Stderr
		c := sdk.NewClient(&sdk.Implementation{Name: "process-test", Version: "1"}, nil)
		s, e := c.Connect(ctx, &sdk.CommandTransport{Command: cmd, TerminateDuration: time.Second}, nil)
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { _ = s.Close() })
		return s
	}
	a, b := connect(), connect()
	call := func(s *sdk.ClientSession, name string, wantError bool) string {
		t.Helper()
		r, e := s.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: map[string]any{}})
		if e != nil {
			t.Fatal(e)
		}
		text := r.Content[0].(*sdk.TextContent).Text
		if r.IsError != wantError {
			t.Fatalf("%s isError=%v: %s", name, r.IsError, text)
		}
		return text
	}
	if got := call(b, "browser_status", false); !strings.Contains(got, `"backend":"extension"`) {
		t.Fatalf("%s", got)
	}
	call(b, "acquire_tab", false)
	call(a, "acquire_tab", true)
	waitForBusy := func(want bool) {
		t.Helper()
		deadline := time.NewTimer(2 * time.Second)
		defer deadline.Stop()
		tick := time.NewTicker(10 * time.Millisecond)
		defer tick.Stop()
		for {
			state := call(a, "browser_status", false)
			if strings.Contains(state, `"busy":`+fmt.Sprint(want)) {
				return
			}
			select {
			case <-tick.C:
			case <-deadline.C:
				t.Fatalf("busy never became %v: %s", want, state)
			}
		}
	}
	// An actual relayed browser call must be cancellable while its gate is held.
	for _, viaTool := range []bool{true, false} {
		callCtx, cancelCall := context.WithCancel(ctx)
		finished := make(chan bool, 1)
		go func() {
			result, err := b.CallTool(callCtx, &sdk.CallToolParams{Name: "wait", Arguments: map[string]any{"for": "5s"}})
			finished <- (err != nil || result.IsError)
		}()
		waitForBusy(true)
		if viaTool {
			call(a, "cancel_task", true)
			call(b, "cancel_task", false)
		} else {
			cancelCall()
		}
		select {
		case failed := <-finished:
			if !failed {
				t.Fatal("canceled wait returned success")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("canceled relay call hung")
		}
		cancelCall()
		waitForBusy(false)
	}
	call(b, "release_tab", false)
	call(a, "acquire_tab", false)
	// Stop must work while the owner's stdio connection is still open.
	if got := Stop(Config{Home: home}); got != "stopped" {
		t.Fatalf("stop=%s", got)
	}
}
