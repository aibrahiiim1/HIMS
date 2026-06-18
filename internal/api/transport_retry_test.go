package api

import (
	"testing"

	"github.com/coralsearesorts/hims/internal/osinv"
	"github.com/google/uuid"
)

// Transient WinRM transport hardening: a WinRM connect-timeout (the host answers ping
// but WinRM/5985 did not respond in time) must be retried with backoff, must never be
// classified credential_failed, and — once retries are exhausted — must settle as
// collection_failed (operator-fixable transport), not credential_failed.

const winTimeout = osinv.WinRMConnectTimeout // "winrm_connect_timeout"

// 1) WinRM TCP dial timeout is retryable; auth/authz rejections are not.
func TestTransport_WinRMTimeoutIsRetryable(t *testing.T) {
	if !agentJobRetryable(winTimeout) {
		t.Fatal("winrm_connect_timeout must be retryable")
	}
	for _, terminal := range []string{"auth_failed", "wmi_access_denied", "access_denied"} {
		if agentJobRetryable(terminal) {
			t.Fatalf("%s must NOT be retryable", terminal)
		}
	}
}

// 3) A WinRM timeout is transport, never an auth failure → never drives credential_failed.
func TestTransport_WinRMTimeoutNotAuthFailure(t *testing.T) {
	if categoryIsAuthFailure(winTimeout) {
		t.Fatal("winrm_connect_timeout must not be an auth failure")
	}
}

//  4. Ping alive + WinRM timeout while a retry is still pending (an in-flight/queued
//     agent job) stays pending_collection, not a terminal failure.
func TestTransport_WinRMTimeoutPendingWhileRetrying(t *testing.T) {
	id := uuid.New()
	m := &statusMaps{
		access:      map[uuid.UUID]*deviceAccess{},
		test:        map[uuid.UUID]*deviceTestStatus{id: {tested: true, failedKinds: map[string]bool{"wmi": true}, kindCategory: map[string]string{"wmi": winTimeout}}},
		onlineSites: map[uuid.UUID]bool{}, anySites: map[uuid.UUID]bool{},
		activeCollect: map[uuid.UUID]bool{id: true}, // retry queued/dispatched
	}
	if st, _ := m.deriveManagement(winEndpoint(id)); st != MgmtPendingCollection {
		t.Fatalf("timeout with retry pending: got %s, want pending_collection", st)
	}
}

//  5. + 7-final-transport: exhausted WinRM transport timeout (no retry in flight, no
//     evidence) settles as collection_failed — operator-fixable — NOT credential_failed.
func TestTransport_WinRMTimeoutExhaustedIsCollectionFailed(t *testing.T) {
	id := uuid.New()
	m := mgmtMaps(nil, map[uuid.UUID]*deviceTestStatus{
		id: {tested: true, failedKinds: map[string]bool{"wmi": true}, kindCategory: map[string]string{"wmi": winTimeout}},
	})
	st, _ := m.deriveManagement(winEndpoint(id))
	if st != MgmtCollectionFailed {
		t.Fatalf("exhausted timeout: got %s, want collection_failed", st)
	}
	if st == MgmtCredentialFailed {
		t.Fatal("exhausted transport timeout must never be credential_failed")
	}
}

//  6. A transient-timeout history row does NOT override a later successful collection:
//     once os_inventory evidence exists, the device is managed.
func TestTransport_TimeoutHistoryDoesNotOverrideSuccess(t *testing.T) {
	id := uuid.New()
	m := mgmtMaps(winInv(id, "winrm-agent"), map[uuid.UUID]*deviceTestStatus{
		id: {tested: true, failedKinds: map[string]bool{"wmi": true}, kindCategory: map[string]string{"wmi": winTimeout}},
	})
	if st, _ := m.deriveManagement(winEndpoint(id)); st != MgmtManaged {
		t.Fatalf("timeout history + later success: got %s, want managed", st)
	}
}
