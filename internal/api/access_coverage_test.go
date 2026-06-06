package api

import (
	"testing"
	"time"

	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
	"github.com/google/uuid"
)

func TestBuildAccessMap_MergeAndSourcePriority(t *testing.T) {
	d1, d2, d3 := uuid.New(), uuid.New(), uuid.New()
	rows := []db.ListDeviceAccessSignalsRow{
		// d1: bound snmp + evidence ssh (two protocols)
		{DeviceID: d1, Protocol: "snmp_v2c", Source: "bound_credential"},
		{DeviceID: d1, Protocol: "ssh", Source: "evidence"},
		// d2: same protocol from both sources — bound must win regardless of order
		{DeviceID: d2, Protocol: "winrm", Source: "evidence"},
		{DeviceID: d2, Protocol: "winrm", Source: "bound_credential"},
		// d3: only evidence
		{DeviceID: d3, Protocol: "onvif", Source: "evidence"},
	}
	m := buildAccessMap(rows)

	if !m[d1].managed() || len(m[d1].protocols) != 2 {
		t.Fatalf("d1 should have 2 protocols, got %+v", m[d1])
	}
	if m[d1].protocols["snmp_v2c"] != "bound_credential" || m[d1].protocols["ssh"] != "evidence" {
		t.Errorf("d1 sources wrong: %+v", m[d1].protocols)
	}
	if m[d2].protocols["winrm"] != "bound_credential" {
		t.Errorf("d2 winrm source = %q, want bound_credential (bound must win)", m[d2].protocols["winrm"])
	}
	if !m[d3].has("onvif") || m[d3].protocols["onvif"] != "evidence" {
		t.Errorf("d3 onvif wrong: %+v", m[d3])
	}
	// A device with no signals is absent → unmanaged.
	var nilAccess *deviceAccess
	if nilAccess.managed() {
		t.Error("nil deviceAccess must be unmanaged")
	}
}

// TestManagedRequiresProven pins the proven-only management rule used by the
// Management Access Coverage card, by_protocol counts, and the Inventory access
// filters: a bound credential alone is NEVER Managed — only a successful test
// (test_result) or authenticated-collection evidence counts. A failed/302 test
// produces no test_result signal, so it stays unmanaged.
//
// HTTP Basic / HTTP is special: a 2xx credential TEST is a web login, not
// inventory collection, so it does NOT make a device Managed on its own — only
// collection EVIDENCE over HTTP does. A successful test of a real management
// protocol (ssh/snmp/winrm/…) still counts.
func TestManagedRequiresProven(t *testing.T) {
	boundFailed, boundUntested, httpAuthOnly, httpEvidence, provenSSH := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	rows := []db.ListDeviceAccessSignalsRow{
		// bound http_basic credential whose latest test was 302/auth_failed:
		// only the binding signal exists (a failed test emits no test_result row).
		{DeviceID: boundFailed, Protocol: "http_basic", Source: "bound_credential"},
		// bound credential, never tested → only a binding signal.
		{DeviceID: boundUntested, Protocol: "ssh", Source: "bound_credential"},
		// latest 2xx http_basic success → a test_result signal, but a web login is
		// NOT management: must stay unmanaged (the reported bug).
		{DeviceID: httpAuthOnly, Protocol: "http_basic", Source: "test_result"},
		// http_basic with collection EVIDENCE (a real BMC/Redfish collector pulled
		// data) → genuinely managed over HTTP.
		{DeviceID: httpEvidence, Protocol: "http_basic", Source: "evidence"},
		// a real management protocol proven by a successful test → managed.
		{DeviceID: provenSSH, Protocol: "ssh", Source: "test_result"},
	}
	m := buildAccessMap(rows)

	if m[boundFailed].hasProven() {
		t.Error("bound http_basic with a failed/302 latest test must NOT count as managed")
	}
	if m[boundUntested].hasProven() {
		t.Error("bound-but-untested credential must NOT count as managed")
	}
	if m[httpAuthOnly].hasProven() {
		t.Error("HTTP Basic 2xx web login (test_result only) must NOT count as managed — nothing was collected")
	}
	if !m[httpEvidence].hasProven() || !m[httpEvidence].provenHas("http_basic") {
		t.Error("http_basic with collection evidence must count as managed")
	}
	if !m[provenSSH].hasProven() || !m[provenSSH].provenHas("ssh") {
		t.Error("a successful ssh test must count as managed")
	}
	// The bindings/methods are still recorded (credentials are never deleted, and
	// the auth-only HTTP method is still a known method) — they just don't make the
	// device Managed.
	if !m[boundFailed].has("http_basic") || !m[boundUntested].has("ssh") || !m[httpAuthOnly].has("http_basic") {
		t.Error("bound credentials and auth-only HTTP methods must still be recorded")
	}
}

func TestFilterDevicesByAccess(t *testing.T) {
	managedID, unmanagedID := uuid.New(), uuid.New()
	credID := uuid.New()
	rows := []db.Device{
		{ID: managedID, CredentialID: &credID},
		{ID: unmanagedID, CredentialID: nil},
	}
	am := map[uuid.UUID]*deviceAccess{
		// Managed requires PROVEN access (not a bare binding) — mark snmp_v2c proven.
		managedID: {protocols: map[string]string{"snmp_v2c": "test_result"}, proven: map[string]bool{"snmp_v2c": true}},
		// unmanagedID absent → unmanaged
	}

	only := func(got []db.Device, want uuid.UUID, label string) {
		if len(got) != 1 || got[0].ID != want {
			t.Errorf("%s: got %d rows %v, want [%s]", label, len(got), ids(got), want)
		}
	}

	tm := map[uuid.UUID]*deviceTestStatus{}
	now := time.Unix(1_700_000_000, 0)
	f := func(access, proto, issue string) []db.Device {
		return filterDevicesByAccess(rows, am, tm, now, access, proto, issue)
	}

	only(f("managed", "", ""), managedID, "access=managed")
	only(f("unmanaged", "", ""), unmanagedID, "access=unmanaged")
	only(f("", "snmp_v2c", ""), managedID, "accessProtocol=snmp_v2c")
	only(f("", "", "no_credential_bound"), unmanagedID, "accessIssue=no_credential_bound")

	// Unknown protocol → no matches.
	if got := f("", "winrm", ""); len(got) != 0 {
		t.Errorf("accessProtocol=winrm should match nothing, got %d", len(got))
	}
	// not_tested: with no test history, both devices qualify (never tested).
	if got := f("", "", "not_tested"); len(got) != 2 {
		t.Errorf("accessIssue=not_tested should match both untested devices, got %d", len(got))
	}
	// credential_failed: no auth-failure history yet → nothing.
	if got := f("", "", "credential_failed"); len(got) != 0 {
		t.Errorf("accessIssue=credential_failed should match nothing without history, got %d", len(got))
	}
	// No params → unchanged.
	if got := f("", "", ""); len(got) != 2 {
		t.Errorf("no params should pass all, got %d", len(got))
	}
}

func TestFilterDevicesByAccess_TestHistory(t *testing.T) {
	failID, staleID, okID := uuid.New(), uuid.New(), uuid.New()
	rows := []db.Device{{ID: failID, Category: "switch"}, {ID: staleID, Category: "switch"}, {ID: okID, Category: "switch"}}
	now := time.Unix(1_700_000_000, 0)
	am := map[uuid.UUID]*deviceAccess{
		staleID: {protocols: map[string]string{"snmp_v2c": "test_result"}},
		okID:    {protocols: map[string]string{"snmp_v2c": "test_result"}},
	}
	tm := map[uuid.UUID]*deviceTestStatus{
		failID:  {tested: true, authFailed: true, lastTestedAt: now.Add(-time.Hour), successKinds: map[string]bool{}, failedKinds: map[string]bool{"ssh": true}},
		staleID: {tested: true, lastTestedAt: now.Add(-60 * 24 * time.Hour), successKinds: map[string]bool{"snmp_v2c": true}, failedKinds: map[string]bool{}},
		okID:    {tested: true, lastTestedAt: now.Add(-time.Hour), successKinds: map[string]bool{"snmp_v2c": true}, failedKinds: map[string]bool{}},
	}
	f := func(issue string) []db.Device { return filterDevicesByAccess(rows, am, tm, now, "", "", issue) }

	if got := f("credential_failed"); len(got) != 1 || got[0].ID != failID {
		t.Errorf("credential_failed → %v, want [%s]", ids(got), failID)
	}
	if got := f("stale"); len(got) != 1 || got[0].ID != staleID {
		t.Errorf("stale → %v, want [%s]", ids(got), staleID)
	}
	// missing_expected_protocol: a switch with no snmp/ssh access → flagged. okID
	// and staleID have snmp_v2c; failID (no access map entry) is missing both.
	if got := f("missing_expected_protocol"); len(got) != 1 || got[0].ID != failID {
		t.Errorf("missing_expected_protocol → %v, want [%s]", ids(got), failID)
	}
}

func ids(ds []db.Device) []uuid.UUID {
	out := make([]uuid.UUID, len(ds))
	for i, d := range ds {
		out[i] = d.ID
	}
	return out
}

func TestProtocolLabel(t *testing.T) {
	cases := map[string]string{"snmp_v2c": "SNMP v2c", "winrm": "WinRM", "onvif": "ONVIF", "http_basic": "HTTP Basic", "weird": "weird"}
	for in, want := range cases {
		if got := protocolLabel(in); got != want {
			t.Errorf("protocolLabel(%q) = %q, want %q", in, got, want)
		}
	}
}
