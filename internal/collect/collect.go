// Package collect holds the credentialed single-target collection cores —
// Redfish, vSphere, Hyper-V, ONVIF, the wireless controllers (UniFi/Omada/
// Ruckus/Extreme), CUCM voice, and AD import — extracted so BOTH the collector
// CLI (cmd/hims-collector) and the API (operator-launched imports in the web
// UI) drive the exact same logic. There is no duplication: each entry point
// builds Deps and calls these functions.
//
// Credential discipline is unchanged: candidates are resolved through the
// scope/group resolver (Deps.Fetcher) and decrypted only when about to be
// tried (Deps.Decrypt); the first that authenticates is bound on success.
package collect

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
	"github.com/masterzen/winrm"
	"github.com/vmware/govmomi"

	"github.com/coralsearesorts/hims/internal/adimport"
	"github.com/coralsearesorts/hims/internal/apply"
	"github.com/coralsearesorts/hims/internal/credresolver"
	cucm "github.com/coralsearesorts/hims/internal/cucm"
	"github.com/coralsearesorts/hims/internal/discovery"
	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/driver"
	cucmdrv "github.com/coralsearesorts/hims/internal/driver/cucm"
	extremedrv "github.com/coralsearesorts/hims/internal/driver/extreme"
	hypervdrv "github.com/coralsearesorts/hims/internal/driver/hyperv"
	omadadrv "github.com/coralsearesorts/hims/internal/driver/omada"
	onvifdrv "github.com/coralsearesorts/hims/internal/driver/onvif"
	rfdrv "github.com/coralsearesorts/hims/internal/driver/redfish"
	ruckusdrv "github.com/coralsearesorts/hims/internal/driver/ruckus"
	unifidrv "github.com/coralsearesorts/hims/internal/driver/unifi"
	vspheredrv "github.com/coralsearesorts/hims/internal/driver/vsphere"
	ec "github.com/coralsearesorts/hims/internal/extreme"
	oc "github.com/coralsearesorts/hims/internal/omada"
	ov "github.com/coralsearesorts/hims/internal/onvif"
	rf "github.com/coralsearesorts/hims/internal/redfish"
	rc "github.com/coralsearesorts/hims/internal/ruckus"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
	uc "github.com/coralsearesorts/hims/internal/unifi"

	"github.com/google/uuid"
)

// Deps are the shared dependencies a collection needs: the CMDB queries (for
// apply), the driver registry, the credential-candidate fetcher (scope/group
// resolver), and the decrypt function. The CLI and the API each build these.
type Deps struct {
	Queries *db.Queries
	Reg     *driver.Registry
	Fetcher discovery.CandidateFetcher
	Decrypt discovery.DecryptFn
	// HTTPTimeout bounds REST/Redfish/SOAP client calls (Redfish/UniFi/Omada/
	// Ruckus/Extreme/ONVIF/CUCM imports). Zero falls back to 20s.
	HTTPTimeout time.Duration
	// WinRMTimeout bounds the Hyper-V WinRM operation. Zero falls back to 60s.
	WinRMTimeout time.Duration
}

func (d Deps) httpTimeout() time.Duration {
	if d.HTTPTimeout <= 0 {
		return 20 * time.Second
	}
	return d.HTTPTimeout
}

func (d Deps) winrmTimeout() time.Duration {
	if d.WinRMTimeout <= 0 {
		return 60 * time.Second
	}
	return d.WinRMTimeout
}

// Result summarizes one persisted collection.
type Result struct {
	DeviceID uuid.UUID `json:"device_id"`
	Summary  string    `json:"summary"`
}

// ADResult summarizes an AD-import run.
type ADResult struct {
	Found    int `json:"found"`
	Imported int `json:"imported"`
	Skipped  int `json:"skipped"`
}

// ControllerOpts carries the kind-specific parameters a few controllers need.
type ControllerOpts struct {
	OmadaCID    string // Omada controller id (required for omada)
	CUCMVersion string // AXL schema version (cucm); defaults to 12.5
	ExtremeBase string // XIQ API base URL (extreme); optional
}

// Controller dispatches to the right vendor collector by kind. Kinds:
// redfish, vsphere, hyperv, onvif, unifi, omada, ruckus, extreme, cucm.
func Controller(ctx context.Context, d Deps, kind string, ip netip.Addr, loc *uuid.UUID, opts ControllerOpts) (Result, error) {
	switch strings.ToLower(kind) {
	case "redfish":
		return Redfish(ctx, d, ip, loc)
	case "vsphere", "esxi":
		return VSphere(ctx, d, ip, loc)
	case "hyperv", "hyper-v":
		return HyperV(ctx, d, ip, loc)
	case "onvif", "camera":
		return ONVIF(ctx, d, ip, loc)
	case "unifi":
		return UniFi(ctx, d, ip, loc)
	case "omada":
		return Omada(ctx, d, ip, loc, opts.OmadaCID)
	case "ruckus":
		return Ruckus(ctx, d, ip, loc)
	case "extreme", "xiq":
		return Extreme(ctx, d, ip, loc, opts.ExtremeBase)
	case "cucm", "voice":
		return CUCM(ctx, d, ip, loc, opts.CUCMVersion)
	default:
		return Result{}, fmt.Errorf("collect: unknown controller kind %q", kind)
	}
}

// tryCandidates iterates the credential candidates for ip whose kind is in
// kinds, decrypts each, and calls try(user, pass). try returns (facts, ok); the
// first ok result wins and its credential is returned as bound. When no
// candidate authenticates, facts is nil.
func (d Deps) tryCandidates(
	ctx context.Context, ip netip.Addr, loc *uuid.UUID, kinds []domain.CredentialKind,
	try func(user, pass string) (*driver.Facts, bool),
) (*driver.Facts, *credresolver.CredRef) {
	want := func(k domain.CredentialKind) bool {
		for _, x := range kinds {
			if x == k {
				return true
			}
		}
		return false
	}

	// Candidate order: scope-resolved credentials first (a bound credential is
	// the operator's intent for that IP), then ALL stored credentials of the
	// needed kind as a fallback — so an import works even when the credential
	// isn't bound to a subnet/location group yet (matches the scan's "try all"
	// default). Deduped by ID.
	var refs []credresolver.CredRef
	if groups, err := d.Fetcher.CredentialCandidates(ctx, ip, loc); err == nil {
		for _, g := range groups {
			refs = append(refs, g.Members...)
		}
	}
	if d.Queries != nil {
		if rows, err := d.Queries.ListCredentialCandidates(ctx); err == nil {
			for _, r := range rows {
				refs = append(refs, credresolver.CredRef{ID: r.ID, Kind: domain.CredentialKind(r.Kind), Weak: r.Weak})
			}
		}
	}

	seen := make(map[uuid.UUID]bool, len(refs))
	for _, m := range refs {
		if !want(m.Kind) || seen[m.ID] {
			continue
		}
		seen[m.ID] = true
		dec, err := d.Decrypt(ctx, m.ID)
		if err != nil {
			continue
		}
		user, pass := splitUserPass(dec.Community)
		if facts, ok := try(user, pass); ok {
			c := m
			return facts, &c
		}
	}
	return nil, nil
}

func (d Deps) persist(ctx context.Context, ip netip.Addr, loc *uuid.UUID, drv driver.Driver, cat domain.DeviceCategory, conf int, facts *driver.Facts, bound *credresolver.CredRef) (uuid.UUID, error) {
	res := discovery.HostResult{
		IP: ip, Alive: true, MatchedDrv: drv,
		Match: driver.Match{Confidence: conf, Category: cat},
		Facts: facts, BoundCred: bound,
	}
	return apply.New(d.Queries).Apply(ctx, res, loc)
}

// Redfish collects a server BMC (iLO/iDRAC) over Redfish (http_basic).
func Redfish(ctx context.Context, d Deps, ip netip.Addr, loc *uuid.UUID) (Result, error) {
	drv := rfdrv.New()
	facts, bound := d.tryCandidates(ctx, ip, loc, []domain.CredentialKind{domain.CredHTTPBasic}, func(user, pass string) (*driver.Facts, bool) {
		client := rf.NewClient("https://"+ip.String(), user, pass, cookieJarClient(d.httpTimeout()))
		var root map[string]any
		if err := client.GetJSON(ctx, "/redfish/v1/", &root); err != nil {
			return nil, false
		}
		f, err := drv.Collect(&rfdrv.Session{Client: client, Ctx: ctx}, driver.Probe{IP: ip})
		if err != nil {
			return nil, false
		}
		return &f, true
	})
	if facts == nil {
		return Result{}, fmt.Errorf("redfish: no http_basic credential collected a BMC at %s", ip)
	}
	id, err := d.persist(ctx, ip, loc, drv, domain.CatServer, 72, facts, bound)
	if err != nil {
		return Result{}, err
	}
	return Result{DeviceID: id, Summary: fmt.Sprintf("BMC %s/%s health=%s", facts.BMC.Vendor, facts.BMC.ControllerKind, facts.BMC.Health)}, nil
}

// VSphere collects an ESXi host's VMs + datastores via govmomi.
func VSphere(ctx context.Context, d Deps, ip netip.Addr, loc *uuid.UUID) (Result, error) {
	drv := vspheredrv.New()
	facts, bound := d.tryCandidates(ctx, ip, loc, []domain.CredentialKind{domain.CredHTTPBasic, domain.CredVendorAPI}, func(user, pass string) (*driver.Facts, bool) {
		u := &url.URL{Scheme: "https", Host: ip.String(), Path: "/sdk", User: url.UserPassword(user, pass)}
		gc, err := govmomi.NewClient(ctx, u, true) // insecure: mgmt-LAN self-signed
		if err != nil {
			return nil, false
		}
		f, err := drv.Collect(&vspheredrv.Session{Client: gc.Client, Ctx: ctx}, driver.Probe{IP: ip})
		_ = gc.Logout(ctx)
		if err != nil {
			return nil, false
		}
		return &f, true
	})
	if facts == nil {
		return Result{}, fmt.Errorf("vsphere: no credential collected host %s", ip)
	}
	id, err := d.persist(ctx, ip, loc, drv, domain.CatVirtualHost, 71, facts, bound)
	if err != nil {
		return Result{}, err
	}
	return Result{DeviceID: id, Summary: fmt.Sprintf("%d VMs, %d datastores", len(facts.VMs), len(facts.Storage))}, nil
}

// HyperV collects a Hyper-V host's VMs over WinRM/PowerShell.
func HyperV(ctx context.Context, d Deps, ip netip.Addr, loc *uuid.UUID) (Result, error) {
	drv := hypervdrv.New()
	facts, bound := d.tryCandidates(ctx, ip, loc, []domain.CredentialKind{domain.CredWinRM}, func(user, pass string) (*driver.Facts, bool) {
		endpoint := winrm.NewEndpoint(ip.String(), 5985, false, false, nil, nil, nil, d.winrmTimeout())
		client, err := winrm.NewClient(endpoint, user, pass)
		if err != nil {
			return nil, false
		}
		f, err := drv.Collect(&hypervdrv.Session{Runner: winrmRunner{c: client}, Ctx: ctx}, driver.Probe{IP: ip})
		if err != nil {
			return nil, false
		}
		return &f, true
	})
	if facts == nil {
		return Result{}, fmt.Errorf("hyperv: no winrm credential collected host %s", ip)
	}
	id, err := d.persist(ctx, ip, loc, drv, domain.CatVirtualHost, 60, facts, bound)
	if err != nil {
		return Result{}, err
	}
	return Result{DeviceID: id, Summary: fmt.Sprintf("%d VMs", len(facts.VMs))}, nil
}

// ONVIF collects an IP camera's ONVIF device-info + profiles.
func ONVIF(ctx context.Context, d Deps, ip netip.Addr, loc *uuid.UUID) (Result, error) {
	drv := onvifdrv.New()
	facts, bound := d.tryCandidates(ctx, ip, loc, []domain.CredentialKind{domain.CredONVIF, domain.CredHTTPBasic}, func(user, pass string) (*driver.Facts, bool) {
		client := ov.NewClient("http://"+ip.String(), user, pass, cookieJarClient(d.httpTimeout()))
		f, err := drv.Collect(&onvifdrv.Session{Client: client, Ctx: ctx}, driver.Probe{IP: ip})
		if err != nil {
			return nil, false
		}
		return &f, true
	})
	if facts == nil {
		return Result{}, fmt.Errorf("onvif: no credential collected camera %s", ip)
	}
	id, err := d.persist(ctx, ip, loc, drv, domain.CatCamera, 75, facts, bound)
	if err != nil {
		return Result{}, err
	}
	return Result{DeviceID: id, Summary: fmt.Sprintf("%s %s", facts.Camera.Manufacturer, facts.Camera.Model)}, nil
}

// UniFi collects a UniFi controller's AP inventory.
func UniFi(ctx context.Context, d Deps, ip netip.Addr, loc *uuid.UUID) (Result, error) {
	drv := unifidrv.New()
	facts, bound := d.tryCandidates(ctx, ip, loc, []domain.CredentialKind{domain.CredHTTPBasic, domain.CredVendorAPI}, func(user, pass string) (*driver.Facts, bool) {
		client := uc.NewClient("https://"+ip.String()+":8443", "default", user, pass, cookieJarClient(d.httpTimeout()))
		if err := client.Login(ctx); err != nil {
			return nil, false
		}
		f, err := drv.Collect(&unifidrv.Session{Client: client, Ctx: ctx}, driver.Probe{IP: ip})
		if err != nil {
			return nil, false
		}
		return &f, true
	})
	if facts == nil {
		return Result{}, fmt.Errorf("unifi: no credential collected controller %s", ip)
	}
	id, err := d.persist(ctx, ip, loc, drv, domain.CatWirelessController, 78, facts, bound)
	if err != nil {
		return Result{}, err
	}
	return Result{DeviceID: id, Summary: fmt.Sprintf("%d APs", len(facts.APs))}, nil
}

// Omada collects a TP-Link Omada controller's APs (needs the controller id).
func Omada(ctx context.Context, d Deps, ip netip.Addr, loc *uuid.UUID, cid string) (Result, error) {
	if cid == "" {
		return Result{}, fmt.Errorf("omada: controller id (cid) is required")
	}
	drv := omadadrv.New()
	facts, bound := d.tryCandidates(ctx, ip, loc, []domain.CredentialKind{domain.CredHTTPBasic, domain.CredVendorAPI}, func(user, pass string) (*driver.Facts, bool) {
		client := oc.NewClient("https://"+ip.String()+":8043", cid, "Default", user, pass, cookieJarClient(d.httpTimeout()))
		if err := client.Login(ctx); err != nil {
			return nil, false
		}
		f, err := drv.Collect(&omadadrv.Session{Client: client, Ctx: ctx}, driver.Probe{IP: ip})
		if err != nil {
			return nil, false
		}
		return &f, true
	})
	if facts == nil {
		return Result{}, fmt.Errorf("omada: no credential collected controller %s", ip)
	}
	id, err := d.persist(ctx, ip, loc, drv, domain.CatWirelessController, 78, facts, bound)
	if err != nil {
		return Result{}, err
	}
	return Result{DeviceID: id, Summary: fmt.Sprintf("%d APs", len(facts.APs))}, nil
}

// Ruckus collects a Ruckus SmartZone controller's APs.
func Ruckus(ctx context.Context, d Deps, ip netip.Addr, loc *uuid.UUID) (Result, error) {
	drv := ruckusdrv.New()
	facts, bound := d.tryCandidates(ctx, ip, loc, []domain.CredentialKind{domain.CredHTTPBasic, domain.CredVendorAPI}, func(user, pass string) (*driver.Facts, bool) {
		client := rc.NewClient("https://"+ip.String()+":8443", "", user, pass, cookieJarClient(d.httpTimeout()))
		if err := client.Login(ctx); err != nil {
			return nil, false
		}
		f, err := drv.Collect(&ruckusdrv.Session{Client: client, Ctx: ctx}, driver.Probe{IP: ip})
		if err != nil {
			return nil, false
		}
		return &f, true
	})
	if facts == nil {
		return Result{}, fmt.Errorf("ruckus: no credential collected controller %s", ip)
	}
	id, err := d.persist(ctx, ip, loc, drv, domain.CatWirelessController, 78, facts, bound)
	if err != nil {
		return Result{}, err
	}
	return Result{DeviceID: id, Summary: fmt.Sprintf("%d APs", len(facts.APs))}, nil
}

// Extreme collects an ExtremeCloud IQ (XIQ) tenant's APs against an anchor IP.
func Extreme(ctx context.Context, d Deps, ip netip.Addr, loc *uuid.UUID, baseURL string) (Result, error) {
	drv := extremedrv.New()
	facts, bound := d.tryCandidates(ctx, ip, loc, []domain.CredentialKind{domain.CredHTTPBasic, domain.CredVendorAPI}, func(user, pass string) (*driver.Facts, bool) {
		client := ec.NewClient(baseURL, user, pass, cookieJarClient(d.httpTimeout()))
		if err := client.Login(ctx); err != nil {
			return nil, false
		}
		f, err := drv.Collect(&extremedrv.Session{Client: client, Ctx: ctx}, driver.Probe{IP: ip})
		if err != nil {
			return nil, false
		}
		return &f, true
	})
	if facts == nil {
		return Result{}, fmt.Errorf("extreme: no credential collected tenant at %s", ip)
	}
	id, err := d.persist(ctx, ip, loc, drv, domain.CatWirelessController, 78, facts, bound)
	if err != nil {
		return Result{}, err
	}
	return Result{DeviceID: id, Summary: fmt.Sprintf("%d APs", len(facts.APs))}, nil
}

// CUCM collects a Cisco CUCM publisher's phone registry over AXL. AXL is HTTP
// Basic per-request (no session login), so a candidate that returns phones
// authenticates implicitly.
func CUCM(ctx context.Context, d Deps, ip netip.Addr, loc *uuid.UUID, version string) (Result, error) {
	if version == "" {
		version = "12.5"
	}
	drv := cucmdrv.New()
	facts, bound := d.tryCandidates(ctx, ip, loc, []domain.CredentialKind{domain.CredHTTPBasic, domain.CredVendorAPI}, func(user, pass string) (*driver.Facts, bool) {
		client := cucm.NewClient("https://"+ip.String()+":8443", user, pass, version, cookieJarClient(d.httpTimeout()))
		f, err := drv.Collect(&cucmdrv.Session{Client: client, Ctx: ctx}, driver.Probe{IP: ip})
		if err != nil {
			return nil, false
		}
		return &f, true
	})
	if facts == nil {
		return Result{}, fmt.Errorf("cucm: no credential collected publisher %s", ip)
	}
	id, err := d.persist(ctx, ip, loc, drv, domain.CatPBX, 75, facts, bound)
	if err != nil {
		return Result{}, err
	}
	return Result{DeviceID: id, Summary: fmt.Sprintf("%d phones", len(facts.Phones))}, nil
}

// ADImport imports AD computer objects over LDAP from a base DN and persists
// them (reconcile by primary_ip+location). Computers that don't resolve to an
// IPv4 are skipped + counted. Resolves an ldap credential scoped to the DC IP.
func ADImport(ctx context.Context, d Deps, host, baseDN string, loc *uuid.UUID) (ADResult, error) {
	if baseDN == "" {
		return ADResult{}, fmt.Errorf("adimport: baseDN is required")
	}
	// Resolve an ldap credential scoped to the DC's IP.
	var dcAddr netip.Addr
	if hosts, _ := net.LookupHost(host); len(hosts) > 0 {
		for _, s := range hosts {
			if a, err := netip.ParseAddr(s); err == nil {
				dcAddr = a
				break
			}
		}
	}
	var user, pass string
	if dcAddr.IsValid() {
		groups, _ := d.Fetcher.CredentialCandidates(ctx, dcAddr, loc)
		for _, g := range groups {
			for _, m := range g.Members {
				if m.Kind != domain.CredLDAP {
					continue
				}
				if dec, err := d.Decrypt(ctx, m.ID); err == nil {
					user, pass = splitUserPass(dec.Community)
					break
				}
			}
			if user != "" {
				break
			}
		}
	}
	if user == "" {
		return ADResult{}, fmt.Errorf("adimport: no ldap credential resolved for DC %s", host)
	}

	conn, err := ldap.DialURL("ldap://" + host + ":389")
	if err != nil {
		return ADResult{}, fmt.Errorf("adimport: LDAP dial failed: %w", err)
	}
	defer conn.Close()
	if err := conn.Bind(user, pass); err != nil {
		return ADResult{}, fmt.Errorf("adimport: LDAP bind failed: %w", err)
	}
	computers, err := adimport.SearchComputers(conn, baseDN)
	if err != nil {
		return ADResult{}, fmt.Errorf("adimport: search failed: %w", err)
	}

	applier := apply.New(d.Queries)
	out := ADResult{Found: len(computers)}
	for _, c := range computers {
		ip := resolveFirstIP(c.DNSHostName)
		if !ip.IsValid() {
			out.Skipped++
			continue
		}
		res := discovery.HostResult{
			IP: ip, Alive: true,
			Match: driver.Match{Category: c.Category},
			Facts: &driver.Facts{Hostname: c.Name, OSVersion: c.OSVersion, Vendor: "Microsoft", KV: map[string]string{"ad.os": c.OS}},
		}
		if _, err := applier.Apply(ctx, res, loc); err == nil {
			out.Imported++
		}
	}
	return out, nil
}

// --- shared helpers (moved from cmd/hims-collector) -------------------------

// winrmRunner adapts a masterzen/winrm client to the hyperv.Runner interface.
type winrmRunner struct{ c *winrm.Client }

func (r winrmRunner) Run(ctx context.Context, script string) (string, error) {
	stdout, stderr, code, err := r.c.RunPSWithContext(ctx, script)
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("winrm exit %d: %s", code, strings.TrimSpace(stderr))
	}
	return stdout, nil
}

// cookieJarClient builds an HTTPS client with a cookie jar (TLS-insecure for
// mgmt-LAN self-signed certs) and the given request timeout.
func cookieJarClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	jar, _ := cookiejar.New(nil)
	return &http.Client{
		Timeout:   timeout,
		Jar:       jar,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, //nolint:gosec
	}
}

// splitUserPass splits a "username:password" secret on the first colon.
func splitUserPass(s string) (user, pass string) {
	if i := strings.IndexByte(s, ':'); i >= 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}

// resolveFirstIP resolves a hostname to its first IPv4 address (empty on fail).
func resolveFirstIP(host string) netip.Addr {
	if host == "" {
		return netip.Addr{}
	}
	ips, err := net.LookupHost(host)
	if err != nil {
		return netip.Addr{}
	}
	for _, s := range ips {
		if a, err := netip.ParseAddr(s); err == nil && a.Is4() {
			return a
		}
	}
	return netip.Addr{}
}
