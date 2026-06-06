package osinv

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/masterzen/winrm"
)

// WinRM diagnostic test-mode. This probes a single target across the WinRM auth
// transports the library supports — NTLM+message-encryption (what HIMS uses),
// plain NTLM (no encryption), and Basic — and captures the raw HTTP auth flow
// (first status, WWW-Authenticate headers, final status, fault kind). It is built
// to explain cases where native PowerShell Invoke-Command succeeds but the Go
// WinRM library returns 401 (e.g. Windows 7 / WSMan Stack 2.0 NTLM-sealing
// incompatibilities). It NEVER logs or returns the password.

// WinRMDiagModeResult is the outcome of one transport mode against the target.
type WinRMDiagModeResult struct {
	Mode         string `json:"mode"`        // ntlm-encrypted | ntlm-plain | basic
	AuthMethod   string `json:"auth_method"` // NTLM | Basic
	Encryption   bool   `json:"encryption"`  // WSMan message encryption on?
	Result       string `json:"result"`      // success | auth_failed | connection_failed | timeout | error
	FaultKind    string `json:"fault_kind"`  // http_401 | soap_fault | http_error | transport | none
	ErrorType    string `json:"error_type,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
	FaultCode    string `json:"fault_code,omitempty"`   // WSMan fault Code (when a SOAP fault came back)
	FaultDetail  string `json:"fault_detail,omitempty"` // sanitized WSMan fault message
	LatencyMS    int64  `json:"latency_ms"`
}

// WinRMDiag is the full diagnostic report (no secrets).
type WinRMDiag struct {
	Host             string                `json:"host"`
	Endpoint         string                `json:"endpoint"`
	UsernameSent     string                `json:"username_sent"`       // exactly as supplied, e.g. coralsearesorts\dpm
	ParsedDomain     string                `json:"parsed_domain"`       // go-ntlmssp domain split
	ParsedUser       string                `json:"parsed_user"`         // go-ntlmssp user split
	DomainSentInNTLM bool                  `json:"domain_sent_in_ntlm"` // false for UPN (user@domain)
	UnauthStatus     int                   `json:"unauth_status"`       // first (no-auth) HTTP status — expect 401
	WWWAuthenticate  []string              `json:"www_authenticate"`    // schemes the listener offers
	ProbeError       string                `json:"probe_error,omitempty"`
	Modes            []WinRMDiagModeResult `json:"modes"`
}

// parseNTLMUser replicates github.com/Azure/go-ntlmssp's GetDomain so we can log
// exactly how the library splits the username. `domain\user` → (user, domain,
// true); `user@domain` (UPN) → (user, "", false) — NTLM sends NO domain for a UPN,
// which is a common reason a UPN fails over NTLM where DOMAIN\user works.
func parseNTLMUser(u string) (user, domain string, domainSent bool) {
	if i := strings.Index(u, `\`); i >= 0 {
		return u[i+1:], u[:i], true
	}
	if strings.Contains(u, "@") {
		return u, "", false
	}
	return u, "", false
}

// newWinRMClientMode builds a client for a specific transport mode.
func newWinRMClientMode(host, user, pass string, timeout time.Duration, mode string) (*winrm.Client, error) {
	ep := winrm.NewEndpoint(host, winrmPort, winrmUseSSL, false, nil, nil, nil, timeout)
	params := winrm.NewParameters("PT180S", "en-US", 153600)
	switch mode {
	case "ntlm-encrypted":
		params.TransportDecorator = func() winrm.Transporter {
			enc, _ := winrm.NewEncryption("ntlm")
			return enc
		}
	case "ntlm-plain":
		params.TransportDecorator = func() winrm.Transporter { return &winrm.ClientNTLM{} }
	case "basic":
		params.TransportDecorator = nil // default clientRequest = HTTP Basic
	default:
		return nil, fmt.Errorf("unknown winrm mode %q", mode)
	}
	return winrm.NewClientWithParameters(ep, user, pass, params)
}

// classifyDiagErr maps a mode error to (result, faultKind, errType).
func classifyDiagErr(err error) (result, faultKind, errType string) {
	if err == nil {
		return "success", "none", ""
	}
	e := strings.ToLower(err.Error())
	switch {
	case strings.Contains(e, "401"):
		return "auth_failed", "http_401", "unauthorized_401"
	case strings.Contains(e, "403"):
		return "auth_failed", "http_error", "forbidden_403"
	case strings.Contains(e, "fault") || strings.Contains(e, "s:fault"):
		return "error", "soap_fault", "wsman_fault"
	case strings.Contains(e, "refused") || strings.Contains(e, "reset"):
		return "connection_failed", "transport", "connection_refused"
	case strings.Contains(e, "timeout") || strings.Contains(e, "deadline"):
		return "timeout", "transport", "timeout"
	case strings.Contains(e, "no route") || strings.Contains(e, "no such host"):
		return "connection_failed", "transport", "unreachable"
	default:
		return "error", "http_error", fmt.Sprintf("%T", err)
	}
}

var (
	reFaultCode = regexp.MustCompile(`(?i)<[^>]*Subcode>\s*<[^>]*Value>([^<]+)</`)
	reFaultMsg  = regexp.MustCompile(`(?i)<[^>]*(?:Reason|Message|Text)[^>]*>([^<]{3,400})</`)
)

// parseWSManFault pulls the fault Code (subcode value, e.g. "w:AccessDenied") and
// a human reason out of a WSMan SOAP fault body, best-effort.
func parseWSManFault(body string) (code, detail string) {
	if m := reFaultCode.FindStringSubmatch(body); len(m) > 1 {
		code = strings.TrimSpace(m[1])
	}
	if m := reFaultMsg.FindStringSubmatch(body); len(m) > 1 {
		detail = strings.TrimSpace(m[1])
	}
	if detail == "" {
		detail = "WSMan fault (see fault_code)"
	}
	return code, detail
}

// probeWinRMAuthSchemes does ONE unauthenticated POST to the WSMan endpoint and
// reports the HTTP status + every WWW-Authenticate scheme the listener offers
// (Negotiate, NTLM, Kerberos, Basic, CredSSP). This reveals what the server is
// actually willing to do — the key comparison against what the Go library sends.
func probeWinRMAuthSchemes(ctx context.Context, host string, timeout time.Duration) (status int, schemes []string, err error) {
	url := WinRMEndpoint(host)
	cl := &http.Client{
		Timeout:   timeout,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS10}},
	}
	// A tiny SOAP-ish body; we only care about the 401 challenge headers.
	req, rerr := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(""))
	if rerr != nil {
		return 0, nil, rerr
	}
	req.Header.Set("Content-Type", "application/soap+xml;charset=UTF-8")
	resp, derr := cl.Do(req)
	if derr != nil {
		return 0, nil, derr
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()
	return resp.StatusCode, resp.Header.Values("Www-Authenticate"), nil
}

// WinRMDiagnose runs the full diagnostic matrix against host with the supplied
// credential and returns a structured report. Every mode attempt is also logged
// via the standard safe WinRM log line. The password is used only to authenticate
// and to redact itself from errors — never logged or returned.
func WinRMDiagnose(ctx context.Context, host, user, pass string, timeout time.Duration) WinRMDiag {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	pu, pd, domainSent := parseNTLMUser(user)
	rep := WinRMDiag{
		Host: host, Endpoint: WinRMEndpoint(host),
		UsernameSent: user, ParsedUser: pu, ParsedDomain: pd, DomainSentInNTLM: domainSent,
	}

	// Step 1: unauthenticated challenge — what does the listener offer?
	pctx, pcancel := context.WithTimeout(ctx, timeout)
	status, schemes, perr := probeWinRMAuthSchemes(pctx, host, timeout)
	pcancel()
	rep.UnauthStatus = status
	rep.WWWAuthenticate = schemes
	if perr != nil {
		rep.ProbeError = redactSecret(perr.Error(), pass)
	}
	slog.Info("WinRM diagnose: auth challenge",
		"event", "winrm_diag_challenge", "target_ip", host, "endpoint", rep.Endpoint,
		"unauth_status", status, "www_authenticate", strings.Join(schemes, " | "),
		"parsed_domain", pd, "parsed_user", pu, "domain_sent_in_ntlm", domainSent,
		"probe_error", rep.ProbeError)

	// Step 2: try each transport mode.
	modes := []struct {
		name       string
		authMethod string
		enc        bool
	}{
		{"ntlm-encrypted", "NTLM", true},
		{"ntlm-plain", "NTLM", false},
		{"basic", "Basic", false},
	}
	for _, m := range modes {
		start := time.Now()
		var runErr error
		cl, cerr := newWinRMClientMode(host, user, pass, timeout, m.name)
		if cerr != nil {
			runErr = cerr
		} else {
			mctx, mcancel := context.WithTimeout(ctx, timeout)
			_, runErr = WinRMRunner{C: cl}.Run(mctx, "$null")
			mcancel()
		}
		result, faultKind, errType := classifyDiagErr(runErr)
		mr := WinRMDiagModeResult{
			Mode: m.name, AuthMethod: m.authMethod, Encryption: m.enc,
			Result: result, FaultKind: faultKind, ErrorType: errType, LatencyMS: time.Since(start).Milliseconds(),
		}
		if runErr != nil {
			mr.ErrorMessage = redactSecret(runErr.Error(), pass)
			// A *winrm.ExecuteCommandError means auth PASSED (HTTP 200) and a WSMan
			// SOAP fault came back — extract the fault Code + Reason, the precise
			// operation-layer cause.
			var ce *winrm.ExecuteCommandError
			if errors.As(runErr, &ce) && ce.Body != "" {
				code, detail := parseWSManFault(ce.Body)
				mr.FaultCode = code
				mr.FaultDetail = redactSecret(detail, pass)
				mr.FaultKind = "soap_fault"
				if result == "error" {
					mr.Result = "auth_ok_operation_fault"
				}
			}
		}
		rep.Modes = append(rep.Modes, mr)
		slog.Info("WinRM diagnose: mode attempt",
			"event", "winrm_diag_mode", "target_ip", host, "endpoint", rep.Endpoint,
			"mode", m.name, "auth_method", m.authMethod, "encryption", m.enc,
			"username", user, "parsed_domain", pd, "parsed_user", pu,
			"result", result, "fault_kind", faultKind, "error_type", errType,
			"error_message", mr.ErrorMessage, "latency_ms", mr.LatencyMS)
	}
	return rep
}
