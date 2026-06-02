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
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/coralsearesorts/hims/internal/credresolver"
	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/driver"
	"github.com/coralsearesorts/hims/internal/driver/aruba"
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
	Community string // for SNMP v2c
	Weak      bool
}

// DecryptFn decrypts a credential blob. The pipeline calls it only on the
// credentials it's about to try — not the entire set.
type DecryptFn func(ctx context.Context, credID uuid.UUID) (DecryptedCred, error)

// PipelineConfig wires the pipeline dependencies.
type PipelineConfig struct {
	Registry *driver.Registry
	Fetcher  CandidateFetcher
	Decrypt  DecryptFn
	// Timeout for each per-host step.
	PingTimeout time.Duration
	SNMPTimeout time.Duration
}

// Run runs the pipeline for a single IP and returns its result.
// All steps are attempted; errors within optional steps (e.g. deep collect)
// are recorded in result.Error but do not abort the pipeline.
func Run(ctx context.Context, ip netip.Addr, locationID *uuid.UUID, cfg PipelineConfig) HostResult { //nolint:gocritic
	r := HostResult{IP: ip}

	// Step 1: Is the host alive?
	if !ping(ctx, ip, cfg.PingTimeout) {
		return r // not alive — nothing to collect
	}
	r.Alive = true

	// Step 2: Light port scan (most-common management ports for switches).
	r.OpenPorts = scanPorts(ctx, ip, []int{22, 23, 80, 161, 443, 8080})

	// Step 3: SNMP light probe for sysDescr + sysObjectID (cheap, uses port 161).
	var community string
	r.Probe = driver.Probe{
		IP:           ip,
		OpenTCPPorts: r.OpenPorts,
	}
	if hasPort(r.OpenPorts, 161) || true { // always try SNMP even when port isn't in scan
		community, r.Probe.SNMPSysDescr, r.Probe.SNMPSysObjectID = lightSNMPProbe(ctx, ip, cfg.SNMPTimeout)
	}

	// Step 4: Driver classification.
	r.MatchedDrv, r.Match = cfg.Registry.Best(r.Probe)
	if r.MatchedDrv == nil {
		return r // unrecognized device — enrolled as "unknown" by the caller
	}

	// Step 5: Credential resolution + authenticated probe (SNMP bind-on-success).
	groups, err := cfg.Fetcher.CredentialCandidates(ctx, ip, locationID)
	if err != nil {
		r.Error = fmt.Errorf("fetch credentials: %w", err)
		return r
	}
	fp := credresolver.Fingerprint{SNMP: community != "" || hasPort(r.OpenPorts, 161)}
	candidates := credresolver.Resolve(credresolver.Input{Fingerprint: fp, Groups: groups})

	var authSess driver.Session
	for _, cand := range candidates {
		if cand.Kind != domain.CredSNMPv2c && cand.Kind != domain.CredSNMPv3 {
			continue
		}
		dec, err := cfg.Decrypt(ctx, cand.ID)
		if err != nil {
			continue
		}
		tgt := snmp.Target{
			Addr:      ip,
			Version:   snmp.V2c,
			Community: dec.Community,
			Timeout:   cfg.SNMPTimeout,
		}
		cli, err := snmp.NewClient(tgt)
		if err != nil {
			continue
		}
		if err := cli.Connect(ctx); err != nil {
			_ = cli.Close()
			continue
		}
		// Verify with a quick Get — if it succeeds, we have a working credential.
		pdus, err := cli.Get(ctx, "1.3.6.1.2.1.1.1.0")
		if err != nil || len(pdus) == 0 {
			_ = cli.Close()
			continue
		}
		// Success — bind this credential.
		c := cand
		r.BoundCred = &c
		authSess = &aruba.Session{Client: cli, Ctx: ctx}
		break
	}

	if authSess == nil {
		return r // could not authenticate — enrolled with no facts
	}

	// Step 6: Deep collection.
	if col, ok := r.MatchedDrv.(driver.Collector); ok {
		facts, err := col.Collect(authSess, r.Probe)
		if err == nil {
			r.Facts = &facts
		} else {
			r.Error = err
		}
	}
	return r
}

// --- Transport helpers --------------------------------------------------------

func ping(ctx context.Context, ip netip.Addr, timeout time.Duration) bool {
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	conn, err := net.DialTimeout("ip4:icmp", ip.String(), timeout)
	if err == nil {
		_ = conn.Close()
		return true
	}
	// Fallback: try TCP port 22 or 161 to confirm "alive" without raw ICMP.
	tctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	d := &net.Dialer{}
	for _, port := range []string{"22", "161", "80"} {
		c, err := d.DialContext(tctx, "tcp", ip.String()+":"+port)
		if err == nil {
			_ = c.Close()
			return true
		}
	}
	return false
}

func scanPorts(ctx context.Context, ip netip.Addr, ports []int) []int {
	open := make([]int, 0, len(ports))
	d := &net.Dialer{}
	for _, port := range ports {
		tctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
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

func hasPort(ports []int, port int) bool {
	for _, p := range ports {
		if p == port {
			return true
		}
	}
	return false
}

func lightSNMPProbe(ctx context.Context, ip netip.Addr, timeout time.Duration) (community, sysDescr, sysOID string) {
	for _, comm := range []string{"public", "private"} {
		tgt := snmp.Target{Addr: ip, Version: snmp.V2c, Community: comm, Timeout: timeout}
		cli, err := snmp.NewClient(tgt)
		if err != nil {
			continue
		}
		if err := cli.Connect(ctx); err != nil {
			continue
		}
		pdus, err := cli.Get(ctx, "1.3.6.1.2.1.1.1.0", "1.3.6.1.2.1.1.2.0")
		_ = cli.Close()
		if err != nil || len(pdus) < 2 {
			continue
		}
		sysDescr = snmp.PDUString(pdus[0])
		sysOID = snmp.PDUString(pdus[1])
		if sysDescr != "" || sysOID != "" {
			community = comm
			return
		}
	}
	return
}

// errNoAuth is a sentinel for "no credential succeeded".
var errNoAuth = errors.New("discovery: no credential authenticated")

// ScopeRange is a sequence of IPs to scan; the engine generates them from a
// CIDR or an explicit list.
type ScopeRange struct {
	IPs        []netip.Addr
	LocationID *uuid.UUID // nil for single-IP without a location
}
