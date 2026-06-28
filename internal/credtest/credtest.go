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
	"strconv"
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
	// CatWebReachable: the web/management page responded 2xx but served the SAME
	// content WITHOUT credentials — the device does not enforce auth on that
	// endpoint (e.g. a camera/NVR web UI that loads its login page on 200). This is
	// proof of REACHABILITY, not AUTHENTICATION: it must never bind a credential or
	// mark a device managed. Real auth requires an endpoint that challenges
	// (401/403) without creds and accepts (2xx) with them — or ONVIF/ISAPI.
	CatWebReachable = "web_reachable"
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
	// WMI/DCOM fallback: when set, a wmi credential test (after a successful DCOM
	// reachability probe) authenticates + collects through this WMI collector helper.
	WMICollectorURL   string
	WMICollectorToken string
	// WebPorts are the device's open web/management ports (e.g. 8015 on a
	// Hikvision NVR). An http_basic test tries these in addition to 80/443, so a
	// device whose web UI is on a non-standard port is reached instead of being
	// reported "no HTTP/HTTPS response" because only :80/:443 were probed.
	WebPorts []int
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
	case k == "winrm" || k == "windows":
		// A unified "windows" credential is tested over WinRM; the WMI/DCOM and
		// relay-agent transports are exercised at collection time.
		return "winrm"
	case k == "wmi":
		return "wmi"
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
		return finish(testHTTP(ctx, secret, host, opts.timeout(), opts.WebPorts))
	case "onvif":
		return finish(testONVIF(ctx, secret, host, opts.timeout()))
	case "winrm":
		return finish(testWinRM(ctx, secret, host, opts.timeout(), opts.CredentialName))
	case "wmi":
		return finish(testWMI(ctx, secret, host, opts))
	default:
		return finish(Outcome{Category: CatUnsupported, Detail: "no tester for kind " + kind})
	}
}

func testSNMP(ctx context.Context, kind, secret, host string, timeout time.Duration) Outcome {
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return Outcome{Category: CatError, Detail: "bad host"}
	}
	if strings.Contains(strings.ToLower(kind), "v3") {
		v3, err := snmp.ParseV3JSON([]byte(secret))
		if err != nil {
			return Outcome{Category: CatError, Detail: "bad SNMPv3 parameters"}
		}
		return snmpGetOK(ctx, snmp.Target{Addr: addr, Version: snmp.V3, V3: v3, Timeout: timeout}, "v3")
	}
	// Community-based: try v2c, then fall back to v1 with the SAME community. The
	// community is version-agnostic, and many printers / older devices answer ONLY
	// SNMPv1 — without this they read as auth_failed despite SNMP being enabled.
	if oc := snmpGetOK(ctx, snmp.Target{Addr: addr, Version: snmp.V2c, Community: secret, Timeout: timeout}, "v2c"); oc.Category == CatSuccess {
		return oc
	}
	return snmpGetOK(ctx, snmp.Target{Addr: addr, Version: snmp.V1, Community: secret, Timeout: timeout}, "v1")
}

// snmpGetOK runs the sysDescr.0 read for one target and maps the result to an
// Outcome. label notes which SNMP version answered on success.
func snmpGetOK(ctx context.Context, tgt snmp.Target, label string) Outcome {
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
		// A wrong community times out (no authenticated error in SNMP v1/v2c).
		return Outcome{Category: CatAuthFailed, Detail: "no response (wrong community or no access)"}
	}
	if len(pdus) == 0 {
		return Outcome{Category: CatAuthFailed, Detail: "empty response"}
	}
	return Outcome{Category: CatSuccess, Detail: "sysDescr read (" + label + ")"}
}

func testSSH(ctx context.Context, secret, host string, opts Options) Outcome {
	user, pass := SplitUserPass(secret)
	creds := ssh.Creds{Username: user, Password: pass}
	err := ssh.CheckAuth(ctx, host, 22, creds, opts.LegacyKEX, opts.timeout())
	// Auto-retry with legacy KEX/ciphers + SHA-1 host-key algorithms when the FIRST
	// (modern) handshake failed at the algorithm-negotiation layer — old switches/servers
	// only speak diffie-hellman-group1/14-sha1 + CBC + ssh-rsa host keys. This makes the
	// legacy ladder automatic (no operator toggle) so a legacy host is never reported a
	// dead "handshake failed" when a legacy negotiation would have connected. Skipped if
	// the first attempt was already legacy, or failed for a non-handshake reason (auth
	// rejected, refused, timeout) where a legacy retry cannot help.
	if err != nil && !opts.LegacyKEX && isHandshakeAlgoError(err.Error()) {
		err = ssh.CheckAuth(ctx, host, 22, creds, true, opts.timeout())
	}
	if err == nil {
		return Outcome{Category: CatSuccess, Detail: "SSH login ok"}
	}
	cat, detail := categorizeErr(err.Error())
	return Outcome{Category: cat, Detail: detail}
}

// isHandshakeAlgoError reports whether an SSH error is an algorithm-negotiation
// failure (key exchange / cipher / host-key / signature algorithm) — the only class a
// legacy-algorithm retry can fix. An auth rejection, connection refusal, or timeout is
// excluded (a legacy retry would not change the outcome and only adds load).
func isHandshakeAlgoError(e string) bool {
	e = strings.ToLower(e)
	if !strings.Contains(e, "handshake") {
		return false
	}
	return strings.Contains(e, "no common algorithm") || strings.Contains(e, "key exchange") ||
		strings.Contains(e, "kex") || strings.Contains(e, "cipher") ||
		strings.Contains(e, "host key") || strings.Contains(e, "hostkey") ||
		strings.Contains(e, "signature algorithm") || strings.Contains(e, "no common")
}

func testHTTP(ctx context.Context, secret, host string, timeout time.Duration, webPorts []int) Outcome {
	user, pass := SplitUserPass(secret)
	client := &http.Client{
		Timeout:       timeout,
		Transport:     &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, DisableKeepAlives: true}, //nolint:gosec
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	// Candidate (scheme, port) endpoints. Standard ports first, then any open
	// web/management port the device exposes (e.g. 8015 on a Hikvision NVR). For
	// a non-standard port try HTTP before HTTPS — the 8000+octet CCTV web/ISAPI
	// ports are plain HTTP, and probing HTTPS first would hang on the full timeout.
	type ep struct {
		url string
	}
	eps := []ep{{"https://" + host + "/"}, {"http://" + host + "/"}}
	for _, p := range webPorts {
		if p == 80 || p == 443 {
			continue
		}
		base := host + ":" + strconv.Itoa(p)
		eps = append(eps, ep{"http://" + base + "/"}, ep{"https://" + base + "/"})
	}
	for _, e := range eps {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.url, nil)
		if err != nil {
			continue
		}
		req.SetBasicAuth(user, pass)
		resp, err := client.Do(req)
		if err != nil {
			continue // try the next endpoint
		}
		resp.Body.Close()
		switch {
		case resp.StatusCode == 401 || resp.StatusCode == 403:
			return Outcome{Category: CatAuthFailed, Detail: "HTTP " + resp.Status}
		case resp.StatusCode >= 300 && resp.StatusCode < 400:
			// A redirect is NOT proof of authentication: a server that ignores the
			// Authorization header and redirects to a login/SSO page returns 302 just
			// the same as one that authenticated. Counting it as success let wrong
			// credentials (e.g. an iDRAC http_basic cred on a plain web app) falsely
			// "bind" and mark a device managed while collecting nothing. Treat a bare
			// redirect as not-authenticated so it never binds.
			return Outcome{Category: CatAuthFailed, Detail: "HTTP " + resp.Status + " — redirected (HTTP basic not accepted; likely a login/SSO page)"}
		case resp.StatusCode < 300:
			// A 2xx is proof of authentication ONLY if the endpoint actually enforces
			// it. A camera/NVR web UI returns 200 for its login page with ANY (or no)
			// credentials — counting that as success falsely binds a credential and
			// marks the device managed via an anonymous page load. Verify by probing
			// the SAME endpoint with NO credentials: if it is still 2xx, the page is
			// public → web-reachable, NOT authenticated (never bind). Only when the
			// no-credential probe is challenged (401/403/redirect) did the credential
			// actually matter → real success. Cameras/NVR/DVR are authenticated via
			// the dedicated ONVIF/ISAPI testers instead.
			if httpAuthEnforced(ctx, client, e.url) {
				return Outcome{Category: CatSuccess, Detail: "HTTP " + resp.Status + " (authenticated — endpoint challenged without credentials)"}
			}
			return Outcome{Category: CatWebReachable, Detail: "HTTP " + resp.Status + " — page served WITHOUT credentials (web-reachable, not authenticated; not bound)"}
		default:
			return Outcome{Category: CatError, Detail: "HTTP " + resp.Status}
		}
	}
	return Outcome{Category: CatUnreachable, Detail: "no HTTP/HTTPS response"}
}

// httpAuthEnforced reports whether url actually requires authentication: a GET
// with NO Authorization header is denied (401/403 or a redirect to a login
// page). If the no-credential request is itself 2xx, the content is public and a
// prior authenticated 2xx proves nothing. A transport error is treated as
// "not enforced" (conservative — never bind on an unprovable success).
func httpAuthEnforced(ctx context.Context, client *http.Client, url string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	// Denied without creds (401/403) or redirected to a login page (3xx) => the
	// credential was required. A 2xx without creds => public page (not auth).
	return resp.StatusCode < 200 || resp.StatusCode >= 300
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

// testWMI probes DCOM/RPC reachability (135) and, when a WMI collector helper is
// configured, authenticates + collects through it. A reachable host with no
// collector is "unsupported" (honest gate), not success — open RPC ports never
// count as managed access. Only a real auth+collect is success.
func testWMI(ctx context.Context, secret, host string, opts Options) Outcome {
	reachable, cat, detail := osinv.WMIProbeReachable(ctx, host, opts.timeout())
	if !reachable {
		return Outcome{Category: cat, Detail: detail}
	}
	if opts.WMICollectorURL == "" {
		return Outcome{Category: osinv.WMIUnsupported, Detail: "DCOM/RPC reachable on 135, but no WMI collector is configured (set HIMS_WMI_COLLECTOR_URL or deploy the WMI collector helper)."}
	}
	user, pass := SplitUserPass(secret)
	if _, err := osinv.CollectViaWMICollector(ctx, opts.WMICollectorURL, opts.WMICollectorToken, host, user, pass, opts.timeout()); err != nil {
		c, d := osinv.ClassifyWMIError(err)
		return Outcome{Category: c, Detail: d}
	}
	return Outcome{Category: CatSuccess, Detail: "WMI authenticated + collected"}
}
