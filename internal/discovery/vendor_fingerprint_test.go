package discovery

import (
	"strings"
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

// TestApplyFingerprints_WinSoftphoneNeverPBX is the 150.0.0.132 regression: a
// Windows PC with a softphone (RPC/SMB/RDP + SIP 5060, no telnet appliance evidence)
// was flipped endpoint→pbx during a broad scan by a STALE port fingerprint
// (5060→voip, voip→pbx) that lived in the vendor_fingerprints DB after being removed
// from the code catalog. The Windows-management guard in applyFingerprints must
// suppress a bare-PORT voice fingerprint on such a host so it stays endpoint.
func TestApplyFingerprints_WinSoftphoneNeverPBX(t *testing.T) {
	// The stale/rogue library entry that reproduces the flip (as merged from the DB).
	staleLib := []fingerprint.Print{{Kind: fingerprint.KindPort, Pattern: "5060", Vendor: "Generic", DeviceType: "voip", Confidence: 50}}

	r := HostResult{
		// What classify.OpenPorts produced for the Windows PC: endpoint (RDP) @45.
		Match:     driver.Match{Category: domain.CatEndpoint, Confidence: 45},
		OpenPorts: []int{135, 445, 3389, 5060},
	}
	applyFingerprints(&r, staleLib)
	if r.Match.Category != domain.CatEndpoint {
		t.Fatalf("Windows PC + softphone must stay endpoint, got %q (conf %d)", r.Match.Category, r.Match.Confidence)
	}

	// A REAL SIP phone (only 5060, no Windows surface) is unaffected — the guard is
	// scoped to Windows-management hosts, so the port fingerprint still applies there.
	phone := HostResult{Match: driver.Match{Category: domain.CatIPPhone, Confidence: 40}, OpenPorts: []int{5060}}
	applyFingerprints(&phone, staleLib)
	if phone.Match.Category == domain.CatEndpoint {
		t.Fatalf("a bare SIP phone must not be forced to endpoint by the guard, got %q", phone.Match.Category)
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

// TestApplyFingerprintsExplicitModelWins: when the winning fingerprint pins an
// explicit model, it is used verbatim — NOT the sysDescr-derived one.
func TestApplyFingerprintsExplicitModelWins(t *testing.T) {
	r := HostResult{
		Match: driver.Match{Category: domain.CatSwitch, Confidence: 50},
		Probe: driver.Probe{
			SNMPSysObjectID: "1.3.6.1.4.1.4242.1",
			SNMPSysDescr:    "Acme Box - PARSED-MODEL, version 1", // would parse to "PARSED-MODEL"
		},
	}
	lib := []fingerprint.Print{
		{Kind: fingerprint.KindOID, Pattern: "1.3.6.1.4.1.4242.1", Vendor: "Acme", DeviceType: "router", Confidence: 96, Model: "PINNED-9000"},
	}
	applyFingerprints(&r, lib)
	if r.Model != "PINNED-9000" {
		t.Fatalf("expected explicit model PINNED-9000 to win over sysDescr-derived, got %q", r.Model)
	}
	if r.Vendor != "Acme" || r.Match.Category != domain.CatRouter {
		t.Fatalf("expected Acme/router, got %q/%q", r.Vendor, r.Match.Category)
	}
}

// TestApplyFingerprintsModelFallsBackToSysDescr: no explicit model → derive from
// sysDescr (the VE6120 path).
func TestApplyFingerprintsModelFallsBackToSysDescr(t *testing.T) {
	r := HostResult{
		Match: driver.Match{Category: domain.CatSwitch, Confidence: 90},
		Probe: driver.Probe{
			SNMPSysObjectID: "1.3.6.1.4.1.1916.2.284",
			SNMPSysDescr:    "Extreme Networks ExtremeCloud IQ Controller - VE6120 Medium, System Version 10.05.04.0006",
		},
	}
	applyFingerprints(&r, fingerprint.Library())
	if r.Model != "VE6120 Medium" {
		t.Fatalf("expected sysDescr-derived VE6120 Medium, got %q", r.Model)
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
	// Phase 3: even on a no-op, the evidence record exists (so the UI can explain
	// "nothing matched" rather than showing a blank panel).
	if r.Classification == nil {
		t.Fatal("expected a Classification record even with no match")
	}
	if len(r.Classification.Winners) != 0 || len(r.Classification.Rejected) != 0 {
		t.Fatalf("expected no winners/rejected, got %+v", r.Classification)
	}
}

// TestClassificationDetail_WinnerRecorded: a positive OID match records the
// evidence channels + the winning fingerprint and marks the source "fingerprint".
func TestClassificationDetail_WinnerRecorded(t *testing.T) {
	r := HostResult{
		Match: driver.Match{Category: domain.CatUnknown, Confidence: 10},
		Probe: driver.Probe{SNMPSysObjectID: "1.3.6.1.4.1.4242.1", SNMPSysDescr: "Acme Box v1"},
	}
	lib := []fingerprint.Print{
		{Kind: fingerprint.KindOID, Pattern: "1.3.6.1.4.1.4242.1", Vendor: "Acme", DeviceType: "router", Confidence: 95},
	}
	applyFingerprints(&r, lib)
	d := r.Classification
	if d == nil {
		t.Fatal("expected Classification detail")
	}
	if d.Evidence.SysObjectID != "1.3.6.1.4.1.4242.1" {
		t.Errorf("evidence sysObjectID not captured: %q", d.Evidence.SysObjectID)
	}
	if len(d.Winners) != 1 || d.Winners[0].DeviceType != "router" {
		t.Fatalf("expected one router winner, got %+v", d.Winners)
	}
	if d.FinalSource != "fingerprint" {
		t.Errorf("expected final_source fingerprint, got %q", d.FinalSource)
	}
}

// TestClassificationDetail_RejectedByExclusion: a broad rule whose positive
// pattern matches but is suppressed by an exclusion is recorded as rejected with
// a reason naming the exclusion — and, with no surviving winner, the rejected
// candidate's type becomes the "likely type" for the unknown bucket.
func TestClassificationDetail_RejectedByExclusion(t *testing.T) {
	r := HostResult{
		Match: driver.Match{Category: domain.CatUnknown, Confidence: 10},
		Probe: driver.Probe{
			SNMPSysObjectID: "1.3.6.1.4.1.11.2.3.9.1", // HP JetDirect sub-tree
			SNMPSysDescr:    "HP ETHERNET MULTI-ENVIRONMENT",
		},
	}
	lib := []fingerprint.Print{{
		Kind: fingerprint.KindOID, Pattern: "1.3.6.1.4.1.11", Vendor: "Aruba/HPE", DeviceType: "switch", Confidence: 78,
		Exclusions: []fingerprint.Exclusion{{Kind: fingerprint.KindOID, Pattern: "1.3.6.1.4.1.11.2.3.9"}},
	}}
	applyFingerprints(&r, lib)
	d := r.Classification
	if d == nil || len(d.Winners) != 0 {
		t.Fatalf("expected no winners (rule excluded), got %+v", d)
	}
	if len(d.Rejected) != 1 || d.Rejected[0].DeviceType != "switch" {
		t.Fatalf("expected one rejected switch candidate, got %+v", d.Rejected)
	}
	if !strings.Contains(d.Rejected[0].Reason, "excluded by") {
		t.Errorf("expected an exclusion reason, got %q", d.Rejected[0].Reason)
	}
	if d.LikelyType != "switch" {
		t.Errorf("expected likely_type switch (top rejected), got %q", d.LikelyType)
	}
	// The host's category was NOT overridden to switch by an excluded rule.
	if r.Match.Category == domain.CatSwitch {
		t.Error("excluded rule must not set the category")
	}
}

// TestClassificationDetail_RejectedRunnerUp: when two different-type rules match,
// the higher-confidence one wins and the other is recorded as a rejected runner-up.
func TestClassificationDetail_RejectedRunnerUp(t *testing.T) {
	r := HostResult{
		Match: driver.Match{Category: domain.CatUnknown, Confidence: 10},
		Probe: driver.Probe{
			SNMPSysObjectID: "1.3.6.1.4.1.11",  // generic HP prefix → switch @78
			SNMPSysDescr:    "HP LaserJet MFP", // service marker → printer @90
		},
	}
	lib := []fingerprint.Print{
		{Kind: fingerprint.KindOID, Pattern: "1.3.6.1.4.1.11", Vendor: "HPE", DeviceType: "switch", Confidence: 78},
		{Kind: fingerprint.KindService, Pattern: "laserjet", Vendor: "HP", DeviceType: "printer", Confidence: 90},
	}
	applyFingerprints(&r, lib)
	d := r.Classification
	if d == nil || len(d.Winners) == 0 || d.Winners[0].DeviceType != "printer" {
		t.Fatalf("expected printer to win, got %+v", d)
	}
	found := false
	for _, rj := range d.Rejected {
		if rj.DeviceType == "switch" && strings.Contains(rj.Reason, "lower confidence") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected switch rejected as lower-confidence runner-up, got %+v", d.Rejected)
	}
}
