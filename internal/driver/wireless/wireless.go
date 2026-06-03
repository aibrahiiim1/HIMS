// Package wireless is the HIMS driver for WLAN controllers (UniFi / Omada /
// Ruckus / Aruba). It fingerprints the controller by HTTP banner + the
// vendor's well-known management ports.
//
// Deep collection (AP inventory, SSIDs, per-AP client counts) is a vendor
// REST transport (login → GET devices/clients) — pure-Go-feasible but not yet
// wired; deferred with a trigger (see PROGRESS Phase 8). Reachability of the
// controller is monitored today.
package wireless

import (
	"strings"

	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/driver"
)

// Driver identifies WLAN controllers.
type Driver struct{}

// New returns the driver.
func New() *Driver { return &Driver{} }

// Name implements driver.Driver.
func (*Driver) Name() string { return "wlan_controller" }

// Template implements driver.Driver.
func (*Driver) Template() string { return "wireless_controller" }

// vendorBanners maps a banner/hint substring to its mgmt port set, so a match
// needs both the name and a plausible port (avoids tagging any "aruba" page).
var vendorBanners = []struct {
	key   string
	ports []int
}{
	{"unifi", []int{8443, 8080, 443}},
	{"omada", []int{8043, 8088, 443}},
	{"ruckus", []int{8443, 443}},
	{"smartzone", []int{8443, 443}},
	{"aruba", []int{4343, 443}}, // Aruba Mobility/Instant controller
	{"mobilitycontroller", []int{4343, 443}},
}

// Fingerprint scores controller evidence:
//
//	78 — a known controller banner together with one of its mgmt ports open
//	60 — the banner alone (port not surfaced by the probe)
func (*Driver) Fingerprint(p driver.Probe) driver.Match {
	banner := strings.ToLower(p.HTTPServer + " " + hint(p, "http_title"))
	for _, v := range vendorBanners {
		if !strings.Contains(banner, v.key) {
			continue
		}
		for _, port := range v.ports {
			if p.HasTCPPort(port) {
				return driver.Match{Confidence: 78, Category: domain.CatWirelessController}
			}
		}
		return driver.Match{Confidence: 60, Category: domain.CatWirelessController}
	}
	return driver.NoMatch
}

func hint(p driver.Probe, k string) string {
	if p.Hints == nil {
		return ""
	}
	return p.Hints[k]
}
