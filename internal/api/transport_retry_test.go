package api

import (
	"testing"
	"time"

	"github.com/coralsearesorts/hims/internal/osinv"
	"github.com/google/uuid"
)

// Transient WinRM transport hardening: a WinRM connect-timeout (the host answers ping
// but WinRM/5985 did not respond in time) must be retried with backoff, must never be
// classified credential_failed, and — once retries are exhausted — must settle as
// collection_failed (operator-fixable transport), not credential_failed.

const (
	winTimeout   = osinv.WinRMConnectTimeout // "winrm_connect_timeout"
	winNegotiate = osinv.WinRMNegotiateError // "winrm_negotiate_error"
)

//  1. WinRM TCP dial timeout AND a WinRM/NTLM negotiation 401 are retryable transport;
//     auth/authz rejections are not.
func TestTransport_WinRMTimeoutIsRetryable(t *testing.T) {
	for _, transient := range []string{winTimeout, winNegotiate} {
		if !agentJobRetryable(transient) {
			t.Fatalf("%s must be retryable", transient)
		}
	}
	for _, terminal := range []string{"auth_failed", "wmi_access_denied", "access_denied"} {
		if agentJobRetryable(terminal) {
			t.Fatalf("%s must NOT be retryable", terminal)
		}
	}
}

//  3. A WinRM timeout / negotiation error is transport, never an auth failure → never
//     drives credential_failed.
func TestTransport_WinRMTimeoutNotAuthFailure(t *testing.T) {
	for _, transient := range []string{winTimeout, winNegotiate} {
		if categoryIsAuthFailure(transient) {
			t.Fatalf("%s must not be an auth failure", transient)
		}
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

//  8. The retry envelope must OUTLAST a from-zero collection storm. A load-induced
//     transient (winrm_negotiate_error / winrm_connect_timeout) is caused by the storm
//     itself; if every retry of the default 5-attempt budget fires while the storm is
//     still at peak (a ~12-min drain), the host goes terminal collection_failed with no
//     post-storm attempt — the 172.21.60.106/.119 gate failure. Assert the cumulative
//     backoff across the 4 retries of a 5-attempt job pushes the final attempt well
//     past a typical drain, AND that each step is monotonically non-decreasing.
func TestTransport_RetryEnvelopeOutlastsStorm(t *testing.T) {
	const maxAttempts = 5 // migration 000081 default
	var cumulative time.Duration
	var prev time.Duration
	// Retries happen after attempts 0..maxAttempts-2 (the final attempt is terminal).
	for attempt := 0; attempt < maxAttempts-1; attempt++ {
		b := agentRetryBackoff(attempt)
		if b < prev {
			t.Fatalf("backoff must be monotonically non-decreasing: attempt %d = %s < prev %s", attempt, b, prev)
		}
		prev = b
		cumulative += b
	}
	// A from-zero subnet drain is ~12 min; the last retry must land clearly past it.
	if cumulative < 15*time.Minute {
		t.Fatalf("retry envelope too short to outlast a from-zero storm: cumulative %s, want >= 15m", cumulative)
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
