package mcp

import (
	"context"

	"github.com/sirerun/ferro/internal/core"
)

// guardedDriver gates every model-generated step, not only the entry URL.
type guardedDriver struct {
	core.PageDriver
	owner *Owner
}

func (d *guardedDriver) SelectorMatches(ctx context.Context, sel string, el *core.Element) (bool, error) {
	if err := d.check(ctx, ""); err != nil {
		return false, err
	}
	if validator, ok := d.PageDriver.(core.SelectorValidator); ok {
		return validator.SelectorMatches(ctx, sel, el)
	}
	return false, nil
}

func (d *guardedDriver) check(ctx context.Context, target string) error {
	if err := d.owner.checkOrigin(ctx, target); err != nil {
		return &core.StopError{Code: "origin_denied", Message: err.Error()}
	}
	return nil
}
func (d *guardedDriver) Navigate(c context.Context, u string) error {
	if e := d.check(c, u); e != nil {
		return e
	}
	return d.PageDriver.Navigate(c, u)
}
func (d *guardedDriver) Click(c context.Context, s string) error {
	if e := d.check(c, ""); e != nil {
		return e
	}
	return d.PageDriver.Click(c, s)
}
func (d *guardedDriver) Fill(c context.Context, s, t string) error {
	if e := d.check(c, ""); e != nil {
		return e
	}
	return d.PageDriver.Fill(c, s, t)
}
func (d *guardedDriver) Select(c context.Context, s, v string) (string, error) {
	if e := d.check(c, ""); e != nil {
		return "", e
	}
	return d.PageDriver.Select(c, s, v)
}
func (d *guardedDriver) Key(c context.Context, k string) error {
	if e := d.check(c, ""); e != nil {
		return e
	}
	return d.PageDriver.Key(c, k)
}
func (d *guardedDriver) Scroll(c context.Context, t string) error {
	if e := d.check(c, ""); e != nil {
		return e
	}
	return d.PageDriver.Scroll(c, t)
}
func (d *guardedDriver) ExtractField(c context.Context, s string) (string, error) {
	if e := d.check(c, ""); e != nil {
		return "", e
	}
	return d.PageDriver.ExtractField(c, s)
}
func (d *guardedDriver) ExtractText(c context.Context) (string, error) {
	if e := d.check(c, ""); e != nil {
		return "", e
	}
	return d.PageDriver.ExtractText(c)
}
func (d *guardedDriver) Snapshot(c context.Context, n int) (*core.Snapshot, error) {
	if e := d.check(c, ""); e != nil {
		return nil, e
	}
	return d.PageDriver.Snapshot(c, n)
}
func (d *guardedDriver) WaitVisible(c context.Context, s string) error {
	if e := d.check(c, ""); e != nil {
		return e
	}
	return d.PageDriver.WaitVisible(c, s)
}
func (d *guardedDriver) Settle(c context.Context) error {
	if e := d.check(c, ""); e != nil {
		return e
	}
	return d.PageDriver.Settle(c)
}
