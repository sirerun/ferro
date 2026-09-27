package mcp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/sirerun/ferro/internal/core"
)

type taskPolicyDriver struct {
	driver       core.PageDriver
	origins      []string
	mode         string
	guard        TaskPolicyGuard
	budget       core.BudgetController
	onSideEffect func(SideEffectState, bool)
}

// NewTaskPolicyDriver wraps a browser driver with origin and action-budget
// checks. Read and write browser operations are permitted within that scope.
func NewTaskPolicyDriver(driver core.PageDriver, policy TaskPolicy, guard TaskPolicyGuard, budget core.BudgetController, observers ...func(SideEffectState, bool)) (core.PageDriver, error) {
	if isNilPolicyDependency(driver) || isNilPolicyDependency(guard) || isNilPolicyDependency(budget) {
		return nil, fmt.Errorf("driver, policy guard, and budget are required")
	}
	if policy.Mode == "" {
		policy.Mode = "read_write"
	}
	if policy.Mode != "read_write" && policy.Mode != "read_only" {
		return nil, fmt.Errorf("unsupported task policy")
	}
	for _, raw := range policy.Origins {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" || !validTaskURLAuthority(u.Host) {
			return nil, fmt.Errorf("invalid task policy origin")
		}
	}
	origins, err := canonicalOrigins(policy.Origins)
	if err != nil {
		return nil, fmt.Errorf("invalid task policy origins: %w", err)
	}
	wrapped := &taskPolicyDriver{driver: driver, origins: origins, mode: policy.Mode, guard: guard, budget: budget}
	if len(observers) != 0 {
		wrapped.onSideEffect = observers[0]
	}
	return wrapped, nil
}

func (d *taskPolicyDriver) observeSideEffect(state SideEffectState, completed bool) {
	if d.onSideEffect != nil {
		d.onSideEffect(state, completed)
	}
}

func (d *taskPolicyDriver) Navigate(ctx context.Context, target string) error {
	origin, err := taskURLOrigin(target)
	if err != nil || !d.permitsOrigin(origin) {
		return originDenied()
	}
	if err := d.guard.Check(ctx); err != nil {
		return err
	}
	if err := d.admit(ctx); err != nil {
		return err
	}
	if err := d.guard.Check(ctx); err != nil {
		return err
	}
	operationErr := d.driver.Navigate(ctx, target)
	if uncertainOperation(operationErr) {
		return operationErr
	}
	if err := d.guard.Check(ctx); err != nil {
		return err
	}
	if err := d.checkCurrentOrigin(ctx); err != nil {
		return err
	}
	return operationErr
}

func (d *taskPolicyDriver) Click(ctx context.Context, selector string) error {
	if d.mode == "read_only" {
		return readOnlyDenied()
	}
	return d.runMutation(ctx, func() error { return d.driver.Click(ctx, selector) })
}

func (d *taskPolicyDriver) Fill(ctx context.Context, selector, text string) error {
	if d.mode == "read_only" {
		return readOnlyDenied()
	}
	return d.runMutation(ctx, func() error { return d.driver.Fill(ctx, selector, text) })
}

func (d *taskPolicyDriver) Select(ctx context.Context, selector, value string) (string, error) {
	if d.mode == "read_only" {
		return "", readOnlyDenied()
	}
	var selected string
	err := d.runMutationOutcome(ctx, func() (bool, error) {
		var err error
		selected, err = d.driver.Select(ctx, selector, value)
		return selected == "ok", err
	})
	return selected, err
}

func (d *taskPolicyDriver) Key(ctx context.Context, key string) error {
	if d.mode == "read_only" {
		return readOnlyDenied()
	}
	return d.runMutation(ctx, func() error { return d.driver.Key(ctx, key) })
}

func (d *taskPolicyDriver) Scroll(ctx context.Context, target string) error {
	return d.run(ctx, func() error { return d.driver.Scroll(ctx, target) })
}

func (d *taskPolicyDriver) WaitVisible(ctx context.Context, selector string) error {
	return d.run(ctx, func() error { return d.driver.WaitVisible(ctx, selector) })
}

func (d *taskPolicyDriver) Settle(ctx context.Context) error {
	return d.run(ctx, func() error { return d.driver.Settle(ctx) })
}

func (d *taskPolicyDriver) ExtractField(ctx context.Context, selector string) (string, error) {
	var value string
	err := d.run(ctx, func() error {
		var err error
		value, err = d.driver.ExtractField(ctx, selector)
		return err
	})
	if err != nil {
		return "", err
	}
	return value, err
}

func (d *taskPolicyDriver) ExtractText(ctx context.Context) (string, error) {
	var value string
	err := d.run(ctx, func() error {
		var err error
		value, err = d.driver.ExtractText(ctx)
		return err
	})
	if err != nil {
		return "", err
	}
	return value, err
}

func (d *taskPolicyDriver) Snapshot(ctx context.Context, maxElements int) (*core.Snapshot, error) {
	if err := d.checkCurrentOrigin(ctx); err != nil {
		return nil, err
	}
	if err := d.admit(ctx); err != nil {
		return nil, err
	}
	if err := d.guard.Check(ctx); err != nil {
		return nil, err
	}
	snapshot, operationErr := d.driver.Snapshot(ctx, maxElements)
	if uncertainOperation(operationErr) {
		return nil, operationErr
	}
	if err := d.guard.Check(ctx); err != nil {
		return nil, err
	}
	if operationErr != nil {
		return nil, operationErr
	}
	if snapshot == nil {
		return nil, fmt.Errorf("driver returned an empty snapshot")
	}
	if err := d.requirePermittedURL(snapshot.URL); err != nil {
		return nil, err
	}
	return snapshot, nil
}

func (d *taskPolicyDriver) run(ctx context.Context, operation func() error) error {
	if err := d.checkCurrentOrigin(ctx); err != nil {
		return err
	}
	if err := d.admit(ctx); err != nil {
		return err
	}
	if err := d.guard.Check(ctx); err != nil {
		return err
	}
	operationErr := operation()
	if uncertainOperation(operationErr) {
		return operationErr
	}
	if err := d.guard.Check(ctx); err != nil {
		return err
	}
	if err := d.checkCurrentOrigin(ctx); err != nil {
		return err
	}
	return operationErr
}

func (d *taskPolicyDriver) runMutation(ctx context.Context, operation func() error) error {
	return d.runMutationOutcome(ctx, func() (bool, error) {
		err := operation()
		return err == nil, err
	})
}

func (d *taskPolicyDriver) runMutationOutcome(ctx context.Context, operation func() (bool, error)) error {
	if err := d.checkCurrentOrigin(ctx); err != nil {
		return err
	}
	if err := d.admit(ctx); err != nil {
		return err
	}
	if err := d.guard.Check(ctx); err != nil {
		return err
	}
	d.observeSideEffect(SideEffectUnknown, false)
	changed, operationErr := operation()
	if operationErr == nil && changed {
		d.observeSideEffect(SideEffectConfirmed, true)
	} else if operationErr == nil || knownPreExecutionRejection(operationErr) {
		d.observeSideEffect(SideEffectNone, true)
	} else {
		d.observeSideEffect(SideEffectUnknown, true)
	}
	if uncertainOperation(operationErr) {
		return operationErr
	}
	if err := d.guard.Check(ctx); err != nil {
		return err
	}
	if err := d.checkCurrentOrigin(ctx); err != nil {
		return err
	}
	return operationErr
}

func knownPreExecutionRejection(err error) bool {
	var stopped *core.StopError
	if !errors.As(err, &stopped) {
		return false
	}
	switch stopped.Code {
	case "blocked", "login_required", "origin_denied", "read_only", "stale_ref":
		return true
	default:
		return false
	}
}

func (d *taskPolicyDriver) checkCurrentOrigin(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := d.guard.Check(ctx); err != nil {
		return err
	}
	snapshot, err := d.driver.Snapshot(ctx, 0)
	if uncertainOperation(err) {
		return err
	}
	if guardErr := d.guard.Check(ctx); guardErr != nil {
		return guardErr
	}
	if err != nil {
		return err
	}
	if snapshot == nil {
		return fmt.Errorf("driver returned an empty origin snapshot")
	}
	return d.requirePermittedURL(snapshot.URL)
}

func (d *taskPolicyDriver) requirePermittedURL(raw string) error {
	origin, err := taskURLOrigin(raw)
	if err != nil || !d.permitsOrigin(origin) {
		return originDenied()
	}
	return nil
}

func (d *taskPolicyDriver) permitsOrigin(origin string) bool {
	i := sort.SearchStrings(d.origins, origin)
	return i < len(d.origins) && d.origins[i] == origin
}

func (d *taskPolicyDriver) admit(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return d.budget.AdmitAction(ctx)
}

func taskURLOrigin(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" || u.User != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("invalid browser URL")
	}
	if !validTaskURLAuthority(u.Host) {
		return "", fmt.Errorf("invalid browser URL authority")
	}
	return canonicalOrigin(u.Scheme + "://" + u.Host)
}

func validTaskURLAuthority(authority string) bool {
	port := ""
	portSpecified := false
	if strings.HasPrefix(authority, "[") {
		end := strings.IndexByte(authority, ']')
		if end < 0 || net.ParseIP(authority[1:end]) == nil {
			return false
		}
		rest := authority[end+1:]
		if rest != "" {
			if !strings.HasPrefix(rest, ":") {
				return false
			}
			port, portSpecified = rest[1:], true
		}
	} else if colon := strings.LastIndexByte(authority, ':'); colon >= 0 {
		if strings.Contains(authority[:colon], ":") {
			return false
		}
		port, portSpecified = authority[colon+1:], true
	}
	if !portSpecified {
		return true
	}
	if port == "" {
		return false
	}
	for _, digit := range port {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	n, err := strconv.Atoi(port)
	return err == nil && n >= 1 && n <= 65535
}

func readOnlyDenied() error {
	return &core.StopError{Code: "read_only", Message: "This task is read-only."}
}

func originDenied() error {
	return &core.StopError{Code: "origin_denied", Message: "The browser page is outside the permitted origins."}
}

func isNilPolicyDependency(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}

// A post-operation guard may deny the result, but must never erase evidence
// that a dispatched browser operation has an unknown outcome.
func uncertainOperation(err error) bool {
	var stopped *core.StopError
	if !errors.As(err, &stopped) {
		return false
	}
	return stopped.Code == "outcome_uncertain" || stopped.Code == "disconnected" || stopped.Code == "pairing_changed"
}
