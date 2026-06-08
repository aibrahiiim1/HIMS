package monitoring

import "testing"

func TestEvaluate_SuccessResets(t *testing.T) {
	// A success always returns up + zero, regardless of prior failures.
	for _, prev := range []int{0, 1, 5} {
		got, f := Evaluate(true, prev, 2)
		if got != StatusUp || f != 0 {
			t.Fatalf("Evaluate(ok, prev=%d) = %v,%d; want up,0", prev, got, f)
		}
	}
}

func TestEvaluate_WarningBandThenDown(t *testing.T) {
	// downThreshold=2: first failure → warning, second → down.
	got, f := Evaluate(false, 0, 2)
	if got != StatusWarning || f != 1 {
		t.Fatalf("first failure = %v,%d; want warning,1", got, f)
	}
	got, f = Evaluate(false, f, 2)
	if got != StatusDown || f != 2 {
		t.Fatalf("second failure = %v,%d; want down,2", got, f)
	}
	// Stays down, counter keeps climbing.
	got, f = Evaluate(false, f, 2)
	if got != StatusDown || f != 3 {
		t.Fatalf("third failure = %v,%d; want down,3", got, f)
	}
}

func TestEvaluate_ThresholdOneNoWarningBand(t *testing.T) {
	got, f := Evaluate(false, 0, 1)
	if got != StatusDown || f != 1 {
		t.Fatalf("threshold=1 first failure = %v,%d; want down,1", got, f)
	}
}

func TestEvaluate_ThresholdClampedToOne(t *testing.T) {
	// A nonsensical threshold of 0 behaves like 1 (first failure = down).
	got, _ := Evaluate(false, 0, 0)
	if got != StatusDown {
		t.Fatalf("threshold=0 = %v; want down (clamped to 1)", got)
	}
}

func TestEvaluate_RecoveryClearsDown(t *testing.T) {
	// down → success → up with counter cleared, so the next blip starts
	// the warning band over (no instant re-down from stale counter).
	_, f := Evaluate(false, 1, 2) // now down, f=2
	got, f := Evaluate(true, f, 2)
	if got != StatusUp || f != 0 {
		t.Fatalf("recovery = %v,%d; want up,0", got, f)
	}
	got, f = Evaluate(false, f, 2)
	if got != StatusWarning || f != 1 {
		t.Fatalf("post-recovery blip = %v,%d; want warning,1", got, f)
	}
}

func TestWorstAndRollup(t *testing.T) {
	cases := []struct {
		in   []Status
		want Status
	}{
		{nil, StatusUnknown},
		{[]Status{StatusUp, StatusUp}, StatusUp},
		{[]Status{StatusUp, StatusWarning}, StatusWarning},
		{[]Status{StatusWarning, StatusDown}, StatusDown},
		{[]Status{StatusUp, StatusUnknown}, StatusUnknown},
		{[]Status{StatusUp, StatusUnknown, StatusDown}, StatusDown},
	}
	for _, c := range cases {
		if got := RollupDevice(c.in); got != c.want {
			t.Errorf("RollupDevice(%v) = %v; want %v", c.in, got, c.want)
		}
	}
}

func TestRollupDeviceWithSupplemental(t *testing.T) {
	cases := []struct {
		name        string
		reach, supp []Status
		want        Status
	}{
		{"no checks at all", nil, nil, StatusUnknown},
		{"reachable, no extras", []Status{StatusUp}, nil, StatusUp},
		// The headline case: device reachable, an EXTRA check is down → the device
		// is DEGRADED (warning), never offline. It must not inflate the down count.
		{"reachable + extra down → warning, not down", []Status{StatusUp}, []Status{StatusDown}, StatusWarning},
		{"reachable + extra warning → warning", []Status{StatusUp}, []Status{StatusWarning}, StatusWarning},
		{"reachable + extra up → up", []Status{StatusUp}, []Status{StatusUp}, StatusUp},
		// Reachability down ALWAYS wins — that's a real offline.
		{"reachability down → down (offline)", []Status{StatusDown}, []Status{StatusUp}, StatusDown},
		{"reachability down + extra down → down", []Status{StatusDown}, []Status{StatusDown}, StatusDown},
		// Multiple reachability checks roll up worst-first.
		{"two reachability, one down → down", []Status{StatusUp, StatusDown}, nil, StatusDown},
		// Unknown reachability degrades to warning if an extra is failing.
		{"unknown reach + extra down → warning", []Status{StatusUnknown}, []Status{StatusDown}, StatusWarning},
		// Degenerate: only supplemental checks → capped at warning (never down).
		{"only extras, down → warning (capped)", nil, []Status{StatusDown}, StatusWarning},
		{"only extras, up → up", nil, []Status{StatusUp}, StatusUp},
	}
	for _, c := range cases {
		if got := RollupDeviceWithSupplemental(c.reach, c.supp); got != c.want {
			t.Errorf("%s: RollupDeviceWithSupplemental(%v,%v) = %v; want %v", c.name, c.reach, c.supp, got, c.want)
		}
	}
}
