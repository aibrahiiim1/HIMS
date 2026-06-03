package webapp

import (
	"testing"

	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/driver"
)

func TestFingerprint_Kea(t *testing.T) {
	cases := []struct {
		name  string
		probe driver.Probe
		want  domain.DeviceCategory
	}{
		{"kea title", driver.Probe{Hints: map[string]string{"http_title": "Kea Control Agent"}}, domain.CatDHCP},
		{"stork body", driver.Probe{Hints: map[string]string{"http_body": "<app-root>stork</app-root>"}}, domain.CatDHCP},
		{"server header", driver.Probe{HTTPServer: "ISC Kea-DHCP"}, domain.CatDHCP},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := New().Fingerprint(tc.probe)
			if m.Confidence == 0 || m.Category != tc.want {
				t.Fatalf("got %+v; want category %s", m, tc.want)
			}
		})
	}
}

func TestFingerprint_NoBannerNoMatch(t *testing.T) {
	if New().Fingerprint(driver.Probe{}).Confidence != 0 {
		t.Fatal("empty banner must not match")
	}
	if New().Fingerprint(driver.Probe{Hints: map[string]string{"http_title": "Login"}}).Confidence != 0 {
		t.Fatal("unrelated web app must not match")
	}
}

// A real switch (authoritative SNMP OID) must outrank a webapp banner guess —
// the webapp confidence is intentionally ≤60.
func TestFingerprint_ConfidenceModest(t *testing.T) {
	if m := New().Fingerprint(driver.Probe{HTTPServer: "kea"}); m.Confidence > 60 {
		t.Fatalf("webapp confidence %d should stay ≤60", m.Confidence)
	}
}
