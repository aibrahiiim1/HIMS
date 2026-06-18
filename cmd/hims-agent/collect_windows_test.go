package main

import (
	"testing"

	"github.com/coralsearesorts/hims/internal/osinv"
)

// TestWinRMShortCircuitsWMI pins the reliability rule that fixed the .106/.119/.161/.194
// acceptance failures: ONLY a clean WinRM auth rejection skips the WMI/DCOM fallback.
// Every transport/negotiation failure (connect-timeout, the persistent 401 "invalid
// content type", refused/closed) MUST fall through to WMI — a different transport
// (RPC/135) that is often the only path to the host. Regression guard: a transient must
// never again short-circuit WMI (the old bug that left those hosts permanently stuck).
func TestWinRMShortCircuitsWMI(t *testing.T) {
	if !winRMShortCircuitsWMI("auth_failed") {
		t.Error("a clean auth rejection MUST short-circuit WMI (same cred → WMI auth-fails + lockout)")
	}
	for _, cat := range []string{
		osinv.WinRMConnectTimeout, osinv.WinRMNegotiateError, "unreachable", "error", "",
	} {
		if winRMShortCircuitsWMI(cat) {
			t.Errorf("%q must NOT short-circuit WMI — it must fall through to the DCOM rung", cat)
		}
	}
}

// TestClassifyNativeWinRMErr pins the native PowerShell-Remoting (New-PSSession) error
// mapping. The critical case: a native "Access is denied" is an AUTHORIZATION refusal
// (authenticated, host policy/UAC denied the session) — its own non-auth category, NEVER
// auth_failed (no false credential_failed). A clean logon failure IS auth_failed.
func TestClassifyNativeWinRMErr(t *testing.T) {
	cases := map[string]string{
		"New-PSSession : Access is denied.":                                                    "access_denied",
		"Connecting to remote server failed: Logon failure: unknown user name or bad password": "auth_failed",
		"The user name or password is incorrect":                                               "auth_failed",
		"WinRM cannot complete the operation ... timed out":                                    osinv.WinRMConnectTimeout,
		"The client cannot connect ... connection was refused":                                 "unreachable",
		"some unrecognized WSMan negotiation glitch":                                           osinv.WinRMNegotiateError,
	}
	for stderr, want := range cases {
		if got := classifyNativeWinRMErr(stderr); got != want {
			t.Errorf("classifyNativeWinRMErr(%q) = %q, want %q", stderr, got, want)
		}
	}
	// "Access is denied" = NOT authorized (canonical access_denied), never auth_failed.
	if classifyNativeWinRMErr("New-PSSession : Access is denied.") == "auth_failed" {
		t.Fatal("native Access-is-denied (authorization) must NEVER classify as auth_failed")
	}
}

// TestWindowsFinalCat pins the Check-#10 final-reason aggregator across the three rungs
// (native PSRP, Go WinRM, WMI). It must NOT collapse a mixed outcome into a misleading
// token: a definitive NATIVE reached-host verdict (access_denied / auth_failed) wins over
// a transient; but when the native path was only transient (storm), a retryable transient
// stays the headline so the host retries — even if WMI returned a UAC access-denied.
func TestWindowsFinalCat(t *testing.T) {
	cases := []struct {
		name                        string
		nativeCat, winrmCat, wmiCat string
		want                        string
	}{
		// Native reached the host: its verdict is authoritative, never hidden by a transient.
		{"native access-denied wins over go-winrm negotiate", "access_denied", "winrm_negotiate_error", "wmi_access_denied", "access_denied"},
		{"native auth-failed (wrong cred) wins", "auth_failed", "winrm_negotiate_error", "", "auth_failed"},
		// Native only transient (storm) → retryable transient stays headline even if WMI got
		// a UAC access-denied (the .49/.50 storm guard — native/WinRM-shell would collect later).
		{"native timeout + wmi access-denied stays retryable", "winrm_connect_timeout", "winrm_negotiate_error", "wmi_access_denied", "winrm_connect_timeout"},
		{"native negotiate + wmi access-denied stays retryable", "winrm_negotiate_error", "winrm_negotiate_error", "wmi_access_denied", "winrm_negotiate_error"},
		// Native unreachable/empty, no transient → WMI reached verdict is the informative one.
		{"wmi access-denied when native unreachable", "unreachable", "unreachable", "wmi_access_denied", "wmi_access_denied"},
		{"all transport unreachable (any transport token ok)", "unreachable", "unreachable", "rpc_unreachable", "unreachable"},
		{"non-windows: only winrm cat present", "", "winrm_connect_timeout", "", "winrm_connect_timeout"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := windowsFinalCat(c.nativeCat, c.winrmCat, c.wmiCat); got != c.want {
				t.Fatalf("windowsFinalCat(%q,%q,%q)=%q want %q", c.nativeCat, c.winrmCat, c.wmiCat, got, c.want)
			}
		})
	}
}
