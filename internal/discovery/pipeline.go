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
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/coralsearesorts/hims/internal/classify"
	"github.com/coralsearesorts/hims/internal/credresolver"
	"github.com/coralsearesorts/hims/internal/credtest"
	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/driver"
	"github.com/coralsearesorts/hims/internal/driver/swsnmp"
	"github.com/coralsearesorts/hims/internal/fingerprint"
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
	Relevant     bool   // protocol is the expected/relevant one for this candidate
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
	// Plan is the expected-protocol decision made before credential testing.
	Plan  ProtocolPlan
	Facts *driver.Facts
	// Vendor/Model from a matched fingerprint (canonical vendor + product model),
	// applied over the driver's generic identity when a specific fingerprint hits.
	Vendor string
	Model  string
	Error  error
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
	// ExtraPorts are appended to the standard management-port set for the
	// aliveness/port scan. The Known-Device Retry pass passes a missed device's
	// last-known open ports here so a host listening only on a non-standard port
	// is still re-detected on the targeted retry.
	ExtraPorts []int
	// OnEvent, if set, is called with a play-by-play event at each probe stage so
	// the live discovery board can show what is happening in real time. Must be
	// cheap + non-blocking (the scan calls it on the hot path).
	OnEvent func(PipelineEvent)
	// Fingerprints is the vendor-fingerprint library (operator-defined ∪ built-in)
	// applied to the SNMP/HTTP/SSH evidence to override the driver category and
	// supply a canonical vendor + product model. Empty = no fingerprint overrides.
	Fingerprints []fingerprint.Print
}

// PipelineEvent is one live stage event emitted during a host probe.
type PipelineEvent struct {
	Stage    string // e.g. target_probe_started, tcp_port_found, snmp_success, credential_bound
	Protocol string // snmp | ssh | winrm | onvif | wmi | http_basic | ...
	Status   string // started | success | failed
	Message  string
}

// explicitTierSpecificity ranks operator-selected groups above subnet (2) and
// location (1) scope bindings.
const explicitTierSpecificity = 100

// Run runs the pipeline for a single IP and returns its result.
// All steps are attempted; errors within optional steps (e.g. deep collect)
// are recorded in result.Error but do not abort the pipeline.
func Run(ctx context.Context, ip netip.Addr, locationID *uuid.UUID, cfg PipelineConfig) HostResult { //nolint:gocritic
	r := HostResult{IP: ip}
	emit := func(stage, proto, status, msg string) {
		if cfg.OnEvent != nil {
			cfg.OnEvent(PipelineEvent{Stage: stage, Protocol: proto, Status: status, Message: msg})
		}
	}
	emit("target_probe_started", "", "started", "")

	// Step 1: TCP port scan — management ports for every device class (switches/
	// servers via SSH/SNMP-mgmt, Windows via SMB/RPC/WinRM/RDP, cameras via
	// RTSP/HTTP, printers via JetDirect) plus the service ports role inference
	// keys on (DNS/DC/DB). This breadth lets the scan DETECT non-SNMP hosts
	// (Windows workstations, Linux, cameras) that the old switch-centric list
	// missed entirely.
	ports := []int{22, 23, 53, 80, 88, 135, 161, 389, 443, 445, 554, 636, 1433, 1521, 3389, 5432, 5985, 5986, 8000, 8080, 8443, 9100}
	for _, p := range cfg.ExtraPorts { // targeted-retry: a missed known device's last-known open ports
		if p > 0 && p < 65536 {
			ports = append(ports, p)
		}
	}
	r.OpenPorts = scanPorts(ctx, ip, ports, cfg.PortTimeout)
	r.Probe = driver.Probe{IP: ip, OpenTCPPorts: r.OpenPorts}
	if len(r.OpenPorts) > 0 {
		emit("tcp_port_found", "", "found", intsCSV(r.OpenPorts))
	}

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

	// Step 2b: Cheap unauthenticated banners (HTTP Server/title/body + SSH ident)
	// — gathered BEFORE any credential test so the protocol plan can be decided
	// from real evidence, not guesses.
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
	sshBanner := ""
	if hasPortN(r.OpenPorts, 22) {
		sshBanner = grabSSHBanner(ctx, ip, cfg.PortTimeout)
		if sshBanner != "" {
			if r.Probe.Hints == nil {
				r.Probe.Hints = map[string]string{}
			}
			r.Probe.Hints["ssh_banner"] = sshBanner
		}
	}

	// Step 2c: Protocol plan — decide the expected protocol(s) and which credential
	// kinds are worth testing for THIS target. This is what stops the scan from
	// trying SNMP/ONVIF/SSH against an obvious Windows workstation (and recording
	// the resulting auth_failed as a scary "credential problem").
	plan := planProtocols(r.OpenPorts, sshBanner, r.Probe.HTTPServer, r.Probe.Hints["http_title"], r.Probe.Hints["http_body"])
	r.Plan = plan

	// Step 3: SNMP probe for sysDescr + sysObjectID. Tried OPPORTUNISTICALLY on
	// every alive host (SNMP is UDP/161 — invisible to a TCP port scan, so we
	// can't infer it from open ports). The resolved/selected SNMP communities are
	// tried first; default communities are a relevant-only fallback. The first that
	// answers gives the probe data, a live session for deep collection, and the
	// bind. Failures are only surfaced when SNMP was expected (see below).
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
		// Identity OIDs collected on every SNMP success: sysDescr, sysObjectID,
		// sysName, sysContact, sysLocation. The raw values feed classification
		// (fingerprints) + inventory; we never store only the firmware string.
		pdus, err := cli.Get(ctx, "1.3.6.1.2.1.1.1.0", "1.3.6.1.2.1.1.2.0",
			"1.3.6.1.2.1.1.5.0", "1.3.6.1.2.1.1.4.0", "1.3.6.1.2.1.1.6.0")
		if err != nil || len(pdus) < 1 {
			_ = cli.Close()
			return false
		}
		r.Probe.SNMPSysDescr = snmp.PDUString(pdus[0])
		if len(pdus) > 1 {
			r.Probe.SNMPSysObjectID = snmp.PDUString(pdus[1])
		}
		if len(pdus) > 2 {
			r.Probe.SNMPSysName = snmp.PDUString(pdus[2])
		}
		if len(pdus) > 3 {
			r.Probe.SNMPSysContact = snmp.PDUString(pdus[3])
		}
		if len(pdus) > 4 {
			r.Probe.SNMPSysLocation = snmp.PDUString(pdus[4])
		}
		authCli = cli
		return true
	}

	// SNMP is UDP/161 and invisible to a TCP port scan, so open ports can't tell us
	// whether a host speaks SNMP. ALWAYS try the resolved SNMP communities on every
	// alive host (opportunistic probe). Behaviour split:
	//   - success → record it (Managed via SNMP), bind, classify from sysDescr, and
	//     SHORT-CIRCUIT remaining communities.
	//   - failure → recorded as a visible auth_failed attempt ONLY when SNMP was
	//     EXPECTED for this candidate (network/printer/unknown). An opportunistic
	//     no-response on a Linux/Windows host stays SILENT — it never pollutes
	//     Credential Health.
	snmpRelevant := plan.SNMPRelevant()
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
		emit("snmp_attempt_started", "snmp", "started", "")
		if probe(tgt) {
			r.CredAttempts = append(r.CredAttempts, snmpAttempt(cand, true)) // success always recorded → proven
			c := cand
			r.BoundCred = &c // bind-on-success, then stop trying further communities
			emit("snmp_success", "snmp", "success", r.Probe.SNMPSysDescr)
			emit("credential_bound", "snmp", "success", "")
			break
		}
		if snmpRelevant {
			// Surface the SNMP failure only when SNMP was expected for this candidate.
			r.CredAttempts = append(r.CredAttempts, snmpAttempt(cand, false))
			emit("snmp_failed_relevant", "snmp", "failed", "")
		}
	}
	if authCli == nil && snmpRelevant { // default-community fallback only when SNMP is expected
		for _, comm := range []string{"public", "private"} {
			if probe(snmp.Target{Addr: ip, Version: snmp.V2c, Community: comm, Timeout: cfg.SNMPTimeout}) {
				break
			}
		}
	}
	snmpAnswered := r.Probe.SNMPSysDescr != "" || r.Probe.SNMPSysObjectID != ""

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
			// Protocol-plan gate: only test credential kinds that are RELEVANT to
			// this candidate (don't WinRM/ONVIF/SSH-probe a host the evidence says
			// is something else). The open-port gate stays as a second guard.
			if !plan.Relevant(cand.Kind) || !portAllowsProto(r.OpenPorts, cand.Kind) {
				continue
			}
			dec, derr := cfg.Decrypt(ctx, cand.ID)
			if derr != nil || dec.Community == "" {
				continue
			}
			emit(string(cand.Kind)+"_attempt_started", string(cand.Kind), "started", "")
			out := credtest.Test(ctx, string(cand.Kind), dec.Community, ip.String(), credtest.Options{})
			r.CredAttempts = append(r.CredAttempts, CredAttempt{
				CredentialID: cand.ID, Kind: cand.Kind, Protocol: out.Protocol,
				Success: out.OK(), Category: out.Category, Detail: out.Detail, Relevant: true,
			})
			if out.OK() {
				c := cand
				r.BoundCred = &c // bind-on-success
				authedKind = cand.Kind
				emit(string(cand.Kind)+"_success", string(cand.Kind), "success", out.Detail)
				emit("credential_bound", string(cand.Kind), "success", "")
				break
			}
			emit(string(cand.Kind)+"_failed", string(cand.Kind), "failed", out.Category)
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
			// VMware/ESXi, wireless controllers, CUCM/Alcatel voice — vendor web markers.
			ev = append(ev, classify.WebVendorMarkers(r.Probe.HTTPServer, r.Probe.Hints["http_title"], r.Probe.Hints["http_body"])...)
		}
		if b := r.Probe.Hints["ssh_banner"]; b != "" {
			ev = append(ev, classify.SSHBanner(b)...)
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

	// Step 5c: Vendor-fingerprint override. The fingerprint library (operator-defined
	// ∪ built-in) is matched against the SNMP/HTTP/SSH evidence. Match() ranks exact
	// sysObjectID > sysDescr/sysName regex > generic prefix, so a PRODUCT fingerprint
	// (e.g. ExtremeCloud IQ Controller, OID .1916.2.284) overrides the generic Extreme
	// enterprise-prefix "switch" + the driver. The category is overridden only when the
	// fingerprint is at least as confident as the current classification; a specific
	// match (≥85) also supplies the canonical vendor + product model.
	applyFingerprints(&r, cfg.Fingerprints)

	if c := string(r.Match.Category); c != "" {
		emit("classification_updated", "", c, strconv.Itoa(r.Match.Confidence))
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

// applyFingerprints runs the fingerprint library against the host's evidence and,
// on a match, overrides the category (when at least as confident as the current
// classification) and sets the canonical vendor + product model (specific matches
// only). Pure precedence lives in fingerprint.Match (exact OID > sysDescr/sysName
// > prefix; operator prints passed first win ties).
func applyFingerprints(r *HostResult, lib []fingerprint.Print) {
	if len(lib) == 0 {
		return
	}
	ev := fingerprint.Evidence{
		SysObjectID: r.Probe.SNMPSysObjectID,
		SysDescr:    r.Probe.SNMPSysDescr,
		SysName:     r.Probe.SNMPSysName,
		HTTPServer:  r.Probe.HTTPServer,
		SSHBanner:   r.Probe.Hints["ssh_banner"],
		Ports:       r.OpenPorts,
	}
	results := fingerprint.Match(ev, lib)
	if len(results) == 0 {
		return
	}
	top := results[0]
	if cat := fingerprintCategory(top.DeviceType); cat != "" && top.Confidence >= r.Match.Confidence {
		r.Match = driver.Match{Category: cat, Confidence: top.Confidence}
	}
	// A strong/specific match is authoritative for vendor + product model, so we
	// record "Extreme Networks / VE6120 Medium" instead of only the firmware string.
	// Model precedence: the winning fingerprint's EXPLICIT model wins; otherwise the
	// model is derived from the sysDescr (the VE6120 built-in path).
	if top.Confidence >= 85 {
		if top.Vendor != "" {
			r.Vendor = top.Vendor
		}
		if top.Model != "" {
			r.Model = top.Model
		} else if m := fingerprint.ModelFromSysDescr(r.Probe.SNMPSysDescr); m != "" {
			r.Model = m
		}
	}
}

// fingerprintCategory maps a fingerprint device_type token to a domain device
// category. An empty/unknown token returns "" (no category override). Delegates
// to fingerprint.CanonicalCategory so discovery + the API reclassify path agree.
func fingerprintCategory(dt string) domain.DeviceCategory {
	return domain.DeviceCategory(fingerprint.CanonicalCategory(dt))
}

// intsCSV renders a port list as "22,443,8443" for an event message.
func intsCSV(xs []int) string {
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = strconv.Itoa(x)
	}
	return strings.Join(parts, ",")
}

// snmpAttempt records one SNMP credential probe outcome for credential-test
// history. Non-secret reason strings only.
func snmpAttempt(cand credresolver.CredRef, ok bool) CredAttempt {
	cat, detail := "auth_failed", "no SNMP response (wrong community or no access)"
	if ok {
		cat, detail = "success", "authenticated"
	}
	return CredAttempt{CredentialID: cand.ID, Kind: cand.Kind, Protocol: "snmp", Success: ok, Category: cat, Detail: detail, Relevant: true}
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
