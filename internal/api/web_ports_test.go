package api

import "testing"

func TestHikvisionRecorderPort(t *testing.T) {
	cases := []struct {
		name     string
		ip       string
		category string
		vendor   *string
		want     int
	}{
		{"dvr .12 hikvision", "172.21.210.12", "dvr", strptr("Hikvision"), 8012},
		{"nvr .1 hikvision", "172.21.210.1", "nvr", strptr("Hikvision"), 8001},
		{"nvr .15 vendor-unknown", "172.21.210.15", "nvr", nil, 8015},     // unknown vendor still gets the hint
		{"nvr .15 empty vendor", "172.21.210.15", "nvr", strptr(""), 8015}, // empty vendor too
		{"dvr .254 hikvision", "10.0.0.254", "dvr", strptr("HIKVISION"), 8254},
		{"camera not a recorder", "172.21.210.50", "camera", strptr("Hikvision"), 0},
		{"switch not a recorder", "172.21.96.1", "switch", strptr("Cisco"), 0},
		{"recorder but non-hikvision vendor", "172.21.210.12", "dvr", strptr("Dahua"), 0},
		{"broadcast octet rejected", "172.21.210.255", "dvr", strptr("Hikvision"), 0},
		{"zero octet rejected", "172.21.210.0", "dvr", strptr("Hikvision"), 0},
		{"ipv6 rejected", "fe80::1", "dvr", strptr("Hikvision"), 0},
	}
	for _, c := range cases {
		if got := hikvisionRecorderPort(c.ip, c.category, c.vendor); got != c.want {
			t.Errorf("%s: hikvisionRecorderPort(%q,%q,%v) = %d, want %d", c.name, c.ip, c.category, c.vendor, got, c.want)
		}
	}
}
