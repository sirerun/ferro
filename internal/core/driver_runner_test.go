package core

import (
	"context"
	"errors"
	"testing"
)

type driverFixture struct {
	PageDriver
	snapshots, fills int
	fail             error
}

func (d *driverFixture) Snapshot(context.Context, int) (*Snapshot, error) {
	d.snapshots++
	return &Snapshot{URL: "https://fixture.test", Elements: []Element{{Ref: 1, Tag: "input", Name: "q"}}}, nil
}
func (d *driverFixture) Fill(context.Context, string, string) error {
	d.fills++
	if d.fills == 1 {
		return d.fail
	}
	return nil
}

type repairFixture struct{ calls int }

func (l *repairFixture) Complete(context.Context, string, string) (string, error) {
	l.calls++
	if l.calls == 1 {
		return `{"steps":[{"kind":"fill","ref":1,"text":"hello"},{"kind":"done","result":"ok"}]}`, nil
	}
	return `{"kind":"fill","ref":1,"text":"hello"}`, nil
}
func TestRunDriverRepairsWithoutCDP(t *testing.T) {
	d := &driverFixture{fail: errors.New("element vanished")}
	l := &repairFixture{}
	r := &Runner{LLM: l}
	result, m, err := r.RunDriver(context.Background(), d, 200, Task{Goal: "fill"})
	if err != nil || result != "ok" || d.snapshots != 2 || d.fills != 2 || m.Repairs != 1 {
		t.Fatalf("result=%v metrics=%+v err=%v snapshots=%d fills=%d", result, m, err, d.snapshots, d.fills)
	}
}
func TestRunDriverNeverRepairsUncertainAction(t *testing.T) {
	d := &driverFixture{fail: &StopError{Code: "outcome_uncertain", Message: "click may have landed"}}
	l := &repairFixture{}
	r := &Runner{LLM: l}
	_, m, err := r.RunDriver(context.Background(), d, 200, Task{Goal: "fill"})
	var stop *StopError
	if !errors.As(err, &stop) || l.calls != 1 || d.fills != 1 || m.Repairs != 0 {
		t.Fatalf("err=%v calls=%d fills=%d repairs=%d", err, l.calls, d.fills, m.Repairs)
	}
}
