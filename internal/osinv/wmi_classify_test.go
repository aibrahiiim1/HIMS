package osinv

import (
	"fmt"
	"strings"
	"testing"
)

// TestClassifyWMIError_AccessDeniedIsAmbiguous locks the honest contract for the
// WMI/DCOM "Access is denied" (0x80070005) case: the CATEGORY stays WMIAccessDenied
// (so the agent ladder + storm guard still treat it as a reached verdict), but the
// operator-facing DETAIL must NOT assert the credential authenticated — over DCOM the
// same code is returned for a real authorization denial AND a wrong local password, and
// DCOM never surfaces a distinct logon failure. The detail must flag that ambiguity so
// the operator isn't steered to a host-policy fix when a wrong local password (the
// 172.21.210.26 case) is equally possible.
func TestClassifyWMIError_AccessDeniedIsAmbiguous(t *testing.T) {
	for _, e := range []error{
		fmt.Errorf("wmi/dcom failed [Access is denied.]"),
		fmt.Errorf("Get-WmiObject: Access is denied. (Exception from HRESULT: 0x80070005)"),
	} {
		cat, detail := ClassifyWMIError(e)
		if cat != WMIAccessDenied {
			t.Errorf("category: got %q want %q", cat, WMIAccessDenied)
		}
		d := strings.ToLower(detail)
		// Must acknowledge the wrong-password possibility, not claim authenticated.
		if !strings.Contains(d, "wrong local password") && !strings.Contains(d, "wrong password") {
			t.Errorf("detail must flag the wrong-password ambiguity, got: %q", detail)
		}
		if strings.Contains(d, "authenticated but") {
			t.Errorf("detail must NOT assert the credential authenticated over DCOM-only, got: %q", detail)
		}
	}
}

// A clean WMI logon failure (when Windows does surface one) stays wmi_auth_failed.
func TestClassifyWMIError_AuthFailedStaysDistinct(t *testing.T) {
	cat, _ := ClassifyWMIError(fmt.Errorf("logon failure: unknown user name or bad password (0x8007052e)"))
	if cat != WMIAuthFailed {
		t.Errorf("got %q want %q", cat, WMIAuthFailed)
	}
}
