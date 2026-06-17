package api

import (
	"testing"

	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
	"github.com/google/uuid"
)

// Regression suite for the 172.21.60.0/24 scan-hardening pass. Each test pins one
// class of bug that previously left enrolled Windows endpoints silently
// unmanaged or mislabeled. Pure functions only (no DB) — the live acceptance
// scan covers the SQL/orchestration paths these guard.

// Class 1 — a Windows host must get an OS-collection attempt even when the scan
// captured NO management port. osCollectionCandidate owns the decision and takes
// no port list, so it cannot regress to port-gating (the original bug that left
// late/slow-probed endpoints enrolled with zero attempts → stuck needs_credential).
func TestOSCollectionCandidate_NoPortGating(t *testing.T) {
	cases := []struct {
		name                          string
		dev                           db.Device
		boundOS, legacyWSMan, special bool
		want                          bool
	}{
		// os_family=windows, no ports, no bound cred → still a candidate.
		{"windows-osfamily", db.Device{OsFamily: "windows"}, false, false, false, true},
		// The .106/.119 case: enrolled endpoint whose os_family is still blank
		// (not yet collected) — must still be attempted.
		{"endpoint-blank-osfamily", db.Device{Category: "endpoint"}, false, false, false, true},
		// Bound WinRM/SSH credential on a non-endpoint (e.g. server) → candidate.
		{"bound-os-cred", db.Device{Category: "server"}, true, false, false, true},
		// Legacy WSMan-2.0 host (auth ok, op fault) → candidate (routes to agent).
		{"legacy-wsman", db.Device{Category: "server"}, false, true, false, true},
		// Non-Windows, non-bound (printer) → NOT a generic OS-collection candidate.
		{"printer", db.Device{Category: "printer", OsFamily: ""}, false, false, false, false},
		// Specialized appliance takes its own branch even if endpoint-like.
		{"specialized-camera", db.Device{Category: "endpoint"}, false, false, true, false},
	}
	for _, c := range cases {
		if got := osCollectionCandidate(c.dev, c.boundOS, c.legacyWSMan, c.special); got != c.want {
			t.Errorf("%s: osCollectionCandidate=%v want %v", c.name, got, c.want)
		}
	}
}

// Class 3 + 5 — a successful OS inventory whose collection_method is winrm OR wmi
// must produce a PROVEN access signal, and both the direct-WinRM and agent-WMI
// paths must derive the SAME managed state. access.sql maps os_inventory
// collection_method → protocol (winrm/winrm-native → "winrm", wmi → "wmi") with
// source "evidence"; this asserts buildAccessMap + deriveManagement treat that
// evidence as proven-managed for BOTH protocols.
func TestEvidenceWinRMAndWMIBothManaged(t *testing.T) {
	winID, wmiID := uuid.New(), uuid.New()
	rows := []db.ListDeviceAccessSignalsRow{
		{DeviceID: winID, Protocol: "winrm", Source: "evidence"}, // direct WinRM collect
		{DeviceID: wmiID, Protocol: "wmi", Source: "evidence"},   // agent-WMI collect
	}
	am := buildAccessMap(rows)
	if !am[winID].provenHas("winrm") {
		t.Error("winrm evidence must be proven-managed")
	}
	if !am[wmiID].provenHas("wmi") {
		t.Error("wmi evidence must be proven-managed")
	}

	mk := func(id uuid.UUID) *statusMaps {
		return &statusMaps{access: am, test: map[uuid.UUID]*deviceTestStatus{},
			onlineSites: map[uuid.UUID]bool{}, anySites: map[uuid.UUID]bool{}}
	}
	if st, by := mk(winID).deriveManagement(db.Device{ID: winID, OsFamily: "windows", Category: "endpoint"}); st != MgmtManaged || len(by) != 1 || by[0] != "winrm" {
		t.Errorf("direct-WinRM host: got %s %v, want managed/[winrm]", st, by)
	}
	if st, by := mk(wmiID).deriveManagement(db.Device{ID: wmiID, OsFamily: "windows", Category: "endpoint"}); st != MgmtManaged || len(by) != 1 || by[0] != "wmi" {
		t.Errorf("agent-WMI host: got %s %v, want managed/[wmi]", st, by)
	}
}

// Class 4 — a device that was ACTUALLY attempted must never show the misleading
// needs_credential: an auth rejection → credential_failed; a bound credential
// that never collected → collection_failed. needs_credential is reserved for a
// credentialed-class host with NO attempt and NO binding (genuinely "supply a
// credential"), never for a host whose credential was tried.
func TestAttemptedHostNotLabeledNeedsCredential(t *testing.T) {
	id, cred := uuid.New(), uuid.New()
	mk := func(ts *deviceTestStatus) *statusMaps {
		m := &statusMaps{access: map[uuid.UUID]*deviceAccess{}, test: map[uuid.UUID]*deviceTestStatus{},
			onlineSites: map[uuid.UUID]bool{}, anySites: map[uuid.UUID]bool{}}
		if ts != nil {
			m.test[id] = ts
		}
		return m
	}
	// Auth was attempted and rejected → credential_failed (NOT needs_credential).
	authFail := &deviceTestStatus{tested: true, authFailed: true, kindCategory: map[string]string{"winrm": "auth_failed"}}
	if st, _ := mk(authFail).deriveManagement(db.Device{ID: id, OsFamily: "windows", Category: "endpoint"}); st != MgmtCredentialFailed {
		t.Errorf("attempted+auth_failed: got %s, want credential_failed", st)
	}
	// The real 172.21.60.49/.50 case: a Windows endpoint the agent TRIED (WMI/WinRM
	// over New-CimSession) but the host firewall blocked it — a non-auth failure
	// (wmi_error), no credential bound. Must be collection_failed (→ "WMI blocked"
	// in the report), NOT the misleading needs_credential that points at credentials.
	wmiBlocked := &deviceTestStatus{tested: true, authFailed: false, failedKinds: map[string]bool{"wmi": true}, kindCategory: map[string]string{"wmi": "wmi_error"}}
	if st, _ := mk(wmiBlocked).deriveManagement(db.Device{ID: id, OsFamily: "windows", Category: "endpoint"}); st != MgmtCollectionFailed {
		t.Errorf("attempted+wmi_error (firewall block), unbound: got %s, want collection_failed (not needs_credential)", st)
	}
	// A credential is bound but nothing collected → collection_failed (NOT needs_credential).
	if st, _ := mk(&deviceTestStatus{}).deriveManagement(db.Device{ID: id, Category: "server", CredentialID: &cred}); st != MgmtCollectionFailed {
		t.Errorf("bound-but-uncollected: got %s, want collection_failed", st)
	}
	// Sanity: a credentialed-class host with NO attempt and NO binding IS the
	// honest needs_credential ("supply a credential") — the only path to it.
	if st, _ := mk(nil).deriveManagement(db.Device{ID: id, Category: "server"}); st != MgmtNeedsCredential {
		t.Errorf("no-attempt credentialed class: got %s, want needs_credential", st)
	}
}

// Class 6 — the Connectivity report (which lists failed credential attempts as
// history) must not contradict Device Detail's management state. Management is
// derived from PROVEN evidence, so a managed device that also has a failed
// credential test on record stays managed — the failure is history, not a
// downgrade. This pins that the two views share one source of truth.
func TestManagedDespiteFailedCredentialHistory(t *testing.T) {
	id := uuid.New()
	am := buildAccessMap([]db.ListDeviceAccessSignalsRow{
		{DeviceID: id, Protocol: "winrm", Source: "evidence"}, // proven by collection
	})
	m := &statusMaps{
		access: am,
		test: map[uuid.UUID]*deviceTestStatus{
			// An earlier WMI credential attempt failed — present in report history.
			id: {tested: true, authFailed: true, failedKinds: map[string]bool{"wmi": true},
				kindCategory: map[string]string{"wmi": "auth_failed"}},
		},
		onlineSites: map[uuid.UUID]bool{}, anySites: map[uuid.UUID]bool{},
	}
	if st, by := m.deriveManagement(db.Device{ID: id, OsFamily: "windows", Category: "endpoint"}); st != MgmtManaged || len(by) != 1 || by[0] != "winrm" {
		t.Errorf("managed-with-failed-history: got %s %v, want managed/[winrm]", st, by)
	}
}
