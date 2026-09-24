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

	"github.com/dndungu/ferro/internal/core"
)

type taskPolicyDriverV2 struct {
	driver  core.PageDriver
	origins []string
	guard   TaskPolicyGuardV2
	budget  core.BudgetControllerV2
}

// NewTaskPolicyDriverV2 wraps a browser driver with read-only origin and
// action-budget checks. It is an automation guard, not network egress control.
func NewTaskPolicyDriverV2(driver core.PageDriver, policy TaskPolicyV2, guard TaskPolicyGuardV2, budget core.BudgetControllerV2) (core.PageDriver, error) {
	if isNilPolicyDependencyV2(driver) || isNilPolicyDependencyV2(guard) || isNilPolicyDependencyV2(budget) {
		return nil, fmt.Errorf("driver, policy guard, and budget are required")
	}
	if policy.Mode != "read_only" {
		return nil, fmt.Errorf("unsupported task policy")
	}
	for _, raw := range policy.Origins {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" || !validTaskURLAuthorityV2(u.Host) {
			return nil, fmt.Errorf("invalid task policy origin")
		}
	}
	origins, err := canonicalOriginsV2(policy.Origins)
	if err != nil {
		return nil, fmt.Errorf("invalid task policy origins: %w", err)
	}
	return &taskPolicyDriverV2{driver: driver, origins: origins, guard: guard, budget: budget}, nil
}

func (d *taskPolicyDriverV2) Navigate(ctx context.Context, target string) error {
	origin, err := taskURLOriginV2(target)
	if err != nil || !d.permitsOrigin(origin) {
		return originDeniedV2()
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
	if uncertainOperationV2(operationErr) {
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

func (d *taskPolicyDriverV2) Click(ctx context.Context, selector string) error {
	return readOnlyDeniedV2()
}

func (d *taskPolicyDriverV2) Fill(ctx context.Context, selector, text string) error {
	return readOnlyDeniedV2()
}

func (d *taskPolicyDriverV2) Select(ctx context.Context, selector, value string) (string, error) {
	return "", readOnlyDeniedV2()
}

func (d *taskPolicyDriverV2) Key(ctx context.Context, key string) error {
	return readOnlyDeniedV2()
}

func (d *taskPolicyDriverV2) Scroll(ctx context.Context, target string) error {
	return d.run(ctx, func() error { return d.driver.Scroll(ctx, target) })
}

func (d *taskPolicyDriverV2) WaitVisible(ctx context.Context, selector string) error {
	return d.run(ctx, func() error { return d.driver.WaitVisible(ctx, selector) })
}

func (d *taskPolicyDriverV2) Settle(ctx context.Context) error {
	return d.run(ctx, func() error { return d.driver.Settle(ctx) })
}

func (d *taskPolicyDriverV2) ExtractField(ctx context.Context, selector string) (string, error) {
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

func (d *taskPolicyDriverV2) ExtractText(ctx context.Context) (string, error) {
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

func (d *taskPolicyDriverV2) Snapshot(ctx context.Context, maxElements int) (*core.Snapshot, error) {
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
	if uncertainOperationV2(operationErr) {
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

func (d *taskPolicyDriverV2) run(ctx context.Context, operation func() error) error {
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
	if uncertainOperationV2(operationErr) {
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

func (d *taskPolicyDriverV2) checkCurrentOrigin(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := d.guard.Check(ctx); err != nil {
		return err
	}
	snapshot, err := d.driver.Snapshot(ctx, 0)
	if uncertainOperationV2(err) {
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

func (d *taskPolicyDriverV2) requirePermittedURL(raw string) error {
	origin, err := taskURLOriginV2(raw)
	if err != nil || !d.permitsOrigin(origin) {
		return originDeniedV2()
	}
	return nil
}

func (d *taskPolicyDriverV2) permitsOrigin(origin string) bool {
	i := sort.SearchStrings(d.origins, origin)
	return i < len(d.origins) && d.origins[i] == origin
}

func (d *taskPolicyDriverV2) admit(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return d.budget.AdmitAction(ctx)
}

func taskURLOriginV2(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" || u.User != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("invalid browser URL")
	}
	if !validTaskURLAuthorityV2(u.Host) {
		return "", fmt.Errorf("invalid browser URL authority")
	}
	return canonicalOriginV2(u.Scheme + "://" + u.Host)
}

func validTaskURLAuthorityV2(authority string) bool {
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

func readOnlyDeniedV2() error {
	return &core.StopError{Code: "read_only", Message: "This task is read-only."}
}

func originDeniedV2() error {
	return &core.StopError{Code: "origin_denied", Message: "The browser page is outside the permitted origins."}
}

func isNilPolicyDependencyV2(value any) bool {
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
func uncertainOperationV2(err error) bool {
	var stopped *core.StopError
	if !errors.As(err, &stopped) {
		return false
	}
	return stopped.Code == "outcome_uncertain" || stopped.Code == "disconnected" || stopped.Code == "pairing_changed"
}
