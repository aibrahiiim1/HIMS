// Package discovery implements the staged discovery pipeline:
//
//	Input Scope → Host Detection → Port/Protocol Fingerprint →
//	Credential Resolver → Authenticated Probe → Device Classification →
//	Template Matching → Deep Collection → Inventory Update
//
// The pipeline is a sequence of self-contained steps; each step enriches the
// shared HostResult and either advances to the next step or marks the host
// as skipped/failed. No step mutates the DB directly — that is the
// persistence layer's job, called at the end.
package discovery

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/coralsearesorts/hims/internal/classify"
	"github.com/coralsearesorts/hims/internal/credresolver"
	"github.com/coralsearesorts/hims/internal/credtest"
	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/driver"
	"github.com/coralsearesorts/hims/internal/driver/swsnmp"
	"github.com/coralsearesorts/hims/internal/snmp"
)

// CredAttempt records one credential↔host authentication attempt during a scan,
// so the orchestrator can persist it to credential-test history (success AND
// failure, with a non-secret reason). No secret is ever captured here.
type CredAttempt struct {
	CredentialID uuid.UUID
	Kind         domain.CredentialKind
	Protocol     string
	Success      bool
	Category     string // credtest category: success|auth_failed|unreachable|...
	Detail       string // non-secret reason
}

func hasPortN(ports []int, p int) bool {
	for _, x := range ports {
		if x == p {
			return true
		}
	}
	return false
}

// fingerprintFromPorts builds the credential-resolver fingerprint from the open
// TCP ports. SNMP is always enabled — it is UDP/161 and therefore invisible to a
// TCP port scan, so we must always offer SNMP candidates (switches/printers).
// The TCP-detectable protocols are gated on their port being open.
func fingerprintFromPorts(ports []int) credresolver.Fingerprint {
	return credresolver.Fingerprint{
		SNMP:  true,
		SSH:   hasPortN(ports, 22),
		WinRM: hasPortN(ports, 5985) || hasPortN(ports, 5986),
		HTTP:  hasPortN(ports, 80) || hasPortN(ports, 443) || hasPortN(ports, 8000) || hasPortN(ports, 8080) || hasPortN(ports, 8443),
		LDAP:  hasPortN(ports, 389) || hasPortN(ports, 636),
	}
}

// portAllowsProto gates a non-SNMP credential test to a host that actually
// exposes that protocol's port (don't WinRM-probe a host with no 5985 open).
func portAllowsProto(ports []int, kind domain.CredentialKind) bool {
	switch kind {
	case domain.CredSSH:
		return hasPortN(ports, 22)
	case domain.CredWinRM:
		return hasPortN(ports, 5985) || hasPortN(ports, 5986)
	case domain.CredONVIF, domain.CredHTTPBasic, domain.CredVendorAPI:
		return hasPortN(ports, 80) || hasPortN(ports, 443) || hasPortN(ports, 8000) || hasPortN(ports, 8080) || hasPortN(ports, 8443)
	}
	return false
}

// provisionalCategory is the best-effort category for a host that authenticated
// via a non-SNMP credential during the scan, before a deep OS collection refines
// it. Windows→endpoint and Linux(SSH)→server are then corrected by Collect OS
// using the real OS caption; ONVIF→camera is already definitive.
func provisionalCategory(kind domain.CredentialKind) domain.DeviceCategory {
	switch kind {
	case domain.CredWinRM:
		return domain.CatEndpoint
	case domain.CredSSH:
		return domain.CatServer
	case domain.CredONVIF:
		return domain.CatCamera
	}
	return domain.CatUnknown
}

// HostResult accumulates what the pipeline learned about one IP.
type HostResult struct {
	IP         netip.Addr
	Alive      bool
	OpenPorts  []int
	Probe      driver.Probe
	MatchedDrv driver.Driver
	Match      driver.Match
	// Credential the pipeline authenticated with (nil = no auth yet).
	BoundCred *credresolver.CredRef
	// CredAttempts is every authentication attempt made this run (for history).
	CredAttempts []CredAttempt
	Facts        *driver.Facts
	Error        error
}

// CandidateFetcher abstracts the DB call that assembles credentials for an IP.
type CandidateFetcher interface {
	CredentialCandidates(ctx context.Context, ip netip.Addr, locationID *uuid.UUID) ([]credresolver.ScopedGroup, error)
}

// DecryptedCred is a credential with its secret already decrypted (by the
// caller — the pipeline never stores plaintext).
type DecryptedCred struct {
	ID        uuid.UUID
	Kind      domain.CredentialKind
	Community string         // for SNMP v2c (or "user:password" for http/winrm)
	V3        *snmp.V3Params // for SNMP v3 (USM)
	Weak      bool
}

// ParseSNMPv3 decodes an SNMP v3 credential secret (JSON) into USM params.
func ParseSNMPv3(secret []byte) (*snmp.V3Params, error) { return snmp.ParseV3JSON(secret) }

// DecryptFn decrypts a credential blob. The pipeline calls it only on the
// credentials it's about to try — not the entire set.
type DecryptFn func(ctx context.Context, credID uuid.UUID) (DecryptedCred, error)

// PipelineConfig wires the pipeline dependencies.
type PipelineConfig struct {
	Registry *driver.Registry
	Fetcher  CandidateFetcher
	Decrypt  DecryptFn
	// ExtraGroups are operator-selected credential groups for this scan. They
	// are injected as the highest-specificity candidate tier (above any
	// scope-resolved bindings), so the resolver still owns ordering and the
	// no-per-device-picker discipline holds: the operator picks GROUPS for a
	// scan, not a credential for a device. Empty = pure scope auto-resolution.
	ExtraGroups []credresolver.ScopedGroup
	// Timeout for each per-host step.
	PingTimeout time.Duration
	SNMPTimeout time.Duration
	// PortTimeout bounds each TCP port-connect during the port scan + aliveness
	// check. Zero falls back to 500ms.
	PortTimeout time.Duration
}

// explicitTierSpecificity ranks operator-selected groups above subnet (2) and
// location (1) scope bindings.
const explicitTierSpecificity = 100

// Run runs the pipeline for a single IP and returns its result.
// All steps are attempted; errors within optional steps (e.g. deep collect)
// are recorded in result.Error but do not abort the pipeline.
func Run(ctx context.Context, ip netip.Addr, locationID *uuid.UUID, cfg PipelineConfig) HostResult { //nolint:gocritic
	r := HostResult{IP: ip}

	// Step 1: TCP port scan — management ports for every device class (switches/
	// servers via SSH/SNMP-mgmt, Windows via SMB/RPC/WinRM/RDP, cameras via
	// RTSP/HTTP, printers via JetDirect) plus the service ports role inference
	// keys on (DNS/DC/DB). This breadth lets the scan DETECT non-SNMP hosts
	// (Windows workstations, Linux, cameras) that the old switch-centric list
	// missed entirely.
	r.OpenPorts = scanPorts(ctx, ip, []int{22, 23, 53, 80, 88, 135, 161, 389, 443, 445, 554, 636, 1433, 1521, 3389, 5432, 5985, 5986, 8000, 8080, 8443, 9100}, cfg.PortTimeout)
	r.Probe = driver.Probe{IP: ip, OpenTCPPorts: r.OpenPorts}

	// Step 2: Resolve credential candidates (scope-bound + operator-selected /
	// all-stored, the latter injected by the scan as ExtraGroups).
	groups, ferr := cfg.Fetcher.CredentialCandidates(ctx, ip, locationID)
	if ferr != nil {
		r.Error = fmt.Errorf("fetch credentials: %w", ferr)
	}
	groups = append(groups, cfg.ExtraGroups...)
	candidates := credresolver.Resolve(credresolver.Input{
		Fingerprint: fingerprintFromPorts(r.OpenPorts), Groups: groups,
	})

	// Step 3: SNMP probe for sysDescr + sysObjectID. Try the resolved/selected
	// SNMP credentials FIRST — a switch using a real community must classify, so
	// classification cannot rely on public/private alone — then fall back to the
	// default communities. The first that answers gives the probe data, a live
	// session for deep collection, and (for a stored credential) the bind.
	var authCli snmp.Client
	probe := func(tgt snmp.Target) bool {
		cli, err := snmp.NewClient(tgt)
		if err != nil {
			return false
		}
		if err := cli.Connect(ctx); err != nil {
			_ = cli.Close()
			return false
		}
		pdus, err := cli.Get(ctx, "1.3.6.1.2.1.1.1.0", "1.3.6.1.2.1.1.2.0")
		if err != nil || len(pdus) < 1 {
			_ = cli.Close()
			return false
		}
		r.Probe.SNMPSysDescr = snmp.PDUString(pdus[0])
		if len(pdus) > 1 {
			r.Probe.SNMPSysObjectID = snmp.PDUString(pdus[1])
		}
		authCli = cli
		return true
	}

	for _, cand := range candidates {
		if cand.Kind != domain.CredSNMPv2c && cand.Kind != domain.CredSNMPv3 {
			continue
		}
		dec, err := cfg.Decrypt(ctx, cand.ID)
		if err != nil {
			continue
		}
		tgt := snmp.Target{Addr: ip, Version: snmp.V2c, Community: dec.Community, Timeout: cfg.SNMPTimeout}
		if cand.Kind == domain.CredSNMPv3 && dec.V3 != nil {
			tgt = snmp.Target{Addr: ip, Version: snmp.V3, V3: dec.V3, Timeout: cfg.SNMPTimeout}
		}
		ok := probe(tgt)
		r.CredAttempts = append(r.CredAttempts, snmpAttempt(cand, ok))
		if ok {
			c := cand
			r.BoundCred = &c // bind-on-success (only for stored credentials)
			break
		}
	}
	if authCli == nil { // fallback: default communities, no bind
		for _, comm := range []string{"public", "private"} {
			if probe(snmp.Target{Addr: ip, Version: snmp.V2c, Community: comm, Timeout: cfg.SNMPTimeout}) {
				break
			}
		}
	}
	snmpAnswered := r.Probe.SNMPSysDescr != "" || r.Probe.SNMPSysObjectID != ""

	// Step 3b: HTTP banner — for hosts exposing a web port, grab the Server
	// header + page <title> + a small body snippet so banner-based drivers
	// (cameras, web apps like Kea/Stork DHCP) can classify a device that has no
	// SNMP. Cheap: one GET with a short timeout.
	if httpPort(r.OpenPorts) {
		if server, title, body := httpBanner(ctx, ip, r.OpenPorts, cfg.PortTimeout); server != "" || title != "" || body != "" {
			r.Probe.HTTPServer = server
			if r.Probe.Hints == nil {
				r.Probe.Hints = map[string]string{}
			}
			r.Probe.Hints["http_title"] = title
			r.Probe.Hints["http_body"] = body
		}
	}

	// Step 3c: Non-SNMP authenticated probe. SNMP classifies network gear; Windows
	// (WinRM), Linux (SSH) and cameras (ONVIF/HTTP) need their own auth. Try the
	// resolved non-SNMP candidates — gated to the host's open management port —
	// and bind the first that authenticates. This is what onboards workstations /
	// servers / VM hosts instead of leaving them "unknown" with no credential.
	// Skipped once SNMP already bound a credential (switches).
	var authedKind domain.CredentialKind
	if r.BoundCred == nil {
		for _, cand := range candidates {
			if cand.Kind == domain.CredSNMPv2c || cand.Kind == domain.CredSNMPv3 {
				continue
			}
			if !portAllowsProto(r.OpenPorts, cand.Kind) {
				continue
			}
			dec, derr := cfg.Decrypt(ctx, cand.ID)
			if derr != nil || dec.Community == "" {
				continue
			}
			out := credtest.Test(ctx, string(cand.Kind), dec.Community, ip.String(), credtest.Options{})
			r.CredAttempts = append(r.CredAttempts, CredAttempt{
				CredentialID: cand.ID, Kind: cand.Kind, Protocol: out.Protocol,
				Success: out.OK(), Category: out.Category, Detail: out.Detail,
			})
			if out.OK() {
				c := cand
				r.BoundCred = &c // bind-on-success
				authedKind = cand.Kind
				break
			}
		}
	}

	// Step 4: Aliveness. A host is enrolled only if it actually responded — an
	// open TCP port or an SNMP answer. This stops a subnet scan from enrolling
	// every empty address as an "unknown" device.
	r.Alive = len(r.OpenPorts) > 0 || snmpAnswered
	if !r.Alive {
		if authCli != nil {
			_ = authCli.Close()
		}
		return r
	}

	// Step 5: Driver classification (now informed by the real-credential probe).
	r.MatchedDrv, r.Match = cfg.Registry.Best(r.Probe)

	// Step 5b: Evidence-based candidate classification. When no driver claimed the
	// host, derive a best-effort category from SAFE UNAUTHENTICATED evidence —
	// open ports, SNMP sysDescr, HTTP server/title, SSH banner — so the host is
	// explained (Windows endpoint, Linux, camera, printer…) instead of a blank
	// "unknown", even if no credential authenticated. This never counts as
	// managed access; it is only a classification hint with its own confidence.
	if r.Match.Category == "" || r.Match.Category == domain.CatUnknown {
		ev := classify.OpenPorts(r.OpenPorts)
		if r.Probe.SNMPSysDescr != "" {
			ev = append(ev, classify.SNMPSysDescr(r.Probe.SNMPSysDescr)...)
		}
		if r.Probe.HTTPServer != "" || r.Probe.Hints["http_title"] != "" {
			ev = append(ev, classify.HTTPServer(r.Probe.HTTPServer, r.Probe.Hints["http_title"])...)
		}
		if hasPortN(r.OpenPorts, 22) {
			if banner := grabSSHBanner(ctx, ip, cfg.PortTimeout); banner != "" {
				ev = append(ev, classify.SSHBanner(banner)...)
			}
		}
		res := classify.FromEvidence(ev)
		switch {
		case res.Category != "" && res.Category != string(domain.CatUnknown) && res.Confidence > 0:
			r.Match = driver.Match{Category: domain.DeviceCategory(res.Category), Confidence: res.Confidence}
		case res.OSFamily == domain.OSFamilyWindows:
			// Windows signals (RDP/SMB/WinRM) with no specific category → endpoint candidate.
			r.Match = driver.Match{Category: domain.CatEndpoint, Confidence: 35}
		}
	}

	// If a non-SNMP credential AUTHENTICATED, that's a stronger signal than
	// unauthenticated evidence — assign the protocol's provisional category. A
	// deep OS collection (run by the orchestrator for WinRM/SSH binds) then
	// refines workstation vs server from the real OS caption.
	if authedKind != "" {
		if cat := provisionalCategory(authedKind); cat != domain.CatUnknown {
			r.Match = driver.Match{Category: cat, Confidence: 60}
		}
	}

	// Step 6: Deep collection — only when a driver matched AND we hold a live
	// authenticated SNMP session.
	if authCli != nil {
		if col, ok := r.MatchedDrv.(driver.Collector); ok {
			facts, err := col.Collect(&swsnmp.Session{Client: authCli, Ctx: ctx}, r.Probe)
			if err == nil {
				r.Facts = &facts
			} else {
				r.Error = err
			}
		}
		_ = authCli.Close()
	}
	return r
}

// snmpAttempt records one SNMP credential probe outcome for credential-test
// history. Non-secret reason strings only.
func snmpAttempt(cand credresolver.CredRef, ok bool) CredAttempt {
	cat, detail := "auth_failed", "no SNMP response (wrong community or no access)"
	if ok {
		cat, detail = "success", "authenticated"
	}
	return CredAttempt{CredentialID: cand.ID, Kind: cand.Kind, Protocol: "snmp", Success: ok, Category: cat, Detail: detail}
}

// grabSSHBanner reads the SSH server identification line (e.g.
// "SSH-2.0-OpenSSH_8.0p1 Ubuntu-...") — safe, unauthenticated OS evidence.
func grabSSHBanner(ctx context.Context, ip netip.Addr, timeout time.Duration) string {
	if timeout <= 0 {
		timeout = 1500 * time.Millisecond
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := (&net.Dialer{Timeout: timeout}).DialContext(cctx, "tcp", net.JoinHostPort(ip.String(), "22"))
	if err != nil {
		return ""
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	buf := make([]byte, 256)
	n, _ := conn.Read(buf)
	return strings.TrimSpace(string(buf[:n]))
}

// --- Transport helpers --------------------------------------------------------

func scanPorts(ctx context.Context, ip netip.Addr, ports []int, timeout time.Duration) []int {
	if timeout <= 0 {
		timeout = 500 * time.Millisecond
	}
	open := make([]int, 0, len(ports))
	d := &net.Dialer{}
	for _, port := range ports {
		tctx, cancel := context.WithTimeout(ctx, timeout)
		c, err := d.DialContext(tctx, "tcp", fmt.Sprintf("%s:%d", ip, port))
		cancel()
		if err == nil {
			_ = c.Close()
			open = append(open, port)
		}
	}
	sort.Ints(open)
	return open
}

var titleRe = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)

// httpPort reports whether any common web port is open.
func httpPort(ports []int) bool {
	for _, p := range []int{443, 80, 8443, 8080, 8000} {
		if hasPort(ports, p) {
			return true
		}
	}
	return false
}

// httpBanner does a single GET against the first open web port and returns the
// Server header, the page <title>, and a lowercased body snippet (≤4KB). It is
// best-effort: any error yields empty strings. TLS is insecure (mgmt-LAN
// self-signed certs are normal).
func httpBanner(ctx context.Context, ip netip.Addr, ports []int, timeout time.Duration) (server, title, body string) {
	if timeout <= 0 {
		timeout = 2 * time.Second
	} else if timeout > 3*time.Second {
		timeout = 3 * time.Second // keep the scan snappy regardless of SNMP timeout
	}
	scheme, port := "https", 443
	switch {
	case hasPort(ports, 443):
		scheme, port = "https", 443
	case hasPort(ports, 8443):
		scheme, port = "https", 8443
	case hasPort(ports, 80):
		scheme, port = "http", 80
	case hasPort(ports, 8080):
		scheme, port = "http", 8080
	case hasPort(ports, 8000):
		scheme, port = "http", 8000
	}
	client := &http.Client{
		Timeout:   timeout,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, //nolint:gosec
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse // don't chase redirects off-host
		},
	}
	url := fmt.Sprintf("%s://%s:%d/", scheme, ip, port)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", "", ""
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", ""
	}
	defer resp.Body.Close()
	server = resp.Header.Get("Server")
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	low := strings.ToLower(string(raw))
	if m := titleRe.FindStringSubmatch(string(raw)); len(m) > 1 {
		title = strings.TrimSpace(m[1])
	}
	return server, title, low
}

func hasPort(ports []int, port int) bool {
	for _, p := range ports {
		if p == port {
			return true
		}
	}
	return false
}

// ScopeRange is a sequence of IPs to scan; the engine generates them from a
// CIDR or an explicit list.
type ScopeRange struct {
	IPs        []netip.Addr
	LocationID *uuid.UUID // nil for single-IP without a location
}
