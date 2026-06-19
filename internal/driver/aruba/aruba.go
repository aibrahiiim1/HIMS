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
	// HP printers/JetDirect print servers live UNDER the same HP enterprise OID
	// (.1.3.6.1.4.1.11) as HP ProCurve switches — the JetDirect subtree is
	// .11.2.3.9.* — and present "HP ETHERNET MULTI-ENVIRONMENT" in sysDescr. They
	// are PRINTERS, not switches, so the generic HP enterprise prefix / "hp " descr
	// keyword must NOT claim them for the switch driver (that gave a printer the
	// aruba_hpe switch template). Bail FIRST; the printer driver handles them.
	if isHPPrinter(oid, strings.ToLower(p.SNMPSysDescr)) {
		return driver.NoMatch
	}
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

// isHPPrinter reports whether the SNMP identity is an HP printer / JetDirect print
// server rather than an HP/Aruba switch. JetDirect sits under the HP enterprise
// OID subtree .11.2.3.9; the sysDescr markers below are printer-only (no HP/Aruba
// switch reports them), so matching any one is a safe printer signal.
func isHPPrinter(oid, lowerDescr string) bool {
	if strings.HasPrefix(oid, "1.3.6.1.4.1.11.2.3.9") {
		return true
	}
	for _, kw := range []string{"jetdirect", "laserjet", "officejet", "designjet", "ethernet multi-environment"} {
		if strings.Contains(lowerDescr, kw) {
			return true
		}
	}
	return false
}
