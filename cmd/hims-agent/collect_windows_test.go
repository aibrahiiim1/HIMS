package main

import "testing"

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
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := pickWindowsFailCat(c.winrmCat, c.wmiCat); got != c.want {
				t.Fatalf("pickWindowsFailCat(%q,%q)=%q want %q", c.winrmCat, c.wmiCat, got, c.want)
			}
		})
	}
}
