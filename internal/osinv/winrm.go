package osinv

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/masterzen/winrm"
)

// WinRMOperationFault is the credential-test category for "NTLM authentication
// SUCCEEDED but the WSMan operation faulted" — the signature of a legacy WSMan
// 2.0 stack (Windows 7 / Server 2008 R2) that the Go WinRM library can't drive
// even though the credential is valid. It must NOT be reported as a wrong password.
const WinRMOperationFault = "auth_ok_operation_fault"

// ClassifyWinRMError maps a WinRM error to a credential-test category + detail +
// (optional) WSMan fault code. The key distinction: a *winrm.ExecuteCommandError
// means HTTP/NTLM auth already SUCCEEDED (HTTP 200) and the failure is a WSMan
// SOAP fault at the operation layer → auth_ok_operation_fault (legacy WSMan),
// never auth_failed. Pure; no secrets.
func ClassifyWinRMError(err error) (category, detail, faultCode string) {
	if err == nil {
		return "success", "WinRM login ok", ""
	}
	var ce *winrm.ExecuteCommandError
	if errors.As(err, &ce) {
		code, reason := parseWSManFault(ce.Body)
		shown := code
		if shown == "" {
			shown = "WSMan fault"
		}
		return WinRMOperationFault,
			"authentication succeeded but the WSMan operation faulted (" + shown + ") — legacy WSMan 2.0 stack (Windows 7 / Server 2008 R2); native PowerShell works but the Go WinRM library cannot drive this host. Use the Windows Native Collector / WMI fallback. " + reason,
			code
	}
	e := strings.ToLower(err.Error())
	switch {
	case strings.Contains(e, "401") || strings.Contains(e, "unauthorized") ||
		strings.Contains(e, "the user name or password is incorrect") || strings.Contains(e, "access is denied"):
		return "auth_failed", "authentication rejected", ""
	case strings.Contains(e, "refused") || strings.Contains(e, "reset") ||
		strings.Contains(e, "timeout") || strings.Contains(e, "deadline") ||
		strings.Contains(e, "no route") || strings.Contains(e, "no such host") || strings.Contains(e, "unreachable"):
		return "unreachable", "could not connect (WinRM/5985 unreachable or filtered)", ""
	default:
		return "error", strings.TrimSpace(err.Error()), ""
	}
}

// WinRM client configuration constants — also surfaced in logs so an operator can
// see exactly how HIMS talks WinRM (and confirm it is NOT plain Basic auth).
const (
	winrmPort          = 5985
	winrmScheme        = "http"
	winrmTransport     = "winrm"
	winrmAuthMethod    = "NTLM (SPNEGO) with WSMan message encryption"
	winrmClientLibrary = "github.com/masterzen/winrm + github.com/Azure/go-ntlmssp"
	winrmClientMode    = "raw WSMan SOAP over HTTP (third-party Go library; NOT PowerShell remoting)"
	winrmUseSSL        = false
	winrmAllowUnenc    = false // we use NTLM message-level encryption, not AllowUnencrypted
)

// WinRMEndpoint is the WSMan URL HIMS connects to for a host (for logs/UI).
func WinRMEndpoint(host string) string {
	return fmt.Sprintf("%s://%s:%d/wsman", winrmScheme, host, winrmPort)
}

// WinRMClientInfo returns the fixed WinRM client configuration as slog key/values
// for a one-time startup/debug log. It makes the default auth mode explicit
// (NTLM + message encryption, basic_auth=false) so there is no ambiguity about
// whether the implementation falls back to Basic over HTTP. No secrets.
func WinRMClientInfo() []any {
	return []any{
		"client_library", winrmClientLibrary,
		"client_mode", winrmClientMode,
		"auth_method", winrmAuthMethod,
		"basic_auth", false,
		"default_port", winrmPort,
		"default_scheme", winrmScheme,
		"ssl", winrmUseSSL,
		"allow_unencrypted", winrmAllowUnenc,
		"message_encryption", true,
	}
}

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
	ep := winrm.NewEndpoint(host, winrmPort, winrmUseSSL, false, nil, nil, nil, timeout)
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
// full collection. Returns nil on success. credName is an optional display label
// for the log (never a secret); pass "" when unknown.
func WinRMCheckAuth(ctx context.Context, host, user, pass string, timeout time.Duration, credName string) error {
	return winrmAuthAttempt(ctx, host, user, pass, timeout, credName, func(cl *winrm.Client) error {
		_, err := WinRMRunner{C: cl}.Run(ctx, "$null")
		return err
	})
}

// winrmAuthAttempt runs a WinRM operation behind a single, safe, structured log
// line describing exactly what HIMS sent and how — for every authentication
// attempt in the discovery/probe flow. It NEVER logs the password (or any secret)
// and defensively redacts the password from the error message. The `op` closure
// performs the actual request so the same logging wraps both the credential check
// and the deep collection.
func winrmAuthAttempt(ctx context.Context, host, user, pass string, timeout time.Duration, credName string, op func(*winrm.Client) error) error {
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	start := time.Now()
	cl, err := NewWinRMClient(host, user, pass, timeout)
	if err == nil {
		err = op(cl)
	}
	LogWinRMAttempt(host, user, credName, timeout, time.Since(start), pass, err)
	return err
}

// LogWinRMAttempt emits one safe, structured log line describing exactly what
// HIMS sent for a single WinRM authentication attempt and how it ended. It NEVER
// logs the password (pass is used only to defensively redact it from the error).
// Shared by the credential check and the deep-collection path so every WinRM auth
// attempt in the discovery/probe flow is visible.
func LogWinRMAttempt(host, user, credName string, timeout, latency time.Duration, pass string, err error) {
	result, errType := classifyWinRM(err)
	attrs := []any{
		"event", "winrm_auth_attempt",
		"target_ip", host,
		"endpoint", WinRMEndpoint(host),
		"port", winrmPort,
		"scheme", winrmScheme,
		"transport", winrmTransport,
		"username", user, // e.g. coralsearesorts\dpm — not a secret; logged exactly as sent
		"credential", credName,
		"auth_method", winrmAuthMethod,
		"ssl", winrmUseSSL,
		"allow_unencrypted", winrmAllowUnenc,
		"cert_validation", "n/a (http)",
		"timeout_ms", timeout.Milliseconds(),
		"client_library", winrmClientLibrary,
		"client_mode", winrmClientMode,
		"result", result,
		"latency_ms", latency.Milliseconds(),
	}
	if err != nil {
		attrs = append(attrs, "error_type", errType, "error_message", redactSecret(err.Error(), pass))
	}
	slog.Info("WinRM auth attempt", attrs...)
}

// classifyWinRM maps a WinRM error to a coarse result + sanitized error class for
// the log. The error string itself never contains the password.
func classifyWinRM(err error) (result, errType string) {
	if err == nil {
		return "success", ""
	}
	e := strings.ToLower(err.Error())
	switch {
	case strings.Contains(e, "401") || strings.Contains(e, "unauthorized") ||
		strings.Contains(e, "authenticate") || strings.Contains(e, "access is denied") ||
		strings.Contains(e, "the user name or password is incorrect"):
		return "auth_failed", "unauthorized_401"
	case strings.Contains(e, "403") || strings.Contains(e, "forbidden"):
		return "auth_failed", "forbidden_403"
	case strings.Contains(e, "refused") || strings.Contains(e, "reset"):
		return "connection_failed", "connection_refused"
	case strings.Contains(e, "timeout") || strings.Contains(e, "deadline") || strings.Contains(e, "i/o timeout"):
		return "timeout", "timeout"
	case strings.Contains(e, "no route") || strings.Contains(e, "no such host") || strings.Contains(e, "unreachable"):
		return "connection_failed", "unreachable"
	case strings.Contains(e, "unsupported") && strings.Contains(e, "auth"):
		return "unsupported_auth", "unsupported_auth"
	default:
		return "error", fmt.Sprintf("%T", err)
	}
}

// redactSecret defensively removes the password from a string before logging.
// The WinRM library does not echo the password, but this guarantees it even if a
// future version changes.
func redactSecret(msg, pass string) string {
	if pass != "" {
		msg = strings.ReplaceAll(msg, pass, "***")
	}
	return strings.TrimSpace(msg)
}
