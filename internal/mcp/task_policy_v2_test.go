package mcp

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/dndungu/ferro/internal/core"
)

type policyGuardFuncV2 func(context.Context) error

func (f policyGuardFuncV2) Check(ctx context.Context) error { return f(ctx) }

type policyBudgetV2 struct {
	core.BudgetControllerV2
	calls int
	limit int
	err   error
}

func (b *policyBudgetV2) AdmitAction(context.Context) error {
	b.calls++
	if b.err != nil {
		return b.err
	}
	if b.limit > 0 && b.calls > b.limit {
		return fmt.Errorf("action cap reached")
	}
	return nil
}

type policyDriverSpyV2 struct {
	url   string
	calls map[string]int
}

func newPolicyDriverSpyV2(url string) *policyDriverSpyV2 {
	return &policyDriverSpyV2{url: url, calls: make(map[string]int)}
}

func (d *policyDriverSpyV2) called(method string) { d.calls[method]++ }
func (d *policyDriverSpyV2) Navigate(_ context.Context, target string) error {
	d.called("navigate")
	d.url = target
	return nil
}
func (d *policyDriverSpyV2) Click(context.Context, string) error { d.called("click"); return nil }
func (d *policyDriverSpyV2) Fill(context.Context, string, string) error {
	d.called("fill")
	return nil
}
func (d *policyDriverSpyV2) Select(context.Context, string, string) (string, error) {
	d.called("select")
	return "ok", nil
}
func (d *policyDriverSpyV2) Key(context.Context, string) error    { d.called("key"); return nil }
func (d *policyDriverSpyV2) Scroll(context.Context, string) error { d.called("scroll"); return nil }
func (d *policyDriverSpyV2) WaitVisible(context.Context, string) error {
	d.called("wait")
	return nil
}
func (d *policyDriverSpyV2) Settle(context.Context) error { d.called("settle"); return nil }
func (d *policyDriverSpyV2) ExtractField(context.Context, string) (string, error) {
	d.called("extract_field")
	return "field-value", nil
}
func (d *policyDriverSpyV2) ExtractText(context.Context) (string, error) {
	d.called("extract_text")
	return "visible text", nil
}
func (d *policyDriverSpyV2) Snapshot(_ context.Context, maxElements int) (*core.Snapshot, error) {
	if maxElements == 0 {
		d.called("internal_snapshot")
	} else {
		d.called("snapshot")
	}
	return &core.Snapshot{URL: d.url}, nil
}

func policyDriverFixtureV2(t *testing.T, driver *policyDriverSpyV2, guard TaskPolicyGuardV2, budget *policyBudgetV2) core.PageDriver {
	t.Helper()
	wrapped, err := NewTaskPolicyDriverV2(driver, TaskPolicyV2{Mode: "read_only", Origins: []string{"https://allowed.example"}}, guard, budget)
	if err != nil {
		t.Fatal(err)
	}
	return wrapped
}

func TestPolicyV2_DeniedMethodsNeverDispatch(t *testing.T) {
	driver := newPolicyDriverSpyV2("https://allowed.example/start")
	guardCalls := 0
	budget := &policyBudgetV2{}
	wrapped := policyDriverFixtureV2(t, driver, policyGuardFuncV2(func(context.Context) error { guardCalls++; return nil }), budget)
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

func TestPolicyV2_ReadMethodsDelegate(t *testing.T) {
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
			driver := newPolicyDriverSpyV2("https://allowed.example/start")
			budget := &policyBudgetV2{}
			guardCalls := 0
			wrapped := policyDriverFixtureV2(t, driver, policyGuardFuncV2(func(context.Context) error { guardCalls++; return nil }), budget)
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

func TestPolicyV2_OriginDenied(t *testing.T) {
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
			driver := newPolicyDriverSpyV2(tc.origin)
			budget := &policyBudgetV2{}
			wrapped := policyDriverFixtureV2(t, driver, policyGuardFuncV2(func(context.Context) error { return nil }), budget)
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

func TestPolicyV2_PairingChanged(t *testing.T) {
	driver := newPolicyDriverSpyV2("https://allowed.example/start")
	budget := &policyBudgetV2{}
	stop := &core.StopError{Code: "pairing_changed", Message: "pairing changed"}
	wrapped := policyDriverFixtureV2(t, driver, policyGuardFuncV2(func(context.Context) error { return stop }), budget)
	err := wrapped.Scroll(context.Background(), "bottom")
	var got *core.StopError
	if !errors.As(err, &got) || got.Code != "pairing_changed" {
		t.Fatalf("guard error lost: %v", err)
	}
	if budget.calls != 0 || driver.calls["scroll"] != 0 {
		t.Fatalf("revoked guard dispatched operation: calls=%v budget=%d", driver.calls, budget.calls)
	}
}

func TestPolicyV2_Cancelled(t *testing.T) {
	driver := newPolicyDriverSpyV2("https://allowed.example/start")
	budget := &policyBudgetV2{}
	wrapped := policyDriverFixtureV2(t, driver, policyGuardFuncV2(func(ctx context.Context) error { return ctx.Err() }), budget)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := wrapped.Scroll(ctx, "bottom"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error not preserved: %v", err)
	}
	if budget.calls != 0 || driver.calls["scroll"] != 0 {
		t.Fatalf("canceled operation dispatched: calls=%v budget=%d", driver.calls, budget.calls)
	}
}

func TestPolicyV2_ExtractionDropsValueAfterRevocationOrOriginChange(t *testing.T) {
	tests := []struct {
		name   string
		guard  TaskPolicyGuardV2
		mutate func(*policyDriverSpyV2)
	}{
		{
			name: "guard revoked after extraction",
			guard: func() TaskPolicyGuardV2 {
				calls := 0
				return policyGuardFuncV2(func(context.Context) error {
					calls++
					if calls == 4 {
						return &core.StopError{Code: "pairing_changed", Message: "pairing changed"}
					}
					return nil
				})
			}(),
			mutate: func(*policyDriverSpyV2) {},
		},
		{
			name:   "origin changes during extraction",
			guard:  policyGuardFuncV2(func(context.Context) error { return nil }),
			mutate: func(driver *policyDriverSpyV2) { driver.url = "https://outside.example/redirect" },
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			driver := newPolicyDriverSpyV2("https://allowed.example/start")
			mutating := &mutatingExtractionDriverV2{policyDriverSpyV2: driver, mutate: tc.mutate}
			wrapped, err := NewTaskPolicyDriverV2(mutating, TaskPolicyV2{Mode: "read_only", Origins: []string{"https://allowed.example"}}, tc.guard, &policyBudgetV2{})
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

func TestPolicyV2_NavigationRedirectDenied(t *testing.T) {
	driver := newPolicyDriverSpyV2("https://allowed.example/start")
	budget := &policyBudgetV2{}
	// The fake browser applies a redirect while loading the permitted URL.
	redirecting := &redirectPolicyDriverV2{policyDriverSpyV2: driver, redirect: "https://outside.example/landing"}
	wrapped, err := NewTaskPolicyDriverV2(redirecting, TaskPolicyV2{Mode: "read_only", Origins: []string{"https://allowed.example"}}, policyGuardFuncV2(func(context.Context) error { return nil }), budget)
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

func TestPolicyV2_ActionCap(t *testing.T) {
	driver := newPolicyDriverSpyV2("https://allowed.example/start")
	budget := &policyBudgetV2{limit: 1}
	wrapped := policyDriverFixtureV2(t, driver, policyGuardFuncV2(func(context.Context) error { return nil }), budget)
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

func TestPolicyV2_ConstructorRejectsTypedNilAndUnsupportedPolicy(t *testing.T) {
	var driver *policyDriverSpyV2
	guard := policyGuardFuncV2(func(context.Context) error { return nil })
	budget := &policyBudgetV2{}
	if _, err := NewTaskPolicyDriverV2(driver, TaskPolicyV2{Mode: "read_only", Origins: []string{"https://allowed.example"}}, guard, budget); err == nil {
		t.Fatal("typed nil driver accepted")
	}
	if _, err := NewTaskPolicyDriverV2(newPolicyDriverSpyV2("https://allowed.example"), TaskPolicyV2{Mode: "interactive", Origins: []string{"https://allowed.example"}}, guard, budget); err == nil {
		t.Fatal("unsupported policy accepted")
	}
	if _, err := NewTaskPolicyDriverV2(newPolicyDriverSpyV2("https://allowed.example"), TaskPolicyV2{Mode: "read_only", Origins: []string{"https://allowed.example:invalid"}}, guard, budget); err == nil {
		t.Fatal("malformed origin port accepted")
	}
}

type redirectPolicyDriverV2 struct {
	*policyDriverSpyV2
	redirect string
}

type mutatingExtractionDriverV2 struct {
	*policyDriverSpyV2
	mutate func(*policyDriverSpyV2)
}

func (d *mutatingExtractionDriverV2) ExtractText(context.Context) (string, error) {
	d.called("extract_text")
	d.mutate(d.policyDriverSpyV2)
	return "secret-like extracted value", nil
}

func (d *redirectPolicyDriverV2) Navigate(context.Context, string) error {
	d.called("navigate")
	d.url = d.redirect
	return nil
}
