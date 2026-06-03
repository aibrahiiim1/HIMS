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

	"github.com/coralsearesorts/hims/internal/credresolver"
	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/driver"
	"github.com/coralsearesorts/hims/internal/driver/swsnmp"
	"github.com/coralsearesorts/hims/internal/snmp"
)

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
	Facts     *driver.Facts
	Error     error
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

	// Step 1: TCP port scan — management ports for switches/servers plus the
	// service ports the role-inference engine keys on (DNS/DC/DB).
	r.OpenPorts = scanPorts(ctx, ip, []int{22, 23, 53, 80, 88, 161, 389, 443, 1433, 1521, 3389, 5432, 8080}, cfg.PortTimeout)
	r.Probe = driver.Probe{IP: ip, OpenTCPPorts: r.OpenPorts}

	// Step 2: Resolve credential candidates (scope-bound + operator-selected /
	// all-stored, the latter injected by the scan as ExtraGroups).
	groups, ferr := cfg.Fetcher.CredentialCandidates(ctx, ip, locationID)
	if ferr != nil {
		r.Error = fmt.Errorf("fetch credentials: %w", ferr)
	}
	groups = append(groups, cfg.ExtraGroups...)
	candidates := credresolver.Resolve(credresolver.Input{
		Fingerprint: credresolver.Fingerprint{SNMP: true}, Groups: groups,
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
		if probe(tgt) {
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
