package operations

import (
	"testing"
	"time"
)

func TestSLAMinutes(t *testing.T) {
	if SLAMinutes("critical") != 240 {
		t.Errorf("critical = %d, want 240", SLAMinutes("critical"))
	}
	if SLAMinutes("low") != 10080 {
		t.Errorf("low = %d, want 10080", SLAMinutes("low"))
	}
	if SLAMinutes("weird") != SLAMinutes("medium") {
		t.Error("unknown priority should default to medium policy")
	}
}

func TestComputeSLAStatusActive(t *testing.T) {
	created := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	// critical → 4h window. 1h in: on_track.
	if s := ComputeSLAStatus(created, "critical", "open", nil, created.Add(1*time.Hour)); s != SLAOnTrack {
		t.Errorf("1h in = %s, want on_track", s)
	}
	// 3h15m in (>75% of 4h): due_soon.
	if s := ComputeSLAStatus(created, "critical", "open", nil, created.Add(195*time.Minute)); s != SLADueSoon {
		t.Errorf("3h15m in = %s, want due_soon", s)
	}
	// 5h in: breached.
	if s := ComputeSLAStatus(created, "critical", "in_progress", nil, created.Add(5*time.Hour)); s != SLABreached {
		t.Errorf("5h in = %s, want breached", s)
	}
}

func TestComputeSLAStatusTerminal(t *testing.T) {
	created := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	resolvedOK := created.Add(2 * time.Hour)
	if s := ComputeSLAStatus(created, "critical", "solved", &resolvedOK, created.Add(99*time.Hour)); s != SLAMet {
		t.Errorf("resolved within window = %s, want met", s)
	}
	resolvedLate := created.Add(9 * time.Hour)
	if s := ComputeSLAStatus(created, "critical", "closed", &resolvedLate, created.Add(99*time.Hour)); s != SLABreached {
		t.Errorf("resolved after window = %s, want breached", s)
	}
}
