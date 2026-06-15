package api

import "testing"

// profileTargetIP underpins on-demand run-collection for unbound, IP-targeted
// vendor profiles: it must extract the IP from the operator's target URL in all
// the shapes the form accepts, and refuse hostnames (which need an explicit
// device binding).
func TestProfileTargetIP(t *testing.T) {
	cases := []struct {
		in     string
		wantIP string
		wantOK bool
	}{
		{"150.0.0.13", "150.0.0.13", true},
		{"https://150.0.0.13", "150.0.0.13", true},
		{"http://150.0.0.13/", "150.0.0.13", true},
		{"https://150.0.0.13/sdk", "150.0.0.13", true},
		{"150.0.0.13:443", "150.0.0.13", true},
		{"https://150.0.0.13:8443/sdk", "150.0.0.13", true},
		{"  https://10.1.2.3  ", "10.1.2.3", true},
		{"https://vcenter.example.com", "", false},
		{"vcenter.example.com:443", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		addr, ok := profileTargetIP(c.in)
		if ok != c.wantOK {
			t.Errorf("profileTargetIP(%q) ok = %v, want %v", c.in, ok, c.wantOK)
			continue
		}
		if ok && addr.String() != c.wantIP {
			t.Errorf("profileTargetIP(%q) = %q, want %q", c.in, addr.String(), c.wantIP)
		}
	}
}
