package osinv

import (
	"fmt"
	"testing"
)

// TestClassifyWinRMError_ConnectTimeout locks that a WinRM TCP connect/dial timeout —
// the host accepted no answer in time (it still answers ping) — is classified as the
// retryable transport category WinRMConnectTimeout, NOT a generic "error" (which the
// agent ladder would let fall through to a WMI logon attempt) and NOT "auth_failed".
// The exact Windows connectex timeout text is the one seen live on the .49/.50 kiosks.
func TestClassifyWinRMError_ConnectTimeout(t *testing.T) {
	timeouts := []error{
		fmt.Errorf(`winrm collect: unknown error Post "http://172.21.60.49:5985/wsman": dial tcp 172.21.60.49:5985: connectex: A connection attempt failed because the connected party did not properly respond after a period of time, or established connection failed because connected host has failed to respond.`),
		fmt.Errorf(`Post "http://10.0.0.5:5985/wsman": context deadline exceeded`),
		fmt.Errorf(`dial tcp 10.0.0.5:5985: i/o timeout`),
		fmt.Errorf(`read tcp: connection timed out`),
	}
	for _, err := range timeouts {
		if cat, _, _ := ClassifyWinRMError(err); cat != WinRMConnectTimeout {
			t.Errorf("ClassifyWinRMError(%q) = %q, want %q", err, cat, WinRMConnectTimeout)
		}
	}
}

// TestClassifyWinRMError_NegotiateError locks that a WinRM 401 whose body is not the
// expected SOAP/NTLM continuation ("invalid content type") — the signature of a WinRM
// listener overloaded during a scan storm — is the RETRYABLE WinRMNegotiateError, NOT
// auth_failed. Critically, the error string contains "401", so the negotiate case must
// be matched BEFORE the generic 401→auth_failed case. The exact text is the one seen
// live on .12/.119/.120/.130 during the 82-host from-zero.
func TestClassifyWinRMError_NegotiateError(t *testing.T) {
	negotiate := []error{
		fmt.Errorf(`winrm collect: http response error: 401 - invalid content type`),
		fmt.Errorf(`http response error: 401 - invalid content-type "text/html"`),
	}
	for _, err := range negotiate {
		if cat, _, _ := ClassifyWinRMError(err); cat != WinRMNegotiateError {
			t.Errorf("ClassifyWinRMError(%q) = %q, want %q (must NOT be auth_failed)", err, cat, WinRMNegotiateError)
		}
	}
	// A clean credential rejection is still auth_failed (terminal).
	if cat, _, _ := ClassifyWinRMError(fmt.Errorf(`http error 401: unauthorized`)); cat != "auth_failed" {
		t.Errorf("clean 401: got %q, want auth_failed", cat)
	}
}

// TestClassifyWinRMError_RefusedVsAuth keeps the neighbouring categories distinct: an
// actively refused/closed port is "unreachable" (WinRM genuinely not listening → the
// agent may fall through to WMI), while a rejected credential is "auth_failed".
func TestClassifyWinRMError_RefusedVsAuth(t *testing.T) {
	if cat, _, _ := ClassifyWinRMError(fmt.Errorf(`dial tcp 10.0.0.5:5985: connectex: No connection could be made because the target machine actively refused it.`)); cat != "unreachable" {
		t.Errorf("refused: got %q, want unreachable", cat)
	}
	if cat, _, _ := ClassifyWinRMError(fmt.Errorf(`http error 401: unauthorized`)); cat != "auth_failed" {
		t.Errorf("401: got %q, want auth_failed", cat)
	}
	if cat, _, _ := ClassifyWinRMError(nil); cat != "success" {
		t.Errorf("nil: got %q, want success", cat)
	}
}
