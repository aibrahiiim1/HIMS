// Package credtest is HIMS's universal credential tester: given a decrypted
// secret + its kind, it attempts authentication against a host over the right
// protocol and reports a categorised outcome. It NEVER returns or logs the
// secret — Detail strings are protocol/error notes only. The protocol mapping,
// secret parsing and error categorisation are pure (unit-tested); the probes
// reuse the existing transport packages (snmp/ssh/onvif/winrm).
package credtest

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/coralsearesorts/hims/internal/onvif"
	"github.com/coralsearesorts/hims/internal/osinv"
	"github.com/coralsearesorts/hims/internal/snmp"
	"github.com/coralsearesorts/hims/internal/ssh"
)

// Result categories — what an operator triages by.
const (
	CatSuccess     = "success"     // authenticated
	CatAuthFailed  = "auth_failed" // reached the service, credentials rejected
	CatUnreachable = "unreachable" // could not connect (port closed / timeout / no route)
	CatUnsupported = "unsupported" // this tester can't probe that kind
	CatError       = "error"       // anything else (malformed secret, protocol fault)
	// CatOperationFault: WinRM/NTLM auth SUCCEEDED but the WSMan operation faulted
	// (legacy WSMan 2.0 / Windows 7 / Server 2008 R2). The credential is valid —
	// never treat this as a wrong password; the host needs a legacy collector.
	CatOperationFault = osinv.WinRMOperationFault
)

// Outcome is the non-secret result of one credential↔host test.
type Outcome struct {
	Protocol  string `json:"protocol"`
	Category  string `json:"category"`
	Detail    string `json:"detail"`
	LatencyMS int64  `json:"latency_ms"`
}

// OK reports a successful authentication.
func (o Outcome) OK() bool { return o.Category == CatSuccess }

// Options tunes a test.
type Options struct {
	Timeout        time.Duration
	LegacyKEX      bool   // try legacy SSH KEX/ciphers for old switches
	CredentialName string // optional display label for safe debug logs (never a secret)
}

func (o Options) timeout() time.Duration {
	if o.Timeout <= 0 {
		return 8 * time.Second
	}
	return o.Timeout
}

// ProtocolForKind maps a credential kind to the probe protocol (pure).
func ProtocolForKind(kind string) string {
	k := strings.ToLower(strings.TrimSpace(kind))
	switch {
	case strings.HasPrefix(k, "snmp"):
		return "snmp"
	case k == "ssh" || k == "cli":
		return "ssh"
	case k == "winrm":
		return "winrm"
	case k == "onvif":
		return "onvif"
	case k == "http_basic" || k == "http" || k == "vendor_api":
		return "http"
	default:
		return ""
	}
}

// SplitUserPass splits a "username:password" secret on the first colon (pure).
// A secret with no colon is treated as a bare username with an empty password.
func SplitUserPass(secret string) (user, pass string) {
	if i := strings.IndexByte(secret, ':'); i >= 0 {
		return secret[:i], secret[i+1:]
	}
	return secret, ""
}

// categorizeConnErr classifies a connection/handshake error into a result
// category + short non-secret detail (pure — the input is an error string).
func categorizeErr(errStr string) (category, detail string) {
	e := strings.ToLower(errStr)
	switch {
	case strings.Contains(e, "unable to authenticate") || strings.Contains(e, "auth") ||
		strings.Contains(e, "permission denied") || strings.Contains(e, "401") ||
		strings.Contains(e, "403") || strings.Contains(e, "unauthorized") ||
		strings.Contains(e, "access denied") || strings.Contains(e, "credentials"):
		return CatAuthFailed, "authentication rejected"
	case strings.Contains(e, "refused") || strings.Contains(e, "timeout") ||
		strings.Contains(e, "no route") || strings.Contains(e, "i/o timeout") ||
		strings.Contains(e, "deadline exceeded") || strings.Contains(e, "no such host") ||
		strings.Contains(e, "connection reset"):
		return CatUnreachable, "could not connect"
	case strings.Contains(e, "no common algorithm") || strings.Contains(e, "key exchange") ||
		strings.Contains(e, "kex") || strings.Contains(e, "handshake"):
		// Old switches negotiate only legacy KEX/ciphers — retry with legacy_kex.
		return CatError, "SSH/TLS handshake failed (try legacy KEX)"
	default:
		return CatError, "probe error"
	}
}

// Test probes (kind, secret) against host and returns a categorised outcome.
func Test(ctx context.Context, kind, secret, host string, opts Options) Outcome {
	proto := ProtocolForKind(kind)
	start := time.Now()
	finish := func(o Outcome) Outcome {
		o.Protocol = proto
		o.LatencyMS = time.Since(start).Milliseconds()
		return o
	}
	switch proto {
	case "snmp":
		return finish(testSNMP(ctx, kind, secret, host, opts.timeout()))
	case "ssh":
		return finish(testSSH(ctx, secret, host, opts))
	case "http":
		return finish(testHTTP(ctx, secret, host, opts.timeout()))
	case "onvif":
		return finish(testONVIF(ctx, secret, host, opts.timeout()))
	case "winrm":
		return finish(testWinRM(ctx, secret, host, opts.timeout(), opts.CredentialName))
	default:
		return finish(Outcome{Category: CatUnsupported, Detail: "no tester for kind " + kind})
	}
}

func testSNMP(ctx context.Context, kind, secret, host string, timeout time.Duration) Outcome {
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return Outcome{Category: CatError, Detail: "bad host"}
	}
	tgt := snmp.Target{Addr: addr, Version: snmp.V2c, Community: secret, Timeout: timeout}
	if strings.Contains(strings.ToLower(kind), "v3") {
		v3, err := snmp.ParseV3JSON([]byte(secret))
		if err != nil {
			return Outcome{Category: CatError, Detail: "bad SNMPv3 parameters"}
		}
		tgt = snmp.Target{Addr: addr, Version: snmp.V3, V3: v3, Timeout: timeout}
	}
	cl, err := snmp.NewClient(tgt.WithDefaults())
	if err != nil {
		return Outcome{Category: CatError, Detail: "snmp init failed"}
	}
	if err := cl.Connect(ctx); err != nil {
		cat, detail := categorizeErr(err.Error())
		return Outcome{Category: cat, Detail: detail}
	}
	defer cl.Close()
	// sysDescr.0 — a read that any SNMP agent answers when the community is right.
	pdus, err := cl.Get(ctx, "1.3.6.1.2.1.1.1.0")
	if err != nil {
		// A wrong community times out (no authenticated error in SNMP v2c).
		return Outcome{Category: CatAuthFailed, Detail: "no response (wrong community or no access)"}
	}
	if len(pdus) == 0 {
		return Outcome{Category: CatAuthFailed, Detail: "empty response"}
	}
	return Outcome{Category: CatSuccess, Detail: "sysDescr read"}
}

func testSSH(ctx context.Context, secret, host string, opts Options) Outcome {
	user, pass := SplitUserPass(secret)
	err := ssh.CheckAuth(ctx, host, 22, ssh.Creds{Username: user, Password: pass}, opts.LegacyKEX, opts.timeout())
	if err == nil {
		return Outcome{Category: CatSuccess, Detail: "SSH login ok"}
	}
	cat, detail := categorizeErr(err.Error())
	return Outcome{Category: cat, Detail: detail}
}

func testHTTP(ctx context.Context, secret, host string, timeout time.Duration) Outcome {
	user, pass := SplitUserPass(secret)
	client := &http.Client{
		Timeout:       timeout,
		Transport:     &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, DisableKeepAlives: true}, //nolint:gosec
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	// Try HTTPS first, then HTTP — appliances vary.
	for _, scheme := range []string{"https", "http"} {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, scheme+"://"+host+"/", nil)
		if err != nil {
			continue
		}
		req.SetBasicAuth(user, pass)
		resp, err := client.Do(req)
		if err != nil {
			continue // try the other scheme
		}
		resp.Body.Close()
		switch {
		case resp.StatusCode == 401 || resp.StatusCode == 403:
			return Outcome{Category: CatAuthFailed, Detail: "HTTP " + resp.Status}
		case resp.StatusCode < 400:
			return Outcome{Category: CatSuccess, Detail: "HTTP " + resp.Status}
		default:
			return Outcome{Category: CatError, Detail: "HTTP " + resp.Status}
		}
	}
	return Outcome{Category: CatUnreachable, Detail: "no HTTP/HTTPS response"}
}

func testONVIF(ctx context.Context, secret, host string, timeout time.Duration) Outcome {
	user, pass := SplitUserPass(secret)
	client := onvif.NewClient("http://"+host, user, pass, &http.Client{Timeout: timeout})
	if _, err := onvif.Collect(ctx, client); err != nil {
		cat, detail := categorizeErr(err.Error())
		return Outcome{Category: cat, Detail: detail}
	}
	return Outcome{Category: CatSuccess, Detail: "ONVIF GetDeviceInformation ok"}
}

func testWinRM(ctx context.Context, secret, host string, timeout time.Duration, credName string) Outcome {
	user, pass := SplitUserPass(secret)
	// Use the SAME WinRM transport the deep-inventory collector uses (NTLM +
	// WSMan message encryption) so a "Test" result matches what Collect will do.
	err := osinv.WinRMCheckAuth(ctx, host, user, pass, timeout, credName)
	if err != nil {
		// A WSMan operation fault means auth SUCCEEDED on a legacy stack — classify
		// it as auth_ok_operation_fault, not a credential failure.
		if cat, detail, _ := osinv.ClassifyWinRMError(err); cat == osinv.WinRMOperationFault {
			return Outcome{Category: CatOperationFault, Detail: detail}
		}
	}
	if err != nil {
		cat, detail := categorizeErr(err.Error())
		return Outcome{Category: cat, Detail: detail}
	}
	return Outcome{Category: CatSuccess, Detail: "WinRM login ok"}
}
