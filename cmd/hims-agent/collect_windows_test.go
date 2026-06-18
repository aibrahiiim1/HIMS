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

// TestPickWindowsFailCat documents the headline-category contract for the two-rung
// Windows ladder when BOTH rungs fail. The WinRM rung runs first and only reaches the
// WMI fallback (and hence this merge) when it was UNREACHABLE — a definitive auth_failed
// short-circuits earlier — so the WMI rung's category is the more informative signal and
// must win whenever it is present. This keeps the server's credential_failed (host
// reached, creds rejected) vs collection_failed (transport miss) classification honest.
func TestPickWindowsFailCat(t *testing.T) {
	cases := []struct {
		name             string
		winrmCat, wmiCat string
		want             string
	}{
		{"wmi access-denied outranks winrm unreachable", "unreachable", "wmi_access_denied", "wmi_access_denied"},
		{"wmi transport when both transport", "unreachable", "rpc_unreachable", "rpc_unreachable"},
		{"falls back to winrm when wmi empty", "error", "", "error"},
		{"wmi error reported", "unreachable", "wmi_error", "wmi_error"},
		// A WinRM connect-timeout is retryable transport and must stay the headline even
		// when WMI returned a (UAC-blocked) access-denied — never masked into a terminal
		// credential_failed. This is the .49/.50 storm case.
		{"winrm timeout outranks wmi access-denied", "winrm_connect_timeout", "wmi_access_denied", "winrm_connect_timeout"},
		{"winrm timeout outranks wmi error", "winrm_connect_timeout", "wmi_error", "winrm_connect_timeout"},
		// A WinRM/NTLM negotiation 401 (overloaded listener) is also retryable transport
		// and must not be masked by the WMI verdict.
		{"winrm negotiate-error outranks wmi access-denied", "winrm_negotiate_error", "wmi_access_denied", "winrm_negotiate_error"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := pickWindowsFailCat(c.winrmCat, c.wmiCat); got != c.want {
				t.Fatalf("pickWindowsFailCat(%q,%q)=%q want %q", c.winrmCat, c.wmiCat, got, c.want)
			}
		})
	}
}
