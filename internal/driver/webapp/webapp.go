// Package webapp classifies infrastructure web applications that expose no SNMP
// — identified by their HTTP banner (Server header / page title / body). The
// first signal is the Kea DHCP management UI (ISC Kea Control Agent / Stork),
// classified as a DHCP service. It is intentionally extensible: add a row to
// signals to recognize another management web app by banner.
//
// Collection-only intent: this driver only fingerprints (a device that should
// land in the right category instead of "unknown"); deep collection for these
// apps, if ever added, is a separate vendor-API path.
package webapp

import (
	"strings"

	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/driver"
)

// Driver classifies web apps by HTTP banner.
type Driver struct{}

// New returns the driver.
func New() *Driver { return &Driver{} }

// Name implements driver.Driver.
func (*Driver) Name() string { return "webapp" }

// Template implements driver.Driver.
func (*Driver) Template() string { return "application" }

// signal maps banner substrings (lowercased) to a device category.
type signal struct {
	needles  []string
	category domain.DeviceCategory
	conf     int
}

var signals = []signal{
	// ISC Kea DHCP — managed via the Kea Control Agent / the Stork dashboard.
	{needles: []string{"kea", "stork", "isc dhcp", "kea-dhcp"}, category: domain.CatDHCP, conf: 60},
}

// Fingerprint matches the HTTP banner (Server header + title + body snippet)
// against the known web-app signals. Confidence stays moderate (≤60) so an
// authoritative SNMP/enterprise-OID match always outranks a banner guess.
func (*Driver) Fingerprint(p driver.Probe) driver.Match {
	blob := strings.ToLower(p.HTTPServer + " " + hint(p, "http_title") + " " + hint(p, "http_body"))
	if strings.TrimSpace(blob) == "" {
		return driver.NoMatch
	}
	for _, s := range signals {
		for _, n := range s.needles {
			if strings.Contains(blob, n) {
				return driver.Match{Confidence: s.conf, Category: s.category}
			}
		}
	}
	return driver.NoMatch
}

func hint(p driver.Probe, k string) string {
	if p.Hints == nil {
		return ""
	}
	return p.Hints[k]
}
