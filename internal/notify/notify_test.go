package notify

import "testing"

func p(v int32) *int32 { return &v }

func TestInQuietHours(t *testing.T) {
	cases := []struct {
		name       string
		start, end *int32
		now        int
		want       bool
	}{
		{"no window", nil, nil, 600, false},
		{"equal bounds disables", p(60), p(60), 60, false},
		{"inside same-day", p(540), p(1020), 600, true},   // 09:00-17:00, now 10:00
		{"before same-day", p(540), p(1020), 480, false},  // 08:00
		{"after same-day", p(540), p(1020), 1080, false},  // 18:00
		{"inside wrap (late)", p(1320), p(420), 1380, true}, // 22:00-07:00, now 23:00
		{"inside wrap (early)", p(1320), p(420), 120, true}, // now 02:00
		{"outside wrap", p(1320), p(420), 600, false},       // now 10:00
	}
	for _, c := range cases {
		if got := InQuietHours(c.start, c.end, c.now); got != c.want {
			t.Errorf("%s: InQuietHours=%v want %v", c.name, got, c.want)
		}
	}
}

func TestShouldNotify(t *testing.T) {
	cases := []struct {
		alert, min string
		quiet, want bool
	}{
		{"info", "warning", false, false},     // below threshold
		{"warning", "warning", false, true},   // meets threshold
		{"critical", "warning", false, true},  // above threshold
		{"warning", "warning", true, false},   // quiet suppresses non-critical
		{"critical", "warning", true, true},   // critical pierces quiet hours
		{"warning", "critical", false, false}, // below threshold
	}
	for _, c := range cases {
		if got := ShouldNotify(c.alert, c.min, c.quiet); got != c.want {
			t.Errorf("ShouldNotify(%s,%s,quiet=%v)=%v want %v", c.alert, c.min, c.quiet, got, c.want)
		}
	}
}
