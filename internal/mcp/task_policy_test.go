package mcp

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/dndungu/ferro/internal/core"
)

type policyGuardFunc func(context.Context) error

func (f policyGuardFunc) Check(ctx context.Context) error { return f(ctx) }

type policyBudget struct {
	core.BudgetController
	calls int
	limit int
	err   error
}

func (b *policyBudget) AdmitAction(context.Context) error {
	b.calls++
	if b.err != nil {
		return b.err
	}
	if b.limit > 0 && b.calls > b.limit {
		return fmt.Errorf("action cap reached")
	}
	return nil
}

type policyDriverSpy struct {
	url          string
	calls        map[string]int
	selectStatus string
	clickErr     error
}

func newPolicyDriverSpy(url string) *policyDriverSpy {
	return &policyDriverSpy{url: url, calls: make(map[string]int)}
}

func (d *policyDriverSpy) called(method string) { d.calls[method]++ }
func (d *policyDriverSpy) Navigate(_ context.Context, target string) error {
	d.called("navigate")
	d.url = target
	return nil
}
func (d *policyDriverSpy) Click(context.Context, string) error { d.called("click"); return d.clickErr }
func (d *policyDriverSpy) Fill(context.Context, string, string) error {
	d.called("fill")
	return nil
}
func (d *policyDriverSpy) Select(context.Context, string, string) (string, error) {
	d.called("select")
	if d.selectStatus != "" {
		return d.selectStatus, nil
	}
	return "ok", nil
}
func (d *policyDriverSpy) Key(context.Context, string) error    { d.called("key"); return nil }
func (d *policyDriverSpy) Scroll(context.Context, string) error { d.called("scroll"); return nil }
func (d *policyDriverSpy) WaitVisible(context.Context, string) error {
	d.called("wait")
	return nil
}
func (d *policyDriverSpy) Settle(context.Context) error { d.called("settle"); return nil }
func (d *policyDriverSpy) ExtractField(context.Context, string) (string, error) {
	d.called("extract_field")
	return "field-value", nil
}
func (d *policyDriverSpy) ExtractText(context.Context) (string, error) {
	d.called("extract_text")
	return "visible text", nil
}
func (d *policyDriverSpy) Snapshot(_ context.Context, maxElements int) (*core.Snapshot, error) {
	if maxElements == 0 {
		d.called("internal_snapshot")
	} else {
		d.called("snapshot")
	}
	return &core.Snapshot{URL: d.url}, nil
}

func policyDriverFixture(t *testing.T, driver *policyDriverSpy, guard TaskPolicyGuard, budget *policyBudget) core.PageDriver {
	t.Helper()
	wrapped, err := NewTaskPolicyDriver(driver, TaskPolicy{Mode: "read_only", Origins: []string{"https://allowed.example"}}, guard, budget)
	if err != nil {
		t.Fatal(err)
	}
	return wrapped
}

func TestPolicy_DeniedMethodsNeverDispatch(t *testing.T) {
	driver := newPolicyDriverSpy("https://allowed.example/start")
	guardCalls := 0
	budget := &policyBudget{}
	wrapped := policyDriverFixture(t, driver, policyGuardFunc(func(context.Context) error { guardCalls++; return nil }), budget)
	if err := wrapped.Click(context.Background(), "#x"); err == nil {
		t.Fatal("Click accepted under read-only policy")
	}
	if err := wrapped.Fill(context.Background(), "#x", "value"); err == nil {
		t.Fatal("Fill accepted under read-only policy")
	}
	if _, err := wrapped.Select(context.Background(), "#x", "value"); err == nil {
		t.Fatal("Select accepted under read-only policy")
	}
	if err := wrapped.Key(context.Background(), "Enter"); err == nil {
		t.Fatal("Key accepted under read-only policy")
	}
	if len(driver.calls) != 0 || budget.calls != 0 || guardCalls != 0 {
		t.Fatalf("denied methods had side effects: calls=%v budget=%d guard=%d", driver.calls, budget.calls, guardCalls)
	}
}

func TestTaskPolicyDefaultModeAllowsBrowserWrites(t *testing.T) {
	driver := newPolicyDriverSpy("https://allowed.example/start")
	wrapped, err := NewTaskPolicyDriver(driver, TaskPolicy{Origins: []string{"https://allowed.example"}}, policyGuardFunc(func(context.Context) error { return nil }), &policyBudget{})
	if err != nil {
		t.Fatal(err)
	}
	if err := wrapped.Click(context.Background(), "#search"); err != nil {
		t.Fatal(err)
	}
	if driver.calls["click"] != 1 {
		t.Fatalf("click count=%d, want 1", driver.calls["click"])
	}
}

func TestPolicy_KnownNoOpsDoNotCreateSideEffects(t *testing.T) {
	newWrapper := func(t *testing.T, driver *policyDriverSpy, tracker *sideEffectTracker) core.PageDriver {
		t.Helper()
		wrapped, err := NewTaskPolicyDriver(driver, TaskPolicy{Mode: "read_write", Origins: []string{"https://allowed.example"}}, policyGuardFunc(func(context.Context) error { return nil }), &policyBudget{}, tracker.observe)
		if err != nil {
			t.Fatal(err)
		}
		return wrapped
	}
	t.Run("select status no-op", func(t *testing.T) {
		driver := newPolicyDriverSpy("https://allowed.example/start")
		driver.selectStatus = "no-option"
		var tracker sideEffectTracker
		wrapped := newWrapper(t, driver, &tracker)
		if status, err := wrapped.Select(context.Background(), "#x", "missing"); err != nil || status != "no-option" {
			t.Fatalf("Select()=(%q,%v), want known no-op", status, err)
		}
		if got := tracker.state(); got != SideEffectNone {
			t.Fatalf("no-option marked as side effect: %s", got)
		}
	})
	t.Run("login rejection", func(t *testing.T) {
		driver := newPolicyDriverSpy("https://allowed.example/start")
		driver.clickErr = &core.StopError{Code: "login_required", Message: "sign in first"}
		var tracker sideEffectTracker
		wrapped := newWrapper(t, driver, &tracker)
		if err := wrapped.Click(context.Background(), "#x"); err == nil {
			t.Fatal("Click() succeeded on a login-blocked page")
		}
		if got := tracker.state(); got != SideEffectNone {
			t.Fatalf("known pre-execution rejection marked as side effect: %s", got)
		}
	})
}

func TestPolicy_ReadMethodsDelegate(t *testing.T) {
	tests := []struct {
		name string
		run  func(core.PageDriver) error
		call string
	}{
		{"navigate", func(d core.PageDriver) error { return d.Navigate(context.Background(), "https://allowed.example/next") }, "navigate"},
		{"scroll", func(d core.PageDriver) error { return d.Scroll(context.Background(), "bottom") }, "scroll"},
		{"wait visible", func(d core.PageDriver) error { return d.WaitVisible(context.Background(), "#x") }, "wait"},
		{"settle", func(d core.PageDriver) error { return d.Settle(context.Background()) }, "settle"},
		{"extract field", func(d core.PageDriver) error {
			v, err := d.ExtractField(context.Background(), "#x")
			if err == nil && v != "field-value" {
				return fmt.Errorf("wrong value %q", v)
			}
			return err
		}, "extract_field"},
		{"extract text", func(d core.PageDriver) error {
			v, err := d.ExtractText(context.Background())
			if err == nil && v != "visible text" {
				return fmt.Errorf("wrong value %q", v)
			}
			return err
		}, "extract_text"},
		{"snapshot", func(d core.PageDriver) error {
			s, err := d.Snapshot(context.Background(), 5)
			if err == nil && (s == nil || s.URL != "https://allowed.example/start") {
				return fmt.Errorf("wrong snapshot: %+v", s)
			}
			return err
		}, "snapshot"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			driver := newPolicyDriverSpy("https://allowed.example/start")
			budget := &policyBudget{}
			guardCalls := 0
			wrapped := policyDriverFixture(t, driver, policyGuardFunc(func(context.Context) error { guardCalls++; return nil }), budget)
			if err := tc.run(wrapped); err != nil {
				t.Fatal(err)
			}
			if driver.calls[tc.call] != 1 {
				t.Fatalf("driver method %q calls=%d; all calls=%v", tc.call, driver.calls[tc.call], driver.calls)
			}
			if budget.calls != 1 {
				t.Fatalf("external operation action count=%d, want 1", budget.calls)
			}
			if guardCalls < 4 {
				t.Fatalf("expected guards around operation and origin snapshots, got %d", guardCalls)
			}
		})
	}
}

func TestPolicy_OriginDenied(t *testing.T) {
	tests := []struct {
		name   string
		origin string
		call   func(core.PageDriver) error
	}{
		{"navigation target", "https://allowed.example/start", func(d core.PageDriver) error {
			return d.Navigate(context.Background(), "https://outside.example/secret")
		}},
		{"malformed navigation port", "https://allowed.example/start", func(d core.PageDriver) error {
			return d.Navigate(context.Background(), "https://allowed.example:invalid/secret")
		}},
		{"current page", "https://outside.example/secret", func(d core.PageDriver) error { return d.Scroll(context.Background(), "bottom") }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			driver := newPolicyDriverSpy(tc.origin)
			budget := &policyBudget{}
			wrapped := policyDriverFixture(t, driver, policyGuardFunc(func(context.Context) error { return nil }), budget)
			err := tc.call(wrapped)
			var stop *core.StopError
			if !errors.As(err, &stop) || stop.Code != "origin_denied" {
				t.Fatalf("error=%v, want origin_denied", err)
			}
			if budget.calls != 0 || driver.calls["navigate"] != 0 || driver.calls["scroll"] != 0 {
				t.Fatalf("origin-denied call dispatched: calls=%v budget=%d", driver.calls, budget.calls)
			}
		})
	}
}

func TestPolicy_PairingChanged(t *testing.T) {
	driver := newPolicyDriverSpy("https://allowed.example/start")
	budget := &policyBudget{}
	stop := &core.StopError{Code: "pairing_changed", Message: "pairing changed"}
	wrapped := policyDriverFixture(t, driver, policyGuardFunc(func(context.Context) error { return stop }), budget)
	err := wrapped.Scroll(context.Background(), "bottom")
	var got *core.StopError
	if !errors.As(err, &got) || got.Code != "pairing_changed" {
		t.Fatalf("guard error lost: %v", err)
	}
	if budget.calls != 0 || driver.calls["scroll"] != 0 {
		t.Fatalf("revoked guard dispatched operation: calls=%v budget=%d", driver.calls, budget.calls)
	}
}

func TestPolicy_Cancelled(t *testing.T) {
	driver := newPolicyDriverSpy("https://allowed.example/start")
	budget := &policyBudget{}
	wrapped := policyDriverFixture(t, driver, policyGuardFunc(func(ctx context.Context) error { return ctx.Err() }), budget)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := wrapped.Scroll(ctx, "bottom"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error not preserved: %v", err)
	}
	if budget.calls != 0 || driver.calls["scroll"] != 0 {
		t.Fatalf("canceled operation dispatched: calls=%v budget=%d", driver.calls, budget.calls)
	}
}

func TestPolicy_ExtractionDropsValueAfterRevocationOrOriginChange(t *testing.T) {
	tests := []struct {
		name   string
		guard  TaskPolicyGuard
		mutate func(*policyDriverSpy)
	}{
		{
			name: "guard revoked after extraction",
			guard: func() TaskPolicyGuard {
				calls := 0
				return policyGuardFunc(func(context.Context) error {
					calls++
					if calls == 4 {
						return &core.StopError{Code: "pairing_changed", Message: "pairing changed"}
					}
					return nil
				})
			}(),
			mutate: func(*policyDriverSpy) {},
		},
		{
			name:   "origin changes during extraction",
			guard:  policyGuardFunc(func(context.Context) error { return nil }),
			mutate: func(driver *policyDriverSpy) { driver.url = "https://outside.example/redirect" },
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			driver := newPolicyDriverSpy("https://allowed.example/start")
			mutating := &mutatingExtractionDriver{policyDriverSpy: driver, mutate: tc.mutate}
			wrapped, err := NewTaskPolicyDriver(mutating, TaskPolicy{Mode: "read_only", Origins: []string{"https://allowed.example"}}, tc.guard, &policyBudget{})
			if err != nil {
				t.Fatal(err)
			}
			value, err := wrapped.ExtractText(context.Background())
			if err == nil || value != "" {
				t.Fatalf("unauthorized extraction escaped: value=%q error=%v", value, err)
			}
		})
	}
}

func TestPolicy_NavigationRedirectDenied(t *testing.T) {
	driver := newPolicyDriverSpy("https://allowed.example/start")
	budget := &policyBudget{}
	// The fake browser applies a redirect while loading the permitted URL.
	redirecting := &redirectPolicyDriver{policyDriverSpy: driver, redirect: "https://outside.example/landing"}
	wrapped, err := NewTaskPolicyDriver(redirecting, TaskPolicy{Mode: "read_only", Origins: []string{"https://allowed.example"}}, policyGuardFunc(func(context.Context) error { return nil }), budget)
	if err != nil {
		t.Fatal(err)
	}
	err = wrapped.Navigate(context.Background(), "https://allowed.example/start")
	var stop *core.StopError
	if !errors.As(err, &stop) || stop.Code != "origin_denied" {
		t.Fatalf("redirect result=%v, want origin_denied", err)
	}
	if redirecting.calls["navigate"] != 1 || budget.calls != 1 {
		t.Fatalf("navigation counts calls=%v budget=%d", redirecting.calls, budget.calls)
	}
}

func TestPolicy_ActionCap(t *testing.T) {
	driver := newPolicyDriverSpy("https://allowed.example/start")
	budget := &policyBudget{limit: 1}
	wrapped := policyDriverFixture(t, driver, policyGuardFunc(func(context.Context) error { return nil }), budget)
	if err := wrapped.Scroll(context.Background(), "bottom"); err != nil {
		t.Fatal(err)
	}
	if err := wrapped.Scroll(context.Background(), "top"); err == nil {
		t.Fatal("second action exceeded cap without error")
	}
	if driver.calls["scroll"] != 1 || budget.calls != 2 {
		t.Fatalf("action cap did not stop second dispatch: scroll=%d admissions=%d", driver.calls["scroll"], budget.calls)
	}
}

func TestPolicy_ConstructorRejectsTypedNilAndUnsupportedPolicy(t *testing.T) {
	var driver *policyDriverSpy
	guard := policyGuardFunc(func(context.Context) error { return nil })
	budget := &policyBudget{}
	if _, err := NewTaskPolicyDriver(driver, TaskPolicy{Mode: "read_only", Origins: []string{"https://allowed.example"}}, guard, budget); err == nil {
		t.Fatal("typed nil driver accepted")
	}
	if _, err := NewTaskPolicyDriver(newPolicyDriverSpy("https://allowed.example"), TaskPolicy{Mode: "interactive", Origins: []string{"https://allowed.example"}}, guard, budget); err == nil {
		t.Fatal("unsupported policy accepted")
	}
	if _, err := NewTaskPolicyDriver(newPolicyDriverSpy("https://allowed.example"), TaskPolicy{Mode: "read_only", Origins: []string{"https://allowed.example:invalid"}}, guard, budget); err == nil {
		t.Fatal("malformed origin port accepted")
	}
}

type redirectPolicyDriver struct {
	*policyDriverSpy
	redirect string
}

type mutatingExtractionDriver struct {
	*policyDriverSpy
	mutate func(*policyDriverSpy)
}

func (d *mutatingExtractionDriver) ExtractText(context.Context) (string, error) {
	d.called("extract_text")
	d.mutate(d.policyDriverSpy)
	return "secret-like extracted value", nil
}

func (d *redirectPolicyDriver) Navigate(context.Context, string) error {
	d.called("navigate")
	d.url = d.redirect
	return nil
}
