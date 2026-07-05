package api

import (
	"testing"

	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
	"github.com/google/uuid"
)

func TestReachabilityFromStatus(t *testing.T) {
	cases := map[string]string{"up": ReachOnline, "down": ReachOffline, "warning": ReachWarning, "needs_attention": ReachWarning, "unknown": ReachUnknown, "": ReachUnknown}
	for in, want := range cases {
		if got := reachabilityFromStatus(in); got != want {
			t.Errorf("reachabilityFromStatus(%q)=%q want %q", in, got, want)
		}
	}
}

func TestDeriveManagement(t *testing.T) {
	id := uuid.New()
	loc := uuid.New()
	cred := uuid.New()

	mk := func(access *deviceAccess, ts *deviceTestStatus, online, any bool) *statusMaps {
		m := &statusMaps{access: map[uuid.UUID]*deviceAccess{}, test: map[uuid.UUID]*deviceTestStatus{}, onlineSites: map[uuid.UUID]bool{}, anySites: map[uuid.UUID]bool{}}
		if access != nil {
			m.access[id] = access
		}
		if ts != nil {
			m.test[id] = ts
		}
		if online {
			m.onlineSites[loc] = true
		}
		if any {
			m.anySites[loc] = true
		}
		return m
	}
	provenWinRM := &deviceAccess{protocols: map[string]string{"winrm": "evidence"}, proven: map[string]bool{"winrm": true}}
	boundOnly := &deviceAccess{protocols: map[string]string{"winrm": "bound_credential"}, proven: map[string]bool{}}
	legacyTS := &deviceTestStatus{kindCategory: map[string]string{"winrm": "auth_ok_operation_fault"}}
	authFailTS := &deviceTestStatus{authFailed: true, kindCategory: map[string]string{"winrm": "auth_failed"}}

	// Managed: a PROVEN working method exists (never open ports).
	m := mk(provenWinRM, nil, false, false)
	if st, by := m.deriveManagement(db.Device{ID: id, Category: "endpoint"}); st != MgmtManaged || len(by) != 1 || by[0] != "winrm" {
		t.Errorf("expected managed/winrm, got %s %v", st, by)
	}
	// Bound credential but never proven to work → NOT managed (collection failed).
	m = mk(boundOnly, nil, false, false)
	if st, _ := m.deriveManagement(db.Device{ID: id, Category: "server", CredentialID: &cred}); st != MgmtCollectionFailed {
		t.Errorf("bound-but-unproven: expected collection_failed, got %s", st)
	}
	// Online but unmanaged: a credentialed category with nothing working.
	m = mk(nil, nil, false, false)
	if st, _ := m.deriveManagement(db.Device{ID: id, Category: "switch"}); st != MgmtNeedsCredential {
		t.Errorf("expected needs_credential, got %s", st)
	}
	// Credential failed.
	m = mk(nil, authFailTS, false, false)
	if st, _ := m.deriveManagement(db.Device{ID: id, Category: "server"}); st != MgmtCredentialFailed {
		t.Errorf("expected credential_failed, got %s", st)
	}
	// Bound credential but no working method → collection failed.
	m = mk(nil, &deviceTestStatus{}, false, false)
	if st, _ := m.deriveManagement(db.Device{ID: id, Category: "server", CredentialID: &cred}); st != MgmtCollectionFailed {
		t.Errorf("expected collection_failed, got %s", st)
	}
	// Legacy Windows, no agent → needs agent.
	m = mk(nil, legacyTS, false, false)
	if st, _ := m.deriveManagement(db.Device{ID: id, Category: "endpoint", LocationID: &loc}); st != MgmtNeedsAgent {
		t.Errorf("expected needs_agent, got %s", st)
	}
	// Legacy Windows, site has an agent but it's offline → agent offline.
	m = mk(nil, legacyTS, false, true)
	if st, _ := m.deriveManagement(db.Device{ID: id, Category: "endpoint", LocationID: &loc}); st != MgmtAgentOffline {
		t.Errorf("expected agent_offline, got %s", st)
	}
	// Non-credentialed, no signal → unmanaged.
	m = mk(nil, nil, false, false)
	if st, _ := m.deriveManagement(db.Device{ID: id, Category: "printer"}); st != MgmtUnmanaged && st != MgmtNeedsCredential {
		t.Errorf("expected unmanaged, got %s", st)
	}
}

// isVirtualByHardware must recognize a VM from its own SMBIOS vendor/model (so an unlinked
// guest is never mislabeled "Physical"), while leaving real physical servers alone.
func TestIsVirtualByHardware(t *testing.T) {
	virtual := []struct{ vendor, model string }{
		{"Microsoft Corporation", "Virtual Machine"}, // Hyper-V / Azure (the 150.0.0.111 bug)
		{"VMware, Inc.", "VMware Virtual Platform"},
		{"VMware, Inc.", "VMware7,1"},
		{"innotek GmbH", "VirtualBox"},
		{"QEMU", "Standard PC (Q35 + ICH9, 2009)"},
		{"Red Hat", "KVM"},
		{"Xen", "HVM domU"},
		{"Nutanix", "AHV"},
	}
	for _, c := range virtual {
		if !isVirtualByHardware(strp(c.vendor), strp(c.model)) {
			t.Errorf("isVirtualByHardware(%q,%q)=false, want true (this is a VM)", c.vendor, c.model)
		}
	}
	physical := []struct{ vendor, model string }{
		{"HP", "ProLiant DL380p Gen8"},
		{"HPE", "ProLiant DL380 Gen10"},
		{"Dell Inc.", "PowerEdge R740"},
		{"", ""},
	}
	for _, c := range physical {
		if isVirtualByHardware(strp(c.vendor), strp(c.model)) {
			t.Errorf("isVirtualByHardware(%q,%q)=true, want false (this is physical)", c.vendor, c.model)
		}
	}
	// Nil-safe.
	if isVirtualByHardware(nil, nil) {
		t.Error("isVirtualByHardware(nil,nil)=true, want false")
	}
}

// TestPortSetEqual locks the multi-signal backfill trigger: a legacy check with an
// empty/partial candidate set is NOT equal to a non-empty open set (so {all:true}
// upgrades it), while an order-independent exact match IS equal (idempotent no-op).
func TestPortSetEqual(t *testing.T) {
	open := map[int32]bool{135: true, 445: true, 5060: true}
	cases := []struct {
		name string
		json string
		want bool
	}{
		{"empty blob vs open set -> backfill", ``, false},
		{"empty array vs open set -> backfill", `[]`, false},
		{"single legacy port -> backfill", `[445]`, false},
		{"partial subset -> backfill", `[135,445]`, false},
		{"exact same set, different order -> healthy", `[5060,135,445]`, true},
		{"superset -> not equal", `[135,445,5060,8080]`, false},
		{"invalid json -> backfill", `{oops`, false},
	}
	for _, c := range cases {
		if got := portSetEqual([]byte(c.json), open); got != c.want {
			t.Errorf("%s: portSetEqual(%q)=%v want %v", c.name, c.json, got, c.want)
		}
	}
	// empty open set: an empty candidate matches (no repair churn for portless hosts).
	if !portSetEqual([]byte(`[]`), map[int32]bool{}) {
		t.Errorf("empty candidate vs empty open set should be equal (no churn)")
	}
}
