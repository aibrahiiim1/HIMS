package wireless

import (
	"testing"

	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/driver"
)

func TestFingerprint_UniFiWithPort(t *testing.T) {
	d := New()
	m := d.Fingerprint(driver.Probe{HTTPServer: "UniFi Network", OpenTCPPorts: []int{8443}})
	if m.Confidence != 78 || m.Category != domain.CatWirelessController {
		t.Fatalf("unifi = %+v; want 78 wireless_controller", m)
	}
}

func TestFingerprint_BannerOnly(t *testing.T) {
	d := New()
	m := d.Fingerprint(driver.Probe{Hints: map[string]string{"http_title": "Omada Controller"}})
	if m.Confidence != 60 {
		t.Fatalf("omada banner-only = %+v; want 60", m)
	}
}

func TestFingerprint_NoMatch(t *testing.T) {
	d := New()
	if m := d.Fingerprint(driver.Probe{HTTPServer: "nginx", OpenTCPPorts: []int{443}}); m.Confidence != 0 {
		t.Fatalf("plain web server should not match; got %+v", m)
	}
}
