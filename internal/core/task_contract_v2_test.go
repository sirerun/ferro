package core

import "testing"

func TestLimitsV2DefaultsAndBounds(t *testing.T) {
	l := DefaultLimitsV2()
	if l.RuntimeMS != 90000 || l.ModelRequests != 3 {
		t.Fatalf("unexpected defaults: %+v", l)
	}
	zero := int64(0)
	if _, e := (LimitOverridesV2{Actions: &zero}).ApplyV2(l); e != nil {
		t.Fatal(e)
	}
	service := l
	service.Actions = 10
	bad := int64(11)
	if _, e := (LimitOverridesV2{Actions: &bad}).ApplyV2(service); e == nil {
		t.Fatal("widened service limit accepted")
	}
}
func TestLimitsV2HardDollarAndReserve(t *testing.T) {
	l := DefaultLimitsV2()
	l.HardDollar = true
	if e := l.ValidateV2(); e != ErrUnsupportedHardDollarV2 {
		t.Fatalf("got %v", e)
	}
	n := int64(0)
	l.HardDollar = false
	l.ReserveMicroUSD = &n
	if l.ValidateV2() == nil {
		t.Fatal("zero reserve accepted")
	}
}
func TestUsageV2UnknownDiffersFromZero(t *testing.T) {
	var u RequestUsageV2
	if u.InputTokens != nil {
		t.Fatal("missing usage should be unknown")
	}
	z := int64(0)
	u.InputTokens = &z
	if *u.InputTokens != 0 {
		t.Fatal("zero lost")
	}
}
