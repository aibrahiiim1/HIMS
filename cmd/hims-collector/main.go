// Command hims-collector runs discovery + monitoring. Phase 1 exposes a
// one-shot discovery mode: given an IP (or CIDR via repeated runs), it runs
// the pipeline and prints what it found. The scheduled-monitoring loop and
// NATS plumbing land in a later phase.
package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/netip"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/go-ldap/ldap/v3"
	"github.com/google/uuid"
	"github.com/masterzen/winrm"
	"github.com/vmware/govmomi"

	"github.com/coralsearesorts/hims/internal/adimport"
	"github.com/coralsearesorts/hims/internal/alerting"
	"github.com/coralsearesorts/hims/internal/apply"
	"github.com/coralsearesorts/hims/internal/credresolver"
	"github.com/coralsearesorts/hims/internal/discovery"
	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/driver"
	hypervdrv "github.com/coralsearesorts/hims/internal/driver/hyperv"
	onvifdrv "github.com/coralsearesorts/hims/internal/driver/onvif"
	rfdrv "github.com/coralsearesorts/hims/internal/driver/redfish"
	unifidrv "github.com/coralsearesorts/hims/internal/driver/unifi"
	vspheredrv "github.com/coralsearesorts/hims/internal/driver/vsphere"
	"github.com/coralsearesorts/hims/internal/drivers"
	"github.com/coralsearesorts/hims/internal/monitoring"
	ov "github.com/coralsearesorts/hims/internal/onvif"
	rf "github.com/coralsearesorts/hims/internal/redfish"
	"github.com/coralsearesorts/hims/internal/scan"
	"github.com/coralsearesorts/hims/internal/secret"
	"github.com/coralsearesorts/hims/internal/storage/postgres"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
	uc "github.com/coralsearesorts/hims/internal/unifi"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	target := flag.String("ip", "", "single IP to discover (one-shot mode, no DB)")
	discover := flag.String("discover", "", "discover an IP AND persist it to the CMDB (needs DB + creds)")
	scanCIDR := flag.String("scan", "", "discover AND persist every host in a CIDR (needs DB + creds)")
	redfishIP := flag.String("redfish", "", "collect a server BMC (iLO/iDRAC) over Redfish AND persist (needs DB + http_basic creds)")
	vsphereIP := flag.String("vsphere", "", "collect an ESXi host's VMs + datastores over the vSphere API AND persist (needs DB + creds)")
	hypervIP := flag.String("hyperv", "", "collect a Hyper-V host's VMs over WinRM AND persist (needs DB + winrm creds)")
	onvifIP := flag.String("onvif", "", "collect an IP camera's ONVIF inventory AND persist (needs DB + onvif/http_basic creds)")
	unifiIP := flag.String("unifi", "", "collect a UniFi controller's APs AND persist (needs DB + http_basic creds)")
	adServer := flag.String("adimport", "", "import AD computer objects over LDAP from this DC host AND persist (needs DB + ldap creds)")
	adBaseDN := flag.String("basedn", "", "AD base DN / OU subtree to import (e.g. OU=HotelA,DC=corp,DC=local)")
	concurrency := flag.Int("concurrency", 16, "max concurrent hosts during -scan")
	maxHosts := flag.Int("max-hosts", 4096, "refuse a -scan scope larger than this")
	location := flag.String("location", "", "location UUID to scope discovered devices to")
	monitor := flag.Bool("monitor", false, "run the scheduled monitoring loop")
	seed := flag.Bool("seed", false, "seed default monitoring checks, then exit")
	rekey := flag.Bool("rekey", false, "rotate credential encryption: re-seal all secrets from HIMS_OLD_ENCRYPTION_KEY under HIMS_ENCRYPTION_KEY")
	tick := flag.Duration("tick", 30*time.Second, "monitoring sweep interval")
	flag.Parse()

	reg := drivers.Builtin()
	slog.Info("hims-collector", "drivers", reg.Names())

	if *rekey {
		runRekey()
		return
	}

	if *monitor || *seed {
		runMonitoring(*monitor, *seed, *tick)
		return
	}

	if *discover != "" {
		runDiscover(reg, *discover, *location)
		return
	}

	if *scanCIDR != "" {
		runScan(reg, *scanCIDR, *location, *concurrency, *maxHosts)
		return
	}

	if *redfishIP != "" {
		runRedfish(reg, *redfishIP, *location)
		return
	}

	if *vsphereIP != "" {
		runVSphere(reg, *vsphereIP, *location)
		return
	}

	if *hypervIP != "" {
		runHyperV(reg, *hypervIP, *location)
		return
	}

	if *onvifIP != "" {
		runONVIF(reg, *onvifIP, *location)
		return
	}

	if *unifiIP != "" {
		runUniFi(reg, *unifiIP, *location)
		return
	}

	if *adServer != "" {
		runADImport(reg, *adServer, *adBaseDN, *location)
		return
	}

	if *target == "" {
		fmt.Println("hims-collector: pass -ip <addr> for one-shot discovery,")
		fmt.Println("or -monitor to run the scheduled monitoring loop (-seed to seed checks).")
		return
	}

	ip, err := netip.ParseAddr(*target)
	if err != nil {
		slog.Error("invalid -ip", "value", *target, "error", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// One-shot discovery requires a CandidateFetcher + Decrypt. Without a DB
	// wired here, we run the light-probe + classification stages only and
	// report the driver match — full authenticated collection runs inside
	// the API/worker process where storage is available.
	cfg := discovery.PipelineConfig{
		Registry:    reg,
		Fetcher:     emptyFetcher{},
		Decrypt:     noDecrypt,
		PingTimeout: 2 * time.Second,
		SNMPTimeout: 3 * time.Second,
	}
	res := discovery.Run(ctx, ip, nil, cfg)
	fmt.Printf("IP %s — alive=%v ports=%v\n", res.IP, res.Alive, res.OpenPorts)
	if res.MatchedDrv != nil {
		fmt.Printf("  classified: driver=%s category=%s confidence=%d\n",
			res.MatchedDrv.Name(), res.Match.Category, res.Match.Confidence)
	} else {
		fmt.Println("  classified: unknown (no driver matched)")
	}
	if res.Error != nil {
		fmt.Printf("  note: %v\n", res.Error)
	}
}

// runRekey rotates credential encryption: it opens every credential's blob
// with the old key (HIMS_OLD_ENCRYPTION_KEY) and re-seals it under the new key
// (HIMS_ENCRYPTION_KEY), updating the stored blob + KeyID. Idempotent: rows
// already under the new KeyID are skipped, so a re-run is safe.
func runRekey() {
	oldKey, newKey := os.Getenv("HIMS_OLD_ENCRYPTION_KEY"), os.Getenv("HIMS_ENCRYPTION_KEY")
	if oldKey == "" || newKey == "" {
		slog.Error("rekey needs both HIMS_OLD_ENCRYPTION_KEY and HIMS_ENCRYPTION_KEY")
		os.Exit(1)
	}
	oldC, err := secret.NewCipher(oldKey)
	if err != nil {
		slog.Error("invalid HIMS_OLD_ENCRYPTION_KEY", "error", err)
		os.Exit(1)
	}
	newC, err := secret.NewCipher(newKey)
	if err != nil {
		slog.Error("invalid HIMS_ENCRYPTION_KEY", "error", err)
		os.Exit(1)
	}

	dbURL := os.Getenv("HIMS_DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://hims:hims@localhost:5432/hims?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, err := postgres.NewPool(ctx, postgres.PoolConfig{URL: dbURL, MaxOpenConns: 5})
	if err != nil {
		slog.Error("rekey: database unavailable", "error", err)
		os.Exit(1)
	}
	defer pool.Close()
	queries := db.New(pool)

	creds, err := queries.ListCredentials(ctx)
	if err != nil {
		slog.Error("rekey: list credentials failed", "error", err)
		os.Exit(1)
	}
	rotated, skipped, failed := 0, 0, 0
	for _, c := range creds {
		if c.KeyID == newC.KeyID() {
			skipped++
			continue // already under the new key
		}
		blob, keyID, err := secret.ReKey(oldC, newC, c.EncryptedBlob, c.KeyID)
		if err != nil {
			failed++
			slog.Warn("rekey: could not re-seal credential", "id", c.ID, "error", err)
			continue
		}
		if err := queries.UpdateCredentialSecret(ctx, db.UpdateCredentialSecretParams{
			ID: c.ID, EncryptedBlob: blob, KeyID: keyID,
		}); err != nil {
			failed++
			slog.Warn("rekey: update failed", "id", c.ID, "error", err)
			continue
		}
		rotated++
	}
	slog.Info("rekey complete", "rotated", rotated, "skipped", skipped, "failed", failed, "new_key_id", newC.KeyID())
	if failed > 0 {
		os.Exit(1)
	}
}

// runDiscover discovers an IP and persists the result to the CMDB. It wires
// the real credential fetcher (Postgres scope resolver), an in-memory decrypt
// closure (cipher.Open — plaintext never leaves this function), and the apply
// worker. This is the end-to-end path that populates the live system.
func runDiscover(reg *driver.Registry, ipStr, locStr string) {
	ip, err := netip.ParseAddr(ipStr)
	if err != nil {
		slog.Error("invalid -discover IP", "value", ipStr, "error", err)
		os.Exit(1)
	}
	locationID := parseLocationID(locStr)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	queries, cfg, closeDB := buildDiscoverDeps(ctx, reg)
	defer closeDB()

	res := discovery.Run(ctx, ip, locationID, cfg)
	id, err := apply.New(queries).Apply(ctx, res, locationID)
	if err != nil {
		slog.Error("discover: apply failed", "ip", ip.String(), "error", err)
		os.Exit(1)
	}
	if id == uuid.Nil {
		fmt.Printf("IP %s — not alive; nothing persisted\n", ip)
		return
	}
	drv := "unknown"
	if res.MatchedDrv != nil {
		drv = res.MatchedDrv.Name()
	}
	collected := res.Facts != nil
	fmt.Printf("IP %s persisted as device %s (driver=%s, category=%s, collected=%v)\n",
		ip, id, drv, res.Match.Category, collected)
}

// runScan discovers + persists every host in a CIDR with bounded concurrency.
// Reuses the same DB + credential-decrypt + pipeline + apply wiring as
// -discover, fanned out across the scope.
func runScan(reg *driver.Registry, cidr, locStr string, concurrency, maxHosts int) {
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil {
		slog.Error("invalid -scan CIDR", "value", cidr, "error", err)
		os.Exit(1)
	}
	hosts, err := discovery.ExpandCIDR(prefix, maxHosts)
	if err != nil {
		slog.Error("scan: scope rejected", "error", err)
		os.Exit(1)
	}
	locationID := parseLocationID(locStr)

	// Generous overall budget: per-host steps have their own timeouts.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	queries, cfg, closeDB := buildDiscoverDeps(ctx, reg)
	defer closeDB()

	applier := apply.New(queries)
	slog.Info("scan starting", "cidr", prefix.String(), "hosts", len(hosts), "concurrency", concurrency)
	res := scan.Scope(ctx, hosts, concurrency, func(ctx context.Context, ip netip.Addr) (uuid.UUID, error) {
		hctx, hcancel := context.WithTimeout(ctx, 45*time.Second)
		defer hcancel()
		r := discovery.Run(hctx, ip, locationID, cfg)
		return applier.Apply(hctx, r, locationID)
	})
	fmt.Printf("scan of %s complete — %d attempted: %d persisted, %d skipped(not-alive), %d failed\n",
		prefix, res.Total, res.Persisted, res.Skipped, res.Failed)
}

// runRedfish collects a server's out-of-band BMC (iLO/iDRAC) over Redfish and
// persists it. It resolves the scoped http_basic credentials, tries each
// against /redfish/v1/, and on success runs the redfish driver + apply worker.
// The http_basic secret is stored as "username:password" (split here, never
// logged).
func runRedfish(reg *driver.Registry, ipStr, locStr string) {
	ip, err := netip.ParseAddr(ipStr)
	if err != nil {
		slog.Error("invalid -redfish IP", "value", ipStr, "error", err)
		os.Exit(1)
	}
	locationID := parseLocationID(locStr)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	queries, cfg, closeDB := buildDiscoverDeps(ctx, reg)
	defer closeDB()

	groups, err := cfg.Fetcher.CredentialCandidates(ctx, ip, locationID)
	if err != nil {
		slog.Error("redfish: credential resolve failed", "error", err)
		os.Exit(1)
	}

	d := rfdrv.New()
	var facts *driver.Facts
	var bound *credresolver.CredRef
	for _, g := range groups {
		for _, m := range g.Members {
			if m.Kind != domain.CredHTTPBasic {
				continue
			}
			dec, err := cfg.Decrypt(ctx, m.ID)
			if err != nil {
				continue
			}
			user, pass := splitUserPass(dec.Community)
			client := rf.NewClient("https://"+ip.String(), user, pass, nil)
			var root map[string]any
			if err := client.GetJSON(ctx, "/redfish/v1/", &root); err != nil {
				continue // credential or host didn't answer Redfish
			}
			f, err := d.Collect(&rfdrv.Session{Client: client, Ctx: ctx}, driver.Probe{IP: ip})
			if err != nil {
				continue
			}
			c := m
			facts, bound = &f, &c
			break
		}
		if facts != nil {
			break
		}
	}
	if facts == nil {
		slog.Error("redfish: no http_basic credential collected a BMC at this IP", "ip", ip.String())
		os.Exit(1)
	}

	res := discovery.HostResult{
		IP: ip, Alive: true, MatchedDrv: d,
		Match: driver.Match{Confidence: 72, Category: domain.CatServer},
		Facts: facts, BoundCred: bound,
	}
	id, err := apply.New(queries).Apply(ctx, res, locationID)
	if err != nil {
		slog.Error("redfish: apply failed", "error", err)
		os.Exit(1)
	}
	fmt.Printf("BMC %s persisted as device %s (vendor=%s, controller=%s, health=%s)\n",
		ip, id, facts.BMC.Vendor, facts.BMC.ControllerKind, facts.BMC.Health)
}

// runVSphere collects an ESXi host's VMs + datastores over the vSphere API and
// persists them to the host's device. Resolves scoped vendor_api/http_basic
// credentials (secret = "username:password"), connects via govmomi, runs the
// vsphere driver + apply worker.
func runVSphere(reg *driver.Registry, ipStr, locStr string) {
	ip, err := netip.ParseAddr(ipStr)
	if err != nil {
		slog.Error("invalid -vsphere IP", "value", ipStr, "error", err)
		os.Exit(1)
	}
	locationID := parseLocationID(locStr)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	queries, cfg, closeDB := buildDiscoverDeps(ctx, reg)
	defer closeDB()

	groups, err := cfg.Fetcher.CredentialCandidates(ctx, ip, locationID)
	if err != nil {
		slog.Error("vsphere: credential resolve failed", "error", err)
		os.Exit(1)
	}

	d := vspheredrv.New()
	var facts *driver.Facts
	var bound *credresolver.CredRef
	for _, g := range groups {
		for _, m := range g.Members {
			if m.Kind != domain.CredHTTPBasic && m.Kind != domain.CredVendorAPI {
				continue
			}
			dec, err := cfg.Decrypt(ctx, m.ID)
			if err != nil {
				continue
			}
			user, pass := splitUserPass(dec.Community)
			u := &url.URL{Scheme: "https", Host: ip.String(), Path: "/sdk", User: url.UserPassword(user, pass)}
			gc, err := govmomi.NewClient(ctx, u, true) // insecure: mgmt LAN self-signed certs
			if err != nil {
				continue
			}
			f, err := d.Collect(&vspheredrv.Session{Client: gc.Client, Ctx: ctx}, driver.Probe{IP: ip})
			_ = gc.Logout(ctx)
			if err != nil {
				continue
			}
			c := m
			facts, bound = &f, &c
			break
		}
		if facts != nil {
			break
		}
	}
	if facts == nil {
		slog.Error("vsphere: no credential collected this host", "ip", ip.String())
		os.Exit(1)
	}

	res := discovery.HostResult{
		IP: ip, Alive: true, MatchedDrv: d,
		Match: driver.Match{Confidence: 71, Category: domain.CatVirtualHost},
		Facts: facts, BoundCred: bound,
	}
	id, err := apply.New(queries).Apply(ctx, res, locationID)
	if err != nil {
		slog.Error("vsphere: apply failed", "error", err)
		os.Exit(1)
	}
	fmt.Printf("ESXi %s persisted as device %s (%d VMs, %d datastores)\n",
		ip, id, len(facts.VMs), len(facts.Storage))
}

// runUniFi collects a UniFi controller's AP inventory and persists it.
// Resolves scoped http_basic credentials (secret = "username:password"), logs
// in via a cookie-jar HTTP client, lists APs, applies. UniFi controllers
// listen on 8443 (https) by default. Omada/Ruckus use different APIs (deferred).
func runUniFi(reg *driver.Registry, ipStr, locStr string) {
	ip, err := netip.ParseAddr(ipStr)
	if err != nil {
		slog.Error("invalid -unifi IP", "value", ipStr, "error", err)
		os.Exit(1)
	}
	locationID := parseLocationID(locStr)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	queries, cfg, closeDB := buildDiscoverDeps(ctx, reg)
	defer closeDB()

	groups, err := cfg.Fetcher.CredentialCandidates(ctx, ip, locationID)
	if err != nil {
		slog.Error("unifi: credential resolve failed", "error", err)
		os.Exit(1)
	}

	d := unifidrv.New()
	var facts *driver.Facts
	var bound *credresolver.CredRef
	for _, g := range groups {
		for _, m := range g.Members {
			if m.Kind != domain.CredHTTPBasic && m.Kind != domain.CredVendorAPI {
				continue
			}
			dec, err := cfg.Decrypt(ctx, m.ID)
			if err != nil {
				continue
			}
			user, pass := splitUserPass(dec.Community)
			jar, _ := cookiejar.New(nil)
			httpc := &http.Client{
				Timeout:   20 * time.Second,
				Jar:       jar,
				Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, //nolint:gosec // controller self-signed cert on mgmt LAN
			}
			client := uc.NewClient("https://"+ip.String()+":8443", "default", user, pass, httpc)
			if err := client.Login(ctx); err != nil {
				continue
			}
			f, err := d.Collect(&unifidrv.Session{Client: client, Ctx: ctx}, driver.Probe{IP: ip})
			if err != nil {
				continue
			}
			c := m
			facts, bound = &f, &c
			break
		}
		if facts != nil {
			break
		}
	}
	if facts == nil {
		slog.Error("unifi: no credential collected this controller", "ip", ip.String())
		os.Exit(1)
	}

	res := discovery.HostResult{
		IP: ip, Alive: true, MatchedDrv: d,
		Match: driver.Match{Confidence: 78, Category: domain.CatWirelessController},
		Facts: facts, BoundCred: bound,
	}
	id, err := apply.New(queries).Apply(ctx, res, locationID)
	if err != nil {
		slog.Error("unifi: apply failed", "error", err)
		os.Exit(1)
	}
	fmt.Printf("UniFi %s persisted as device %s (%d APs)\n", ip, id, len(facts.APs))
}

// runONVIF collects an IP camera's ONVIF inventory and persists it. Resolves
// scoped onvif/http_basic credentials (secret = "username:password"), verifies
// GetDeviceInformation, applies. The password is used only in memory.
func runONVIF(reg *driver.Registry, ipStr, locStr string) {
	ip, err := netip.ParseAddr(ipStr)
	if err != nil {
		slog.Error("invalid -onvif IP", "value", ipStr, "error", err)
		os.Exit(1)
	}
	locationID := parseLocationID(locStr)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	queries, cfg, closeDB := buildDiscoverDeps(ctx, reg)
	defer closeDB()

	groups, err := cfg.Fetcher.CredentialCandidates(ctx, ip, locationID)
	if err != nil {
		slog.Error("onvif: credential resolve failed", "error", err)
		os.Exit(1)
	}

	d := onvifdrv.New()
	var facts *driver.Facts
	var bound *credresolver.CredRef
	for _, g := range groups {
		for _, m := range g.Members {
			if m.Kind != domain.CredONVIF && m.Kind != domain.CredHTTPBasic {
				continue
			}
			dec, err := cfg.Decrypt(ctx, m.ID)
			if err != nil {
				continue
			}
			user, pass := splitUserPass(dec.Community)
			client := ov.NewClient("http://"+ip.String(), user, pass, nil)
			f, err := d.Collect(&onvifdrv.Session{Client: client, Ctx: ctx}, driver.Probe{IP: ip})
			if err != nil {
				continue
			}
			c := m
			facts, bound = &f, &c
			break
		}
		if facts != nil {
			break
		}
	}
	if facts == nil {
		slog.Error("onvif: no credential collected this camera", "ip", ip.String())
		os.Exit(1)
	}

	res := discovery.HostResult{
		IP: ip, Alive: true, MatchedDrv: d,
		Match: driver.Match{Confidence: 75, Category: domain.CatCamera},
		Facts: facts, BoundCred: bound,
	}
	id, err := apply.New(queries).Apply(ctx, res, locationID)
	if err != nil {
		slog.Error("onvif: apply failed", "error", err)
		os.Exit(1)
	}
	fmt.Printf("Camera %s persisted as device %s (%s %s)\n", ip, id, facts.Camera.Manufacturer, facts.Camera.Model)
}

// runADImport imports AD computer objects over LDAP and persists them as
// devices. Resolves an ldap credential (secret = "bindUser:password") for the
// DC, binds, searches the base DN, resolves each computer's DNS name to an IP,
// and applies via the existing worker (reconcile by primary_ip+location).
// Computers that don't resolve to an IP are skipped + counted.
func runADImport(reg *driver.Registry, host, baseDN, locStr string) {
	if baseDN == "" {
		slog.Error("adimport: -basedn is required (e.g. OU=HotelA,DC=corp,DC=local)")
		os.Exit(1)
	}
	locationID := parseLocationID(locStr)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	queries, cfg, closeDB := buildDiscoverDeps(ctx, reg)
	defer closeDB()

	// Resolve an ldap credential scoped to the DC's IP.
	dcIPs, _ := net.LookupHost(host)
	var dcAddr netip.Addr
	for _, s := range dcIPs {
		if a, err := netip.ParseAddr(s); err == nil {
			dcAddr = a
			break
		}
	}
	var user, pass string
	if dcAddr.IsValid() {
		groups, _ := cfg.Fetcher.CredentialCandidates(ctx, dcAddr, locationID)
		for _, g := range groups {
			for _, m := range g.Members {
				if m.Kind != domain.CredLDAP {
					continue
				}
				if dec, err := cfg.Decrypt(ctx, m.ID); err == nil {
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
		slog.Error("adimport: no ldap credential resolved for the DC", "host", host)
		os.Exit(1)
	}

	conn, err := ldap.DialURL("ldap://" + host + ":389")
	if err != nil {
		slog.Error("adimport: LDAP dial failed", "error", err)
		os.Exit(1)
	}
	defer conn.Close()
	if err := conn.Bind(user, pass); err != nil {
		slog.Error("adimport: LDAP bind failed", "error", err)
		os.Exit(1)
	}

	computers, err := adimport.SearchComputers(conn, baseDN)
	if err != nil {
		slog.Error("adimport: search failed", "error", err)
		os.Exit(1)
	}

	applier := apply.New(queries)
	imported, skipped := 0, 0
	for _, c := range computers {
		ip := resolveFirstIP(c.DNSHostName)
		if !ip.IsValid() {
			skipped++
			continue // no IP → can't reconcile; surfaced via the count
		}
		ver := c.OSVersion
		res := discovery.HostResult{
			IP: ip, Alive: true,
			Match: driver.Match{Category: c.Category},
			Facts: &driver.Facts{Hostname: c.Name, OSVersion: ver, Vendor: "Microsoft", KV: map[string]string{"ad.os": c.OS}},
		}
		if _, err := applier.Apply(ctx, res, locationID); err == nil {
			imported++
		}
	}
	fmt.Printf("AD import from %s (%s) — %d computers: %d imported, %d skipped(no DNS/IP)\n",
		host, baseDN, len(computers), imported, skipped)
}

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

// runHyperV collects a Hyper-V host's VMs over WinRM and persists them.
// Resolves scoped winrm credentials (secret = "username:password"), runs
// Get-VM, applies. WinRM listens on 5985 (http) by default.
func runHyperV(reg *driver.Registry, ipStr, locStr string) {
	ip, err := netip.ParseAddr(ipStr)
	if err != nil {
		slog.Error("invalid -hyperv IP", "value", ipStr, "error", err)
		os.Exit(1)
	}
	locationID := parseLocationID(locStr)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	queries, cfg, closeDB := buildDiscoverDeps(ctx, reg)
	defer closeDB()

	groups, err := cfg.Fetcher.CredentialCandidates(ctx, ip, locationID)
	if err != nil {
		slog.Error("hyperv: credential resolve failed", "error", err)
		os.Exit(1)
	}

	d := hypervdrv.New()
	var facts *driver.Facts
	var bound *credresolver.CredRef
	for _, g := range groups {
		for _, m := range g.Members {
			if m.Kind != domain.CredWinRM {
				continue
			}
			dec, err := cfg.Decrypt(ctx, m.ID)
			if err != nil {
				continue
			}
			user, pass := splitUserPass(dec.Community)
			endpoint := winrm.NewEndpoint(ip.String(), 5985, false, false, nil, nil, nil, 0)
			client, err := winrm.NewClient(endpoint, user, pass)
			if err != nil {
				continue
			}
			f, err := d.Collect(&hypervdrv.Session{Runner: winrmRunner{c: client}, Ctx: ctx}, driver.Probe{IP: ip})
			if err != nil {
				continue
			}
			c := m
			facts, bound = &f, &c
			break
		}
		if facts != nil {
			break
		}
	}
	if facts == nil {
		slog.Error("hyperv: no winrm credential collected this host", "ip", ip.String())
		os.Exit(1)
	}

	res := discovery.HostResult{
		IP: ip, Alive: true, MatchedDrv: d,
		Match: driver.Match{Confidence: 60, Category: domain.CatVirtualHost},
		Facts: facts, BoundCred: bound,
	}
	id, err := apply.New(queries).Apply(ctx, res, locationID)
	if err != nil {
		slog.Error("hyperv: apply failed", "error", err)
		os.Exit(1)
	}
	fmt.Printf("Hyper-V %s persisted as device %s (%d VMs)\n", ip, id, len(facts.VMs))
}

// splitUserPass splits a "username:password" secret on the first colon.
func splitUserPass(s string) (user, pass string) {
	if i := strings.IndexByte(s, ':'); i >= 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}

// parseLocationID parses an optional location UUID flag (empty → nil).
func parseLocationID(locStr string) *uuid.UUID {
	if locStr == "" {
		return nil
	}
	l, err := uuid.Parse(locStr)
	if err != nil {
		slog.Error("invalid -location UUID", "value", locStr, "error", err)
		os.Exit(1)
	}
	return &l
}

// buildDiscoverDeps connects the DB and assembles the pipeline config (scope
// resolver fetcher + in-memory cipher-decrypt closure). Exits on failure.
// Returns the queries handle, the pipeline config, and a close func.
func buildDiscoverDeps(ctx context.Context, reg *driver.Registry) (*db.Queries, discovery.PipelineConfig, func()) {
	dbURL := os.Getenv("HIMS_DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://hims:hims@localhost:5432/hims?sslmode=disable"
	}
	pool, err := postgres.NewPool(ctx, postgres.PoolConfig{URL: dbURL, MaxOpenConns: 10})
	if err != nil {
		slog.Error("discover: database unavailable", "error", err)
		os.Exit(1)
	}
	queries := db.New(pool)
	store := postgres.New(pool) // CandidateFetcher (scope resolver)

	cipher, err := secret.NewCipher(os.Getenv("HIMS_ENCRYPTION_KEY"))
	if err != nil {
		pool.Close()
		slog.Error("discover: HIMS_ENCRYPTION_KEY required to decrypt credentials", "error", err)
		os.Exit(1)
	}
	decrypt := func(ctx context.Context, id uuid.UUID) (discovery.DecryptedCred, error) {
		cred, err := queries.GetCredential(ctx, id)
		if err != nil {
			return discovery.DecryptedCred{}, err
		}
		plain, err := cipher.Open(cred.EncryptedBlob, cred.KeyID)
		if err != nil {
			return discovery.DecryptedCred{}, err
		}
		// plain is used only here and never logged.
		dc := discovery.DecryptedCred{ID: id, Kind: domain.CredentialKind(cred.Kind), Weak: cred.Weak}
		if cred.Kind == string(domain.CredSNMPv3) {
			if v3, err := discovery.ParseSNMPv3(plain); err == nil {
				dc.V3 = v3
			}
		} else {
			dc.Community = string(plain)
		}
		return dc, nil
	}
	cfg := discovery.PipelineConfig{
		Registry: reg, Fetcher: store, Decrypt: decrypt,
		PingTimeout: 2 * time.Second, SNMPTimeout: 3 * time.Second,
	}
	return queries, cfg, pool.Close
}

// emptyFetcher returns no credential candidates (one-shot mode has no DB).
type emptyFetcher struct{}

func (emptyFetcher) CredentialCandidates(_ context.Context, _ netip.Addr, _ *uuid.UUID) ([]credresolver.ScopedGroup, error) {
	return nil, nil
}

func noDecrypt(context.Context, uuid.UUID) (discovery.DecryptedCred, error) {
	return discovery.DecryptedCred{}, fmt.Errorf("no decrypt in one-shot mode")
}

// runMonitoring connects to Postgres and runs the monitoring engine: either a
// one-shot seed (-seed) and/or the scheduled sweep loop (-monitor). The loop
// runs until the process is signalled.
func runMonitoring(loop, seed bool, tick time.Duration) {
	dbURL := os.Getenv("HIMS_DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://hims:hims@localhost:5432/hims?sslmode=disable"
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	connCtx, connCancel := context.WithTimeout(ctx, 10*time.Second)
	defer connCancel()
	pool, err := postgres.NewPool(connCtx, postgres.PoolConfig{URL: dbURL, MaxOpenConns: 10})
	if err != nil {
		slog.Error("monitoring: database unavailable", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	queries := db.New(pool)
	engine := monitoring.NewEngine(queries, monitoring.NewPoller(nil, 3*time.Second), slog.Default())
	// SNMP-metric checks need to decrypt the device's bound community. With no
	// key, the engine still runs TCP reachability and skips snmp checks.
	if k := os.Getenv("HIMS_ENCRYPTION_KEY"); k != "" {
		c, err := secret.NewCipher(k)
		if err != nil {
			slog.Error("invalid HIMS_ENCRYPTION_KEY", "error", err)
			os.Exit(1)
		}
		engine.Cipher = c
		slog.Info("snmp-metric monitoring enabled", "key_id", c.KeyID())
	}
	// Chain alerting after each sweep: evaluate rules against the freshly
	// updated check statuses (opens alerts, bridges to work orders, resolves
	// recovered). Dependency inversion keeps monitoring unaware of alerting.
	alertEngine := alerting.NewEngine(queries, slog.Default())
	engine.AfterSweep = func(c context.Context) {
		if res, err := alertEngine.Evaluate(c); err != nil && c.Err() == nil {
			slog.Warn("alert evaluation failed", "error", err)
		} else if res.Opened > 0 || res.Resolved > 0 {
			slog.Info("alerts evaluated", "opened", res.Opened, "work_orders", res.WorkOrders, "resolved", res.Resolved)
		}
	}

	if seed {
		n, err := engine.SeedDefaults(ctx)
		if err != nil {
			slog.Error("monitoring: seed failed", "error", err)
			os.Exit(1)
		}
		slog.Info("monitoring: seed complete", "checks", n)
		if !loop {
			return
		}
	}

	if loop {
		if err := engine.Loop(ctx, tick); err != nil && ctx.Err() == nil {
			slog.Error("monitoring loop exited", "error", err)
			os.Exit(1)
		}
		slog.Info("monitoring loop stopped")
	}
}
