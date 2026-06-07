package discovery

import (
	"testing"

	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/driver"
	"github.com/coralsearesorts/hims/internal/fingerprint"
)

// TestApplyFingerprintsOverridesExtremeSwitch is the .100 regression: the
// extremesw driver fingerprints any Extreme PEN (.1916) host as a switch @90, so
// the ExtremeCloud IQ Controller VE6120 (sysObjectID .1916.2.284) lands as a
// switch. The vendor-fingerprint override must reclassify it as a
// wireless_controller and stamp the canonical vendor + product model.
func TestApplyFingerprintsOverridesExtremeSwitch(t *testing.T) {
	r := HostResult{
		// What the driver registry produced for this host.
		Match: driver.Match{Category: domain.CatSwitch, Confidence: 90},
		Probe: driver.Probe{
			SNMPSysObjectID: "1.3.6.1.4.1.1916.2.284",
			SNMPSysDescr:    "Extreme Networks ExtremeCloud IQ Controller - VE6120 Medium, System Version 10.05.04.0006",
			SNMPSysName:     "XIQC.coralsearesorts.com",
		},
	}
	applyFingerprints(&r, fingerprint.Library())

	if r.Match.Category != domain.CatWirelessController {
		t.Fatalf("expected category wireless_controller, got %q (conf %d)", r.Match.Category, r.Match.Confidence)
	}
	if r.Match.Confidence < 90 {
		t.Fatalf("expected the override to win on confidence, got %d", r.Match.Confidence)
	}
	if r.Vendor != "Extreme Networks" {
		t.Errorf("expected vendor Extreme Networks, got %q", r.Vendor)
	}
	if r.Model != "VE6120 Medium" {
		t.Errorf("expected model VE6120 Medium, got %q", r.Model)
	}
}

// TestApplyFingerprintsKeepsRealSwitch guards the precedence rule (req #7/#8):
// a genuine Extreme switch (generic .1916 PEN, no product print) keeps category
// switch and is NOT forced to wireless_controller.
func TestApplyFingerprintsKeepsRealSwitch(t *testing.T) {
	r := HostResult{
		Match: driver.Match{Category: domain.CatSwitch, Confidence: 90},
		Probe: driver.Probe{
			SNMPSysObjectID: "1.3.6.1.4.1.1916.1.1.100", // generic Extreme switch sub-tree
			SNMPSysDescr:    "ExtremeXOS (X440-G2) version 31.7",
		},
	}
	applyFingerprints(&r, fingerprint.Library())

	if r.Match.Category != domain.CatSwitch {
		t.Fatalf("a real Extreme switch must stay a switch, got %q", r.Match.Category)
	}
}

// TestApplyFingerprintsNoEvidenceNoChange: with no SNMP evidence the override is
// a no-op and the driver's verdict survives untouched.
func TestApplyFingerprintsNoEvidenceNoChange(t *testing.T) {
	r := HostResult{Match: driver.Match{Category: domain.CatServer, Confidence: 55}}
	applyFingerprints(&r, fingerprint.Library())
	if r.Match.Category != domain.CatServer || r.Vendor != "" || r.Model != "" {
		t.Fatalf("expected no change without evidence, got %+v vendor=%q model=%q", r.Match, r.Vendor, r.Model)
	}
}
