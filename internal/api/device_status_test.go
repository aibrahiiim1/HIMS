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
