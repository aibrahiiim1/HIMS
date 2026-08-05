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
	"github.com/coralsearesorts/hims/internal/isapi"
	"github.com/coralsearesorts/hims/internal/omnipcx"
	"github.com/coralsearesorts/hims/internal/redfish"
	"github.com/coralsearesorts/hims/internal/snmp"
	"github.com/coralsearesorts/hims/internal/telnet"
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
	// Source attributes WHY this credential was tried, for the credential-test
	// history: "subnet" (the IP's site subnet has assigned credentials, so only
	// those were tried), or "default" (normal global/scope resolution). Manual
	// per-device collects record "manual" outside the pipeline.
	Source string
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
		HTTP:  anyWebPort(ports),
		LDAP:  hasPortN(ports, 389) || hasPortN(ports, 636),
		CLI:   hasPortN(ports, 23), // telnet-only devices (Alcatel OmniPCX)
	}
}

// portAllowsProto gates a non-SNMP credential test to a host that actually
// exposes that protocol's port (don't WinRM-probe a host with no 5985 open).
func portAllowsProto(ports []int, kind domain.CredentialKind) bool {
	switch kind {
	case domain.CredSSH:
		return hasPortN(ports, 22)
	case domain.CredWinRM, domain.CredWindows:
		return hasPortN(ports, 5985) || hasPortN(ports, 5986)
	case domain.CredONVIF, domain.CredHTTPBasic, domain.CredVendorAPI:
		return anyWebPort(ports)
	}
	return false
}

// provisionalCategory is the best-effort category for a host that authenticated
// via a non-SNMP credential during the scan, before a deep OS collection refines
// it. Windows→endpoint and Linux(SSH)→server are then corrected by Collect OS
// using the real OS caption; ONVIF→camera is already definitive.
func provisionalCategory(kind domain.CredentialKind) domain.DeviceCategory {
	switch kind {
	case domain.CredWinRM, domain.CredWindows:
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
	// CredScope, when non-empty, is the human label of the site subnet whose
	// assigned credentials were the EXCLUSIVE set tried for this IP (e.g.
	// "CCTV Cameras 172.21.210.0/24"). Empty ⇒ normal/global resolution was used.
	CredScope string
	// Plan is the expected-protocol decision made before credential testing.
	Plan  ProtocolPlan
	Facts *driver.Facts
	// Vendor/Model from a matched fingerprint (canonical vendor + product model),
	// applied over the driver's generic identity when a specific fingerprint hits.
	Vendor string
	Model  string
	// Serial is a REAL identity serial learned from a safe unauthenticated probe
	// (e.g. a Dell iDRAC's Redfish ServiceTag). Empty ⇒ leave any existing serial
	// untouched — a weak scan never wipes a proven serial.
	Serial string
	// Subtype is a discovery-refined device subtype within the category (e.g.
	// "alcatel_omnipcx" for a detected OmniPCX). Empty = leave the subtype untouched.
	Subtype string
	// Classification is the Phase-3 explainable-classification record: the evidence
	// channels, the fingerprint(s) that won, and the candidates that were considered
	// but rejected (with reasons). Nil until applyFingerprints runs. Persisted into
	// discovery_results.probe_data so the UI can show WHY a device was classified the
	// way it was — and, for unknowns, what it most likely is.
	Classification *ClassificationDetail
	Error          error
}

// ClassificationDetail captures the "why" behind a device's category for the
// evidence panel. Winners are the fingerprints that survived (highest first);
// Rejected lists candidates that matched a positive pattern but lost — either
// suppressed by an exclusion (e.g. HP JetDirect excluded from the Aruba switch
// rule) or outranked by a more confident different-category match. LikelyType is
// the best guess for an otherwise-unknown device (the top runner-up category).
type ClassificationDetail struct {
	Evidence    fingerprint.Evidence   `json:"evidence"`
	FinalSource string                 `json:"final_source"` // "fingerprint" | "driver" | "plan" | "none"
	Winners     []fingerprint.Result   `json:"winners,omitempty"`
	Rejected    []fingerprint.Rejected `json:"rejected,omitempty"`
	LikelyType  string                 `json:"likely_type,omitempty"`
}

// CandidateFetcher abstracts the DB call that assembles credentials for an IP.
type CandidateFetcher interface {
	CredentialCandidates(ctx context.Context, ip netip.Addr, locationID *uuid.UUID) ([]credresolver.ScopedGroup, error)
	// SubnetScopedCredentials returns the EXCLUSIVE credential set for the IP's
	// site subnet (when that subnet has assignments) plus a human label for
	// reporting. Empty slice ⇒ no subnet scoping ⇒ normal resolution applies.
	SubnetScopedCredentials(ctx context.Context, ip netip.Addr, locationID *uuid.UUID) ([]credresolver.CredRef, string, error)
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
	// ExplicitCreds reports that ExtraGroups represents an EXPLICIT operator
	// credential selection for this scan (not the implicit "all stored" default).
	// When set, that selection wins over any standing subnet assignment: it
	// becomes the EXCLUSIVE candidate set for every host and the subnet-scoped set
	// is ignored. This mirrors per-device Collect, where an operator-chosen
	// credential overrides subnet scope. Anti-spray still holds — only the
	// explicitly chosen credentials are tried, never the whole global list.
	ExplicitCreds bool
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
// finalizeCredSource stamps the credential-test source ("subnet" | "default")
// on every attempt that didn't already carry one, so the history can report why
// each credential was tried (subnet-scoped vs normal resolution).
// flattenGroups collapses scoped groups into a de-duplicated, order-preserving
// CredRef slice — used to turn an explicit operator selection (ExtraGroups) into
// the resolver's Exclusive set.
func flattenGroups(groups []credresolver.ScopedGroup) []credresolver.CredRef {
	var out []credresolver.CredRef
	seen := map[uuid.UUID]bool{}
	for _, g := range groups {
		for _, m := range g.Members {
			if seen[m.ID] {
				continue
			}
			seen[m.ID] = true
			out = append(out, m)
		}
	}
	return out
}

func finalizeCredSource(r HostResult, source string) HostResult {
	for i := range r.CredAttempts {
		if r.CredAttempts[i].Source == "" {
			r.CredAttempts[i].Source = source
		}
	}
	return r
}

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
	// 902 = VMware ESXi vpxa/authd host-agent port — near-unique to ESXi, so probing it lets
	// classification detect ESXi (→ virtual_host) even when the host's web banner does not
	// advertise vmware/esxi, which is what routes vSphere/vendor_api collection automatically.
	ports := append([]int(nil), StandardScanPorts...)
	// Hikvision/CCTV convention: a recorder/camera's web/ISAPI port is commonly
	// 8000 + the host's last octet (.2 -> 8002, .15 -> 8015). Probe it per-host so
	// these recorders are discovered automatically — the operator never has to
	// hand-add each port to the web-port settings.
	if u := ip.Unmap(); u.Is4() {
		if octet := int(u.As4()[3]); octet >= 1 && octet <= 255 {
			ports = append(ports, 8000+octet)
		}
	}
	for _, p := range cfg.ExtraPorts { // targeted-retry: a missed known device's last-known open ports
		if p > 0 && p < 65536 {
			ports = append(ports, p)
		}
	}
	r.OpenPorts = scanPorts(ctx, ip, dedupInts(ports), cfg.PortTimeout)
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
	// Subnet-scoped credentials: if this IP's site subnet has assigned
	// credentials, they become the EXCLUSIVE set — the resolver discards every
	// global/scope/operator group above. This is the anti-spray / lockout-safety
	// lever (a CCTV subnet is never sprayed with Windows/switch credentials).
	exclusive, scopeLabel, serr := cfg.Fetcher.SubnetScopedCredentials(ctx, ip, locationID)
	if serr != nil && r.Error == nil {
		r.Error = fmt.Errorf("subnet-scoped credentials: %w", serr)
	}
	credSource := "default"
	switch {
	case cfg.ExplicitCreds && len(cfg.ExtraGroups) > 0:
		// Explicit operator selection wins over any standing subnet assignment:
		// the selected credentials ARE the exclusive set (only those are tried,
		// fingerprint-filtered), and the subnet-scoped set is ignored for this
		// scan. Without this, a subnet that has assigned credentials would
		// override the operator's per-scan choice — surfacing as "I selected
		// these creds but the scan applied other ones."
		exclusive = flattenGroups(cfg.ExtraGroups)
		scopeLabel = ""
		credSource = "selected"
		emit("credential_scope", "", "selected", "operator-selected credentials")
	case len(exclusive) > 0:
		r.CredScope = scopeLabel
		credSource = "subnet"
		emit("credential_scope", "", "subnet", scopeLabel)
	}
	candidates := credresolver.Resolve(credresolver.Input{
		Fingerprint: fingerprintFromPorts(r.OpenPorts), Groups: groups, Exclusive: exclusive,
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
	// Telnet pre-login banner (unauthenticated) — legacy CLI devices print a vendor
	// banner before the login prompt. Used to honestly detect Alcatel OmniPCX (telnet-only).
	if hasPortN(r.OpenPorts, 23) {
		if tb := grabTelnetBanner(ctx, ip, cfg.PortTimeout); tb != "" {
			if r.Probe.Hints == nil {
				r.Probe.Hints = map[string]string{}
			}
			r.Probe.Hints["telnet_banner"] = tb
		}
	}

	// Step 2b-redfish: UNAUTHENTICATED Redfish ServiceRoot probe. Every DMTF BMC
	// (HPE iLO / Dell iDRAC / Lenovo XClarity / Supermicro) serves /redfish/v1/
	// without a credential, exposing Vendor/Product (and, on Dell, the ServiceTag
	// serial) — while Systems/Chassis stay behind Basic auth. This is the honest way
	// to detect an out-of-band controller (and which vendor) when its web UI banner
	// is generic (e.g. an iDRAC behind "Server: Apache"), instead of a generic-web
	// guess. It NEVER authenticates, so full inventory stays gated behind a real
	// Redfish credential. Gated to 443/80 so it adds at most one cheap GET.
	if hasPortN(r.OpenPorts, 443) || hasPortN(r.OpenPorts, 80) {
		probeRedfish(ctx, ip, &r, cfg.PortTimeout)
	}
	// Step 2b-qnap: one cheap GET of the QNAP QTS authLogin.cgi. Every QNAP NAS serves a
	// <QDocRoot> XML there even pre-login — a definitive marker the generic "/" banner
	// misses (QTS root is a JS redirect). On a hit it seeds the http_body marker so the
	// host classifies as storage/QNAP instead of a generic SSH-server. Never authenticates.
	if httpPort(r.OpenPorts) {
		probeQNAP(ctx, ip, &r, cfg.PortTimeout)
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
		ok := probe(tgt)
		if !ok && cand.Kind == domain.CredSNMPv2c {
			// SNMPv1 fallback: many printers and older devices answer ONLY SNMPv1
			// (e.g. "Use SNMPv1: On, Community: public"). The community string is
			// version-agnostic, so retry it as v1 before recording a failure —
			// otherwise these stay SNMP-dead and unmanaged despite SNMP being enabled.
			ok = probe(snmp.Target{Addr: ip, Version: snmp.V1, Community: dec.Community, Timeout: cfg.SNMPTimeout})
		}
		if ok {
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
			// SNMPv1 fallback (same community) for v1-only devices like printers.
			if probe(snmp.Target{Addr: ip, Version: snmp.V1, Community: comm, Timeout: cfg.SNMPTimeout}) {
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
		// Try the host's EXPECTED-protocol credentials before opportunistic ones.
		// Without this, a Linux/appliance host (expected SSH) could spend its whole
		// per-host budget on relevant-but-secondary HTTP_basic creds and never reach
		// the working SSH credential — the exact reason a full-subnet scan failed to
		// manage a host that a single-IP scan (working cred tried first) managed.
		// Stable sort preserves the resolver's within-kind ordering.
		ordered := append(candidates[:0:0], candidates...)
		sort.SliceStable(ordered, func(i, j int) bool {
			return plan.Expects(ordered[i].Kind) && !plan.Expects(ordered[j].Kind)
		})
		for _, cand := range ordered {
			if cand.Kind == domain.CredSNMPv2c || cand.Kind == domain.CredSNMPv3 {
				continue
			}
			if cand.Kind == domain.CredCLI {
				continue // telnet CLI is handled by the OmniPCX step below (not a credtest proto)
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
			out := credtest.Test(ctx, string(cand.Kind), dec.Community, ip.String(), credtest.Options{WebPorts: webPortsOf(r.OpenPorts)})
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

	// Step 3d: Alcatel OmniPCX (telnet-only). Detect it HONESTLY from its unauthenticated
	// pre-login banner; if a scoped CLI credential authenticates over the OmniPCX telnet
	// protocol, bind it + capture real software identity → managed pbx. Banner-only (no working
	// credential) = credential_required; an attempted-but-rejected CLI cred = credential_failed.
	// A Windows host (RPC/SMB/RDP/WinRM) that also shows an OmniPCX telnet banner is the
	// OmniPCX MANAGEMENT / OmniVista console PC, NOT the PBX — OmniPCX Enterprise runs on a
	// dedicated Linux appliance, never Windows. Don't let the banner override the Windows
	// classification (which strands the PC on voice onboarding with no OS collection). The
	// real telnet-only appliance has none of these ports, so it still classifies as pbx.
	winMgmtHost := hasPortN(r.OpenPorts, 135) || hasPortN(r.OpenPorts, 445) || hasPortN(r.OpenPorts, 3389) || hasPortN(r.OpenPorts, 5985) || hasPortN(r.OpenPorts, 5986)
	if hasPortN(r.OpenPorts, 23) && strings.Contains(strings.ToLower(r.Probe.Hints["telnet_banner"]), "omnipcx") && !winMgmtHost {
		// Honest detection from the banner alone — even with no working credential.
		r.Match = driver.Match{Category: domain.CatPBX, Confidence: 80}
		r.Vendor, r.Model, r.Subtype = "Alcatel-Lucent", "OmniPCX Enterprise", "alcatel_omnipcx"
		if r.BoundCred == nil {
			for _, cand := range candidates {
				if cand.Kind != domain.CredCLI {
					continue
				}
				dec, derr := cfg.Decrypt(ctx, cand.ID)
				if derr != nil || dec.Community == "" {
					continue
				}
				emit("omnipcx_attempt_started", "omnipcx", "started", "")
				u, p := credtest.SplitUserPass(dec.Community)
				id := omnipcx.Probe(ctx, ip.String(), 23, u, p)
				att := CredAttempt{CredentialID: cand.ID, Kind: cand.Kind, Protocol: "omnipcx", Relevant: true}
				if id.Connected {
					att.Success, att.Category, att.Detail = true, "success", "OmniPCX "+id.OSVersion()
					r.CredAttempts = append(r.CredAttempts, att)
					c := cand
					r.BoundCred = &c
					r.Vendor, r.Model = id.Vendor, id.Model
					if r.Facts == nil {
						r.Facts = &driver.Facts{KV: map[string]string{}}
					}
					r.Facts.Vendor, r.Facts.Model, r.Facts.OSVersion = id.Vendor, id.Model, id.OSVersion()
					if id.CPURole != "" {
						r.Facts.KV["omnipcx.cpu_role"] = id.CPURole
					}
					emit("omnipcx_success", "omnipcx", "success", id.OSVersion())
					break
				}
				att.Category = "auth_failed"
				if strings.HasPrefix(id.Reason, "unreachable") {
					att.Category = "unreachable"
				} else if strings.HasPrefix(id.Reason, "protocol") {
					att.Category = "unsupported"
				}
				att.Detail = id.Reason
				r.CredAttempts = append(r.CredAttempts, att)
				emit("omnipcx_failed", "omnipcx", "failed", att.Category)
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
		return finalizeCredSource(r, credSource)
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
		// The auth-provisional category is a FALLBACK — it must not clobber a stronger
		// evidence-based classification. A QNAP NAS answers SSH (→ generic server, 60), but a
		// definitive QDocRoot/NFS storage classification (≥60) is the truth; only fill in when
		// the current classification is weaker/absent.
		if cat := provisionalCategory(authedKind); cat != domain.CatUnknown && r.Match.Confidence < 60 {
			r.Match = driver.Match{Category: cat, Confidence: 60}
		}
	}

	// Camera-by-port-shape: the protocol plan inferred camera/recorder purely from
	// the Hikvision "8000 + host octet" web/ISAPI port (no other service, often a
	// generic banner). When nothing else set a category, adopt that hint so the
	// orchestrator routes the host to ONVIF/ISAPI onboarding (with subnet-scoped
	// web creds) instead of leaving it unknown + SNMP-only. ISAPI deviceInfo then
	// confirms camera vs nvr/dvr at high confidence on a successful collect.
	if (r.Match.Category == "" || r.Match.Category == domain.CatUnknown) && r.Plan.Candidate == "camera" {
		r.Match = driver.Match{Category: domain.CatCamera, Confidence: 40}
	}

	// Step 5c: Vendor-fingerprint override. The fingerprint library (operator-defined
	// ∪ built-in) is matched against the SNMP/HTTP/SSH evidence. Match() ranks exact
	// sysObjectID > sysDescr/sysName regex > generic prefix, so a PRODUCT fingerprint
	// (e.g. ExtremeCloud IQ Controller, OID .1916.2.284) overrides the generic Extreme
	// enterprise-prefix "switch" + the driver. The category is overridden only when the
	// fingerprint is at least as confident as the current classification; a specific
	// match (≥85) also supplies the canonical vendor + product model.
	applyFingerprints(&r, cfg.Fingerprints)

	// Redfish ServiceRoot evidence is authoritative for "this is an out-of-band
	// controller" — apply it AFTER the driver/fingerprint passes so a generic
	// web-port "server" guess doesn't bury a real iLO/iDRAC.
	applyRedfishClassification(&r)

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
	return finalizeCredSource(r, credSource)
}

// applyFingerprints runs the fingerprint library against the host's evidence and,
// on a match, overrides the category (when at least as confident as the current
// classification) and sets the canonical vendor + product model (specific matches
// only). Pure precedence lives in fingerprint.Match (exact OID > sysDescr/sysName
// > prefix; operator prints passed first win ties).
func applyFingerprints(r *HostResult, lib []fingerprint.Print) {
	ev := fingerprint.Evidence{
		SysObjectID: r.Probe.SNMPSysObjectID,
		SysDescr:    r.Probe.SNMPSysDescr,
		SysName:     r.Probe.SNMPSysName,
		HTTPServer:  r.Probe.HTTPServer,
		SSHBanner:   r.Probe.Hints["ssh_banner"],
		Ports:       r.OpenPorts,
	}
	// Record evidence even when the library is empty / nothing matches, so the
	// evidence panel can explain unknowns (what we saw + why no rule fired).
	detail := &ClassificationDetail{Evidence: ev, FinalSource: classificationSource(r)}
	r.Classification = detail
	if len(lib) == 0 {
		return
	}
	results, rejected := fingerprint.MatchWithRejected(ev, lib)
	detail.Winners = results
	detail.Rejected = rejected
	if len(results) == 0 {
		// No positive match. If a candidate was rejected only by exclusion/confidence,
		// surface the best guess for the unknown bucket (top rejected device type).
		detail.LikelyType = likelyTypeFromRejected(rejected)
		return
	}
	top := results[0]
	if cat := fingerprintCategory(top.DeviceType); cat != "" && top.Confidence >= r.Match.Confidence {
		// Windows-management guard: a host exposing an RPC/SMB/RDP/WinRM surface is a
		// Windows PC. A bare PORT fingerprint that reads as voice (e.g. a stale
		// 5060→voip → pbx rule) must NOT override that — a softphone on a workstation,
		// or the OmniVista management console, is NOT a PBX/phone. Mirrors the winMgmt
		// guards in classify.OpenPorts (SIP) and the OmniPCX telnet classifier. This is
		// the 150.0.0.132 regression lock: a real voice appliance is fingerprinted by
		// OID/service/sysDescr (not a bare port) and has none of these Windows ports.
		voice := cat == domain.CatPBX || cat == domain.CatIPPhone || cat == domain.CatVoiceGateway
		winMgmt := hasPortN(r.OpenPorts, 135) || hasPortN(r.OpenPorts, 445) || hasPortN(r.OpenPorts, 3389) || hasPortN(r.OpenPorts, 5985) || hasPortN(r.OpenPorts, 5986)
		if voice && top.Kind == fingerprint.KindPort && winMgmt {
			detail.LikelyType = string(cat) // record the suppressed guess for the evidence panel
		} else {
			r.Match = driver.Match{Category: cat, Confidence: top.Confidence}
			detail.FinalSource = "fingerprint"
		}
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

// classificationSource names what set the current category BEFORE fingerprints
// run, for the evidence panel's provenance line. A driver match beats a bare
// protocol-plan guess; an empty/unknown category is "none".
func classificationSource(r *HostResult) string {
	if r.MatchedDrv != nil {
		return "driver"
	}
	if r.Match.Category != "" && r.Match.Category != domain.CatUnknown {
		return "plan"
	}
	return "none"
}

// likelyTypeFromRejected returns the highest-confidence rejected candidate's
// device type — the system's best guess for a device that ended up unknown.
func likelyTypeFromRejected(rejected []fingerprint.Rejected) string {
	best := ""
	bestConf := -1
	for _, rj := range rejected {
		if rj.DeviceType != "" && rj.Confidence > bestConf {
			best, bestConf = rj.DeviceType, rj.Confidence
		}
	}
	return best
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

// grabTelnetBanner reads the unauthenticated pre-login banner from telnet/23 (decoding IAC),
// up to the login prompt. Legacy CLI devices print a vendor banner here (e.g. Alcatel OmniPCX:
// "Welcome to … / Alcatel OmniPCX Enterprise / login:"). No credentials are sent.
func grabTelnetBanner(ctx context.Context, ip netip.Addr, timeout time.Duration) string {
	if timeout <= 0 {
		timeout = 1500 * time.Millisecond
	}
	dctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	c, err := telnet.Dial(dctx, net.JoinHostPort(ip.String(), "23"), timeout)
	if err != nil {
		return ""
	}
	defer c.Close()
	banner, _ := c.ReadUntil(3*time.Second, "login:", "ogin:", "username:", "password:")
	return strings.TrimSpace(banner)
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

// probeRedfish runs the UNAUTHENTICATED Redfish ServiceRoot probe and, on a hit,
// records the controller identity as evidence + classifies the host as a 'bmc'.
// It writes redfish.* hints (consumed by Apply → device facts and the BMC
// inventory's honest redfish_status) and sets the canonical vendor / controller
// subtype / real serial (Dell ServiceTag). It NEVER authenticates and NEVER marks
// Redfish collected — full inventory still requires a real Redfish credential.
// probeQNAP GETs the QNAP QTS authLogin.cgi across the host's open web ports and, on the
// <QDocRoot> marker every QNAP serves, seeds the http_body hint so WebVendorMarkers
// classifies it as storage/QNAP. One small unauthenticated GET per web port; no secrets.
func probeQNAP(ctx context.Context, ip netip.Addr, r *HostResult, timeout time.Duration) {
	if timeout < 3*time.Second {
		timeout = 3 * time.Second
	}
	host := ip.String()
	if ip.Is6() {
		host = "[" + host + "]"
	}
	var bases []string
	for _, p := range []int{8080, 80} {
		if hasPortN(r.OpenPorts, p) {
			bases = append(bases, fmt.Sprintf("http://%s:%d", host, p))
		}
	}
	for _, p := range []int{443, 8443} {
		if hasPortN(r.OpenPorts, p) {
			bases = append(bases, fmt.Sprintf("https://%s:%d", host, p))
		}
	}
	doer := isapi.PermissiveClient(timeout)
	for _, base := range bases {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/cgi-bin/authLogin.cgi", nil)
		if err != nil {
			continue
		}
		resp, err := doer.Do(req)
		if err != nil {
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		if strings.Contains(string(body), "QDocRoot") {
			if r.Probe.Hints == nil {
				r.Probe.Hints = map[string]string{}
			}
			r.Probe.Hints["http_body"] = strings.TrimSpace(r.Probe.Hints["http_body"] + " QDocRoot qnap-qts")
			return
		}
	}
}

func probeRedfish(ctx context.Context, ip netip.Addr, r *HostResult, timeout time.Duration) {
	// A BMC's ServiceRoot is a single small GET, but its management NIC + legacy-TLS
	// handshake can be slow — give it a floor of 5s so a real iLO/iDRAC isn't missed
	// on a borderline handshake (which would leave a controller mis-identified).
	if timeout < 5*time.Second {
		timeout = 5 * time.Second
	}
	doer := isapi.PermissiveClient(timeout) // legacy/self-signed BMC TLS
	var info redfish.ServiceRootInfo
	for _, base := range redfishBaseURLs(ip, r.OpenPorts) {
		if info = redfish.ProbeServiceRoot(ctx, base, doer); info.Reachable {
			break
		}
		// One retry per base: the first TLS handshake to a BMC can time out cold.
		if info = redfish.ProbeServiceRoot(ctx, base, doer); info.Reachable {
			break
		}
	}
	if !info.Reachable {
		return
	}
	if r.Probe.Hints == nil {
		r.Probe.Hints = map[string]string{}
	}
	setHint := func(k, v string) {
		if strings.TrimSpace(v) != "" {
			r.Probe.Hints[k] = v
		}
	}
	r.Probe.Hints["redfish.reachable"] = "true"
	setHint("redfish.vendor", info.Vendor)
	setHint("redfish.product", info.Product)
	setHint("redfish.version", info.RedfishVersion)
	setHint("redfish.controller", info.ControllerKind)
	setHint("redfish.servicetag", info.ServiceTag)

	// Identity from the ServiceRoot (vendor / controller subtype / Dell ServiceTag serial).
	// The CATEGORY override is applied later by applyRedfishClassification — AFTER the
	// driver-registry + fingerprint passes, which would otherwise reassign r.Match.
	if info.Vendor != "" {
		r.Vendor = info.Vendor
	}
	if info.ControllerKind != "" {
		r.Subtype = strings.ToLower(strings.NewReplacer(" ", "_", "-", "_").Replace(info.ControllerKind))
	}
	if info.ServiceTag != "" { // Dell ServiceTag is the chassis serial — real, unauthenticated
		r.Serial = info.ServiceTag
	}
}

// applyRedfishClassification promotes a host with a reachable Redfish ServiceRoot to
// the 'bmc' category. A Redfish service at /redfish/v1/ IS the out-of-band controller
// (an iLO/iDRAC on its own management IP) — definitive evidence that outranks the
// generic "open web port ⇒ server" guess. Runs AFTER the driver/fingerprint passes
// (which reassign r.Match) so the controller identity survives. Confidence 88 matches
// the Dell-iDRAC SNMP-OID fingerprint; it never overrides a stronger (≥88) specific
// match. Reconcile still honors an operator classification lock downstream.
func applyRedfishClassification(r *HostResult) {
	if r.Probe.Hints["redfish.reachable"] != "true" {
		return
	}
	if r.Match.Category == "" || r.Match.Category == domain.CatUnknown || r.Match.Confidence < 88 {
		r.Match = driver.Match{Category: domain.CatBMC, Confidence: 88}
	}
}

// redfishBaseURLs returns the scheme://host[:port] bases to try for the Redfish
// ServiceRoot, https first (the BMC norm), http only if 443 is closed.
func redfishBaseURLs(ip netip.Addr, ports []int) []string {
	host := ip.String()
	if ip.Is6() {
		host = "[" + host + "]"
	}
	var bases []string
	if hasPortN(ports, 443) {
		bases = append(bases, "https://"+host)
	}
	if hasPortN(ports, 80) {
		bases = append(bases, "http://"+host)
	}
	return bases
}

// httpPort reports whether any web/management port is open (incl. the Hikvision
// 8000+octet range), gating whether a banner grab runs at all.
func httpPort(ports []int) bool {
	return anyWebPort(ports)
}

// httpBanner GETs the open web port(s) and returns the Server header, the page
// <title>, and a lowercased body snippet. It probes up to 3 open web ports in
// priority order (443, 8443, 80, 8080, 8000) and MERGES their bodies/titles, so a
// classification marker on a secondary port is still seen — e.g. Cisco CUCM serves
// its "Cisco Unified CM Console" page on 8443 while 443 is also open. TLS uses the
// legacy-permissive client (full cipher set, TLS1.0+) because voice/appliance web
// stacks (CUCM 8443, OmniPCX) won't negotiate with Go's modern defaults — a plain
// client gets "tls: handshake failure" and the banner is lost. Best-effort: errors
// on any single port are skipped.
func httpBanner(ctx context.Context, ip netip.Addr, ports []int, timeout time.Duration) (server, title, body string) {
	if timeout <= 0 {
		timeout = 2 * time.Second
	} else if timeout > 3*time.Second {
		timeout = 3 * time.Second // keep the scan snappy regardless of SNMP timeout
	}
	client := isapi.PermissiveClient(timeout) // legacy ciphers for CUCM/OmniPCX/appliance TLS
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse // don't chase redirects off-host
	}
	type cand struct {
		scheme string
		port   int
	}
	var cands []cand
	for _, c := range []cand{{"https", 443}, {"https", 8443}, {"http", 80}, {"http", 8080}, {"http", 8000}} {
		if hasPort(ports, c.port) {
			cands = append(cands, c)
		}
	}
	// Hikvision-style 8000+octet ports (e.g. 8011) — probe over http so a camera/
	// recorder that exposes ONLY that port still yields a banner for classification.
	for _, p := range ports {
		if p >= 8001 && p <= 8255 && p != 8080 && !hasPort([]int{443, 8443, 80, 8080, 8000}, p) {
			dup := false
			for _, c := range cands {
				if c.port == p {
					dup = true
					break
				}
			}
			if !dup {
				cands = append(cands, cand{"http", p})
			}
		}
	}
	var bodySb strings.Builder
	for tried, c := range cands {
		if tried >= 3 { // cap probes to keep the scan snappy
			break
		}
		url := fmt.Sprintf("%s://%s:%d/", c.scheme, ip, c.port)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		if server == "" {
			server = resp.Header.Get("Server")
		}
		if title == "" {
			if m := titleRe.FindStringSubmatch(string(raw)); len(m) > 1 {
				title = strings.TrimSpace(m[1])
			}
		}
		bodySb.WriteString(strings.ToLower(string(raw)))
		bodySb.WriteByte('\n')
	}
	return server, title, bodySb.String()
}

func hasPort(ports []int, port int) bool {
	for _, p := range ports {
		if p == port {
			return true
		}
	}
	return false
}

// isWebPort reports whether a TCP port is an HTTP/management web surface worth a
// banner grab + an HTTP/ONVIF/ISAPI credential. Beyond the standard web ports it
// recognises the Hikvision-style "8000 + host octet" CCTV/ISAPI convention
// (e.g. .11 -> 8011, .130 -> 8130): many recorders/cameras expose ONLY that port,
// and without recognising it they were read as "no web surface" -> mis-classified
// unknown (SNMP-expected) with their ONVIF/HTTP-Basic credentials filtered out by
// the resolver's fingerprint, even when the subnet was scoped to CCTV creds.
func isWebPort(p int) bool {
	switch p {
	case 80, 443, 8080, 8443:
		return true
	}
	return p >= 8000 && p <= 8255
}

// anyWebPort reports whether any open port is a web/management surface.
func anyWebPort(ports []int) bool {
	for _, p := range ports {
		if isWebPort(p) {
			return true
		}
	}
	return false
}

// dedupInts returns ports with duplicates removed, preserving first-seen order
// (so a configured/derived port that duplicates a base port isn't probed — or
// reported open — twice).
func dedupInts(ports []int) []int {
	seen := make(map[int]bool, len(ports))
	out := ports[:0:0]
	for _, p := range ports {
		if seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// webPortsOf returns the open ports that are web/management surfaces, so the
// http_basic credential test probes the device's actual web port (e.g. 8015 on a
// Hikvision NVR) instead of only :80/:443.
func webPortsOf(ports []int) []int {
	var out []int
	for _, p := range ports {
		if isWebPort(p) {
			out = append(out, p)
		}
	}
	return out
}

// cctvWebPortPattern reports the Hikvision CCTV signature: every open port is in
// the 8000+octet range (8001-8255) and none is the common 8080 web-app port. A
// camera/recorder that exposes ONLY its 8000+octet web/ISAPI port matches; a plain
// 8080 or 8000 web app does not. Used as a camera classification signal so such a
// device is onboarded over ONVIF/ISAPI rather than dropped into the SNMP bucket.
func cctvWebPortPattern(ports []int) bool {
	if len(ports) == 0 {
		return false
	}
	for _, p := range ports {
		if p < 8001 || p > 8255 || p == 8080 {
			return false
		}
	}
	return true
}

// ScopeRange is a sequence of IPs to scan; the engine generates them from a
// CIDR or an explicit list.
type ScopeRange struct {
	IPs        []netip.Addr
	LocationID *uuid.UUID // nil for single-IP without a location
}
