// Package aruba is the HIMS driver for HP / Aruba / ProCurve switches — the
// reference driver (22 of 26 switches in the target fleet). Phase 0
// implements fingerprinting; Phase 1 adds Collect (interfaces / VLANs /
// MAC / LLDP / port-roles) once the SNMP transport lands.
package aruba

import (
	"strings"

	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/driver"
)

// Driver identifies HP/Aruba/ProCurve switches.
type Driver struct{}

// New returns the driver.
func New() *Driver { return &Driver{} }

// Name implements driver.Driver.
func (*Driver) Name() string { return "aruba_hpe" }

// Template implements driver.Driver.
func (*Driver) Template() string { return "switch" }

// HP/Aruba SNMP private-enterprise OID prefixes:
//
//	.1.3.6.1.4.1.11    — Hewlett-Packard / HP ProCurve
//	.1.3.6.1.4.1.14823 — Aruba Networks (legacy)
//	.1.3.6.1.4.1.47196 — HPE Aruba (ArubaOS-CX)
var enterprisePrefixes = []string{
	"1.3.6.1.4.1.11.",
	"1.3.6.1.4.1.14823.",
	"1.3.6.1.4.1.47196.",
}

var descrKeywords = []string{"aruba", "procurve", "hewlett", "hpe", "hp "}

// Fingerprint implements driver.Driver. Confidence scale:
//
//	90 — SNMP sysObjectID under an HP/Aruba enterprise OID (authoritative)
//	70 — SNMP sysDescr mentions Aruba/ProCurve/HP with SNMP open
//	 0 — not ours
func (*Driver) Fingerprint(p driver.Probe) driver.Match {
	oid := strings.TrimPrefix(strings.TrimSpace(p.SNMPSysObjectID), ".")
	for _, pre := range enterprisePrefixes {
		if strings.HasPrefix(oid, pre) {
			return driver.Match{Confidence: 90, Category: domain.CatSwitch}
		}
	}
	if p.HasTCPPort(161) || len(p.OpenUDPPorts) > 0 {
		d := strings.ToLower(p.SNMPSysDescr)
		for _, kw := range descrKeywords {
			if strings.Contains(d, kw) {
				return driver.Match{Confidence: 70, Category: domain.CatSwitch}
			}
		}
	}
	return driver.NoMatch
}
