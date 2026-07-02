package api

import (
	"testing"

	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
	"github.com/google/uuid"
)

// State-regression suite: a device that has a SUCCESSFUL OS inventory must stay
// `managed` and must NOT be flipped to `credential_failed` by a later failed attempt
// or by stale failed-credential history. These lock the precedence invariant —
// hasProven() (collection evidence) outranks the authFailed signal in deriveManagement
// — together with the access.sql fix that makes EVERY os_inventory row a durable
// `evidence` signal (the WinRM-shell relay-agent collector writes
// collection_method='winrm-agent', which the old enumerated IN-list silently dropped).
//
// winInv builds the access map exactly as the (fixed) ListDeviceAccessSignals query
// emits it for a host with os_inventory: collection_method is normalised to a protocol
// token and the source is 'evidence'. 'winrm-agent' → 'winrm'.
func winInv(id uuid.UUID, method string) map[uuid.UUID]*deviceAccess {
	proto := "winrm"
	switch method {
	case "wmi":
		proto = "wmi"
	case "ssh":
		proto = "ssh"
	}
	return buildAccessMap([]db.ListDeviceAccessSignalsRow{{DeviceID: id, Protocol: proto, Source: "evidence"}})
}

func mgmtMaps(access map[uuid.UUID]*deviceAccess, test map[uuid.UUID]*deviceTestStatus) *statusMaps {
	if access == nil {
		access = map[uuid.UUID]*deviceAccess{}
	}
	if test == nil {
		test = map[uuid.UUID]*deviceTestStatus{}
	}
	return &statusMaps{
		access: access, test: test,
		onlineSites: map[uuid.UUID]bool{}, anySites: map[uuid.UUID]bool{},
		nvrChannelCams: map[uuid.UUID]bool{}, activeCollect: map[uuid.UUID]bool{},
	}
}

func winEndpoint(id uuid.UUID) db.Device {
	return db.Device{ID: id, OsFamily: "windows", Category: "endpoint"}
}

// 1) Success via winrm-agent, THEN a later wmi_access_denied — stays managed.
func TestRegression_WinrmAgentSuccessThenWMIAccessDenied(t *testing.T) {
	id := uuid.New()
	m := mgmtMaps(winInv(id, "winrm-agent"), map[uuid.UUID]*deviceTestStatus{
		id: {tested: true, authFailed: true, failedKinds: map[string]bool{"wmi": true},
			kindCategory: map[string]string{"wmi": "wmi_access_denied"}},
	})
	if st, by := m.deriveManagement(winEndpoint(id)); st != MgmtManaged || len(by) != 1 || by[0] != "winrm" {
		t.Fatalf("winrm-agent success + later wmi_access_denied: got %s %v, want managed/[winrm]", st, by)
	}
}

// 2) Success via direct WinRM, then OLD failed credential history — stays managed.
func TestRegression_DirectWinRMSuccessThenOldFailedHistory(t *testing.T) {
	id := uuid.New()
	m := mgmtMaps(winInv(id, "winrm"), map[uuid.UUID]*deviceTestStatus{
		id: {tested: true, authFailed: true, failedKinds: map[string]bool{"winrm": true},
			kindCategory: map[string]string{"winrm": "auth_failed"}},
	})
	if st, _ := m.deriveManagement(winEndpoint(id)); st != MgmtManaged {
		t.Fatalf("direct-winrm success + old failed history: got %s, want managed", st)
	}
}

// 3) Successful OS-inventory evidence outranks stale failed credential_test_results.
func TestRegression_InventoryEvidenceOutranksFailedTests(t *testing.T) {
	id := uuid.New()
	m := mgmtMaps(winInv(id, "winrm-agent"), map[uuid.UUID]*deviceTestStatus{
		id: {tested: true, authFailed: true,
			failedKinds:  map[string]bool{"wmi": true, "winrm": true},
			kindCategory: map[string]string{"wmi": "wmi_access_denied", "winrm": "auth_failed"}},
	})
	if st, _ := m.deriveManagement(winEndpoint(id)); st != MgmtManaged {
		t.Fatalf("inventory evidence vs multiple failed tests: got %s, want managed", st)
	}
}

//  4. A later transient collection failure does NOT produce a final credential_failed
//     while inventory evidence stands (the failure is surfaced as history/stale, the
//     device stays managed).
func TestRegression_TransientFailureAfterSuccessStaysManaged(t *testing.T) {
	id := uuid.New()
	m := mgmtMaps(winInv(id, "winrm-agent"), map[uuid.UUID]*deviceTestStatus{
		id: {tested: true, failedKinds: map[string]bool{"wmi": true},
			kindCategory: map[string]string{"wmi": "wmi_error"}}, // transient, non-auth
	})
	if st, _ := m.deriveManagement(winEndpoint(id)); st != MgmtManaged {
		t.Fatalf("transient failure after success: got %s, want managed", st)
	}
}

// 5) credential_failed only wins when a credential was CLEANLY rejected (wrong
// username/password) and there is NO authenticated evidence at all. (access_denied is
// "authenticated but not authorized" → not_authorized, covered separately.)
func TestRegression_CredentialFailedOnlyWithoutEvidence(t *testing.T) {
	id := uuid.New()
	m := mgmtMaps(nil, map[uuid.UUID]*deviceTestStatus{
		id: {tested: true, authFailed: true, failedKinds: map[string]bool{"winrm": true},
			kindCategory: map[string]string{"winrm": "auth_failed"}},
	})
	if st, _ := m.deriveManagement(winEndpoint(id)); st != MgmtCredentialFailed {
		t.Fatalf("no evidence + clean auth rejection: got %s, want credential_failed", st)
	}
}

// Inventory-only: a device the operator marked record-and-monitor-only must report the
// distinct "inventory_only" management state — NEVER a credential/collection gap — even
// when it has a clean auth rejection that would otherwise be credential_failed. Its
// reachability still reflects the live monitoring status (it IS monitored), so an offline
// inventory-only device reads offline, which is exactly what the offline-alert bucket keys on.
func TestRegression_InventoryOnlyNeverCredentialGap(t *testing.T) {
	id := uuid.New()
	m := mgmtMaps(nil, map[uuid.UUID]*deviceTestStatus{
		id: {tested: true, authFailed: true, failedKinds: map[string]bool{"winrm": true},
			kindCategory: map[string]string{"winrm": "auth_failed"}},
	})
	// Same signals as TestRegression_CredentialFailedOnlyWithoutEvidence (→ credential_failed),
	// but the inventory-only flag must override it to inventory_only.
	d := winEndpoint(id)
	d.IsInventoryOnly = true
	d.Status = "down"
	sf := m.statusFor(d)
	if sf.Management != MgmtInventoryOnly {
		t.Fatalf("inventory-only management: got %s, want inventory_only", sf.Management)
	}
	if sf.Reachability != ReachOffline {
		t.Fatalf("inventory-only reachability: got %s, want offline (still monitored)", sf.Reachability)
	}
}

//  6. Connectivity report and Device Detail share ONE source of truth. The Connectivity
//     report lists raw failed attempts as history but takes the device's management
//     state from the same deriveManagement the Inventory/Device-Detail use — so a device
//     proven by inventory is managed in BOTH, never contradicting. This asserts the
//     single derivation yields managed given (evidence + a failed-attempt history row),
//     which is precisely the data the Connectivity report would also be showing.
func TestRegression_ConnectivityAndDetailAgree(t *testing.T) {
	id := uuid.New()
	test := map[uuid.UUID]*deviceTestStatus{
		id: {tested: true, authFailed: true, failedKinds: map[string]bool{"wmi": true},
			kindCategory: map[string]string{"wmi": "wmi_access_denied"}},
	}
	m := mgmtMaps(winInv(id, "winrm-agent"), test)
	st, _ := m.deriveManagement(winEndpoint(id))
	// statusFor is what the device list (Inventory / Device Detail / Connectivity join)
	// consumes — it must equal the raw deriveManagement state.
	if sf := m.statusFor(winEndpoint(id)); sf.Management != st || sf.Management != MgmtManaged {
		t.Fatalf("connectivity/detail disagreement: statusFor=%s derive=%s, want both managed", sf.Management, st)
	}
}

//  7. The .49/.50 case: a non-domain kiosk collected by local-admin over the relay
//     agent's WinRM shell (collection_method='winrm-agent') must NOT be overridden by a
//     later WMI/UAC "access denied" — exactly the regression the operator reported.
func TestRegression_LocalAdminWinrmAgentNotOverriddenByWMIUAC(t *testing.T) {
	id := uuid.New()
	m := mgmtMaps(winInv(id, "winrm-agent"), map[uuid.UUID]*deviceTestStatus{
		// The agent tried candidate creds; the local admin won over WinRM (the success
		// is the os_inventory evidence), but a domain cred earlier hit the host's UAC
		// remote-WMI block → wmi_access_denied is in history.
		id: {tested: true, authFailed: true, failedKinds: map[string]bool{"wmi": true},
			kindCategory: map[string]string{"wmi": "wmi_access_denied"}},
	})
	if st, by := m.deriveManagement(winEndpoint(id)); st != MgmtManaged || len(by) != 1 || by[0] != "winrm" {
		t.Fatalf("kiosk local-admin winrm-agent vs WMI/UAC: got %s %v, want managed/[winrm]", st, by)
	}
}

// buildAccessMap contract: an os_inventory 'evidence' row makes the protocol PROVEN —
// the signal the fixed query now emits for winrm-agent collections. Locks that evidence
// (not merely a bound credential) drives hasProven().
func TestRegression_InventoryEvidenceIsProven(t *testing.T) {
	id := uuid.New()
	am := winInv(id, "winrm-agent")
	da := am[id]
	if da == nil || !da.hasProven() || !da.provenHas("winrm") {
		t.Fatalf("winrm-agent inventory evidence not proven: %+v", da)
	}
}
