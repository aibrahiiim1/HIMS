package osinv

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/masterzen/winrm"
)

// NewWinRMClient builds a WinRM client that authenticates the way modern,
// default-hardened Windows requires: NTLM over Negotiate WITH WSMan message
// encryption (HTTP-SPNEGO-session-encrypted). Windows listeners advertise
// Negotiate/Kerberos only and default to AllowUnencrypted=false, so plain
// Basic/NTLM fails — this transport is what actually works. Shared by the
// deep-inventory collector AND credential testing so "Test" matches reality.
func NewWinRMClient(host, user, pass string, timeout time.Duration) (*winrm.Client, error) {
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	ep := winrm.NewEndpoint(host, 5985, false, false, nil, nil, nil, timeout)
	params := winrm.NewParameters("PT180S", "en-US", 153600)
	params.TransportDecorator = func() winrm.Transporter {
		enc, _ := winrm.NewEncryption("ntlm") // only errors for an unsupported protocol
		return enc
	}
	return winrm.NewClientWithParameters(ep, user, pass, params)
}

// WinRMRunner adapts a *winrm.Client to the Runner interface. PowerShell is run
// per small section (CollectWindows splits the work) so each fits the Windows
// command-line length limit of -EncodedCommand.
type WinRMRunner struct{ C *winrm.Client }

func (r WinRMRunner) Run(ctx context.Context, script string) (string, error) {
	stdout, stderr, code, err := r.C.RunPSWithContext(ctx, script)
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("winrm exit %d: %s", code, strings.TrimSpace(stderr))
	}
	return stdout, nil
}

// WinRMCheckAuth verifies a credential authenticates (and the encrypted channel
// works) with one tiny command — used to pick a working credential before a
// full collection. Returns nil on success.
func WinRMCheckAuth(ctx context.Context, host, user, pass string, timeout time.Duration) error {
	cl, err := NewWinRMClient(host, user, pass, timeout)
	if err != nil {
		return err
	}
	_, err = WinRMRunner{C: cl}.Run(ctx, "$null")
	return err
}
