package api

import (
	"testing"

	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
	"github.com/google/uuid"
)

// Classification model (operator spec): credential_failed means ONLY "every applicable
// credential cleanly rejected AND nothing authenticated by any supported method." Any
// credential that authenticated by any method (deep success, web login, legacy auth-ok,
// or access-denied) must NOT be credential_failed. These tests drive deriveManagement
// through the masking-proof per-device cred-signal aggregate (the production path).

// cmaps builds a statusMaps with the cred-signal aggregate + optional proven access.
func cmaps(id uuid.UUID, cs credSignal, proven map[string]string) *statusMaps {
	m := &statusMaps{
		access: map[uuid.UUID]*deviceAccess{}, test: map[uuid.UUID]*deviceTestStatus{},
		cred:        map[uuid.UUID]credSignal{id: cs},
		onlineSites: map[uuid.UUID]bool{}, anySites: map[uuid.UUID]bool{}, activeCollect: map[uuid.UUID]bool{},
	}
	if len(proven) > 0 {
		da := &deviceAccess{protocols: map[string]string{}, proven: map[string]bool{}, provenSrc: map[string]string{}}
		for p, src := range proven {
			da.proven[p] = true
			da.provenSrc[p] = src
		}
		m.access[id] = da
	}
	return m
}

func TestClassification_WebAuthNotCredentialFailed(t *testing.T) {
	id := uuid.New()
	dev := db.Device{ID: id, Category: "server"}
	// (1) http_basic success + winrm auth_failed → web_authenticated, NOT credential_failed.
	st, _ := cmaps(id, credSignal{anySuccess: true, webSuccess: true, authRejected: true}, nil).deriveManagement(dev)
	if st != MgmtWebAuthenticated {
		t.Errorf("http_basic success + winrm auth_failed: got %s, want web_authenticated", st)
	}
	if st == MgmtCredentialFailed {
		t.Fatal("a host where a web credential authenticated must NEVER be credential_failed")
	}
	// (2) http_basic success only (no deep) → web_authenticated (needs deep mgmt).
	st, _ = cmaps(id, credSignal{anySuccess: true, webSuccess: true}, nil).deriveManagement(dev)
	if st != MgmtWebAuthenticated {
		t.Errorf("web success only: got %s, want web_authenticated", st)
	}
}

func TestClassification_LegacyAuthOKNotCredentialFailed(t *testing.T) {
	id := uuid.New()
	dev := db.Device{ID: id, Category: "server"}
	// (3) legacy WSMan auth_ok_operation_fault + sibling auth_failed → needs_agent, never
	// credential_failed (the credential authenticated; Go-WinRM can't drive a legacy stack).
	st, _ := cmaps(id, credSignal{legacyAuthOK: true, authRejected: true}, nil).deriveManagement(dev)
	if st != MgmtNeedsAgent {
		t.Errorf("legacy auth-ok + sibling auth_failed: got %s, want needs_agent", st)
	}
}

func TestClassification_AnyAuthOutranksFailure(t *testing.T) {
	id := uuid.New()
	dev := db.Device{ID: id, Category: "endpoint"}
	// (4) Any authenticated result outranks a failed attempt from another kind: even with
	// authRejected set, web/legacy/not-authorized all win → never credential_failed.
	for _, cs := range []credSignal{
		{anySuccess: true, webSuccess: true, authRejected: true},
		{legacyAuthOK: true, authRejected: true},
		{notAuthorized: true, authRejected: true},
	} {
		if st, _ := cmaps(id, cs, nil).deriveManagement(dev); st == MgmtCredentialFailed {
			t.Errorf("authenticated signal %+v wrongly derived credential_failed", cs)
		}
	}
}

func TestClassification_AllRejectedIsCredentialFailed(t *testing.T) {
	id := uuid.New()
	dev := db.Device{ID: id, Category: "endpoint"}
	// (5) All applicable credentials cleanly rejected, nothing authenticated → credential_failed.
	st, _ := cmaps(id, credSignal{authRejected: true}, nil).deriveManagement(dev)
	if st != MgmtCredentialFailed {
		t.Errorf("all auth-rejected, no success: got %s, want credential_failed", st)
	}
}

func TestClassification_NotAuthorizedDistinctFromWrongPassword(t *testing.T) {
	id := uuid.New()
	dev := db.Device{ID: id, Category: "endpoint"}
	// (8) Not-authorized (authenticated, host denied access) is DISTINCT from wrong password.
	if st, _ := cmaps(id, credSignal{notAuthorized: true}, nil).deriveManagement(dev); st != MgmtNotAuthorized {
		t.Errorf("access-denied only: got %s, want not_authorized", st)
	}
	if st, _ := cmaps(id, credSignal{authRejected: true}, nil).deriveManagement(dev); st != MgmtCredentialFailed {
		t.Errorf("auth-rejected only: got %s, want credential_failed", st)
	}
}

func TestClassification_DeepSuccessOutranksEverything(t *testing.T) {
	id := uuid.New()
	dev := db.Device{ID: id, OsFamily: "windows", Category: "endpoint"}
	// Durable deep evidence → managed, even alongside a sibling auth_failed/not-authorized.
	m := cmaps(id, credSignal{anySuccess: true, authRejected: true, notAuthorized: true}, map[string]string{"winrm": "evidence"})
	if st, by := m.deriveManagement(dev); st != MgmtManaged || len(by) != 1 || by[0] != "winrm" {
		t.Errorf("deep evidence present: got %s %v, want managed/[winrm]", st, by)
	}
}

func TestClassification_TransportAndTransientNotCredentialFailed(t *testing.T) {
	id := uuid.New()
	dev := db.Device{ID: id, OsFamily: "windows", Category: "endpoint"}
	// (6) Transport-only failure (no auth signal) → collection_failed, never credential_failed.
	mTransport := &statusMaps{
		access: map[uuid.UUID]*deviceAccess{}, cred: map[uuid.UUID]credSignal{},
		test:        map[uuid.UUID]*deviceTestStatus{id: {tested: true, failedKinds: map[string]bool{"wmi": true}, kindCategory: map[string]string{"wmi": "unreachable"}}},
		onlineSites: map[uuid.UUID]bool{}, anySites: map[uuid.UUID]bool{}, activeCollect: map[uuid.UUID]bool{},
	}
	if st, _ := mTransport.deriveManagement(dev); st != MgmtCollectionFailed {
		t.Errorf("transport-only: got %s, want collection_failed", st)
	}
	// (7) Transient while a collect job is in flight → pending_collection (retry/active).
	mActive := &statusMaps{
		access: map[uuid.UUID]*deviceAccess{}, cred: map[uuid.UUID]credSignal{},
		test:        map[uuid.UUID]*deviceTestStatus{id: {tested: true, kindCategory: map[string]string{"winrm": "winrm_negotiate_error"}}},
		onlineSites: map[uuid.UUID]bool{}, anySites: map[uuid.UUID]bool{}, activeCollect: map[uuid.UUID]bool{id: true},
	}
	if st, _ := mActive.deriveManagement(dev); st != MgmtPendingCollection {
		t.Errorf("transient while active: got %s, want pending_collection", st)
	}
}
