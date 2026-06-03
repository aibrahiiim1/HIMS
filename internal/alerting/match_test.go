package alerting

import "testing"

func strptr(s string) *string { return &s }

func TestMatches(t *testing.T) {
	cases := []struct {
		name string
		rule Rule
		chk  CheckState
		want bool
	}{
		{
			name: "down matches down",
			rule: Rule{TriggerStatus: "down", MinFailures: 1},
			chk:  CheckState{Status: "down", Failures: 2, DeviceCategory: "switch"},
			want: true,
		},
		{
			name: "warning does not match down rule",
			rule: Rule{TriggerStatus: "down", MinFailures: 1},
			chk:  CheckState{Status: "warning", Failures: 1},
			want: false,
		},
		{
			name: "below failure floor",
			rule: Rule{TriggerStatus: "down", MinFailures: 3},
			chk:  CheckState{Status: "down", Failures: 2},
			want: false,
		},
		{
			name: "at failure floor",
			rule: Rule{TriggerStatus: "down", MinFailures: 3},
			chk:  CheckState{Status: "down", Failures: 3},
			want: true,
		},
		{
			name: "category filter excludes",
			rule: Rule{TriggerStatus: "down", MinFailures: 1, DeviceCategory: strptr("firewall")},
			chk:  CheckState{Status: "down", Failures: 1, DeviceCategory: "switch"},
			want: false,
		},
		{
			name: "category filter includes",
			rule: Rule{TriggerStatus: "down", MinFailures: 1, DeviceCategory: strptr("firewall")},
			chk:  CheckState{Status: "down", Failures: 5, DeviceCategory: "firewall"},
			want: true,
		},
	}
	for _, c := range cases {
		if got := Matches(c.rule, c.chk); got != c.want {
			t.Errorf("%s: Matches = %v; want %v", c.name, got, c.want)
		}
	}
}
