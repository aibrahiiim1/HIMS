package api

import "testing"

// normMAC must canonicalize any reasonable operator input to the lowercase
// colon-separated form the FDB/ARP tables store, so a MAC pasted from the UI
// (which renders uppercase) still resolves on the Path Finder / global search.
func TestNormMAC(t *testing.T) {
	const want = "f0:b0:52:07:c2:80"
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"already canonical", "f0:b0:52:07:c2:80", want},
		{"uppercase colon (UI form)", "F0:B0:52:07:C2:80", want},
		{"dash separated", "f0-b0-52-07-c2-80", want},
		{"uppercase dash", "F0-B0-52-07-C2-80", want},
		{"cisco dotted", "f0b0.5207.c280", want},
		{"no separators", "f0b05207c280", want},
		{"no separators upper", "F0B05207C280", want},
		{"surrounding spaces", "  F0:B0:52:07:C2:80  ", want},
		// Non-MAC inputs pass through (lowercased/trimmed) so hostname search works.
		{"hostname passthrough", "SW-CORE-01", "sw-core-01"},
		{"partial hex not a mac", "f0:b0:52", "f0:b0:52"},
	}
	for _, c := range cases {
		if got := normMAC(c.in); got != c.want {
			t.Errorf("%s: normMAC(%q) = %q; want %q", c.name, c.in, got, c.want)
		}
	}
}
