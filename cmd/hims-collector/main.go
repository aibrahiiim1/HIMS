// Command hims-collector runs discovery + monitoring. Phase 1 exposes a
// one-shot discovery mode: given an IP (or CIDR via repeated runs), it runs
// the pipeline and prints what it found. The scheduled-monitoring loop and
// NATS plumbing land in a later phase.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/coralsearesorts/hims/internal/alerting"
	"github.com/coralsearesorts/hims/internal/apply"
	"github.com/coralsearesorts/hims/internal/collect"
	"github.com/coralsearesorts/hims/internal/credresolver"
	"github.com/coralsearesorts/hims/internal/discovery"
	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/driver"
	"github.com/coralsearesorts/hims/internal/drivers"
	"github.com/coralsearesorts/hims/internal/monitoring"
	"github.com/coralsearesorts/hims/internal/scan"
	"github.com/coralsearesorts/hims/internal/secret"
	"github.com/coralsearesorts/hims/internal/storage/postgres"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
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
	omadaIP := flag.String("omada", "", "collect a TP-Link Omada controller's APs AND persist (needs DB + http_basic creds)")
	omadaCID := flag.String("omada-cid", "", "Omada controller id (from /api/info)")
	ruckusIP := flag.String("ruckus", "", "collect a Ruckus SmartZone controller's APs AND persist (needs DB + http_basic creds)")
	cucmIP := flag.String("cucm", "", "collect a Cisco CUCM publisher's phone registry over AXL AND persist (needs DB + http_basic/vendor_api creds)")
	cucmVer := flag.String("cucm-version", "12.5", "CUCM AXL schema version (e.g. 12.5, 14.0)")
	extremeIP := flag.String("extreme", "", "collect an ExtremeCloud IQ (XIQ) tenant's APs AND persist (needs DB + http_basic/vendor_api creds)")
	extremeBase := flag.String("extreme-base", "", "XIQ API base URL (default https://api.extremecloudiq.com)")
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

	if *omadaIP != "" {
		runOmada(reg, *omadaIP, *omadaCID, *location)
		return
	}

	if *ruckusIP != "" {
		runRuckus(reg, *ruckusIP, *location)
		return
	}

	if *cucmIP != "" {
		runCUCM(reg, *cucmIP, *cucmVer, *location)
		return
	}

	if *extremeIP != "" {
		runExtreme(reg, *extremeIP, *extremeBase, *location)
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

// The credentialed single-target collectors below are thin CLI wrappers over
// the shared internal/collect package (the same cores the API's operator-
// launched imports use — no duplication). Each resolves DB + credential deps,
// invokes the collector, and prints the result or exits non-zero.

// collectDeps builds the shared collect.Deps from the discovery wiring.
func collectDeps(ctx context.Context, reg *driver.Registry) (collect.Deps, func()) {
	queries, cfg, closeDB := buildDiscoverDeps(ctx, reg)
	return collect.Deps{Queries: queries, Reg: reg, Fetcher: cfg.Fetcher, Decrypt: cfg.Decrypt}, closeDB
}

// mustIP parses an IP flag or exits.
func mustIP(flag, ipStr string) netip.Addr {
	ip, err := netip.ParseAddr(ipStr)
	if err != nil {
		slog.Error("invalid "+flag+" IP", "value", ipStr, "error", err)
		os.Exit(1)
	}
	return ip
}

// reportCollect prints a single-target collection result or exits on error.
func reportCollect(kind string, ip netip.Addr, r collect.Result, err error) {
	if err != nil {
		slog.Error(kind+": collect failed", "ip", ip.String(), "error", err)
		os.Exit(1)
	}
	fmt.Printf("%s %s persisted as device %s (%s)\n", kind, ip, r.DeviceID, r.Summary)
}

func runRedfish(reg *driver.Registry, ipStr, locStr string) {
	ip, loc := mustIP("-redfish", ipStr), parseLocationID(locStr)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	deps, closeDB := collectDeps(ctx, reg)
	defer closeDB()
	r, err := collect.Redfish(ctx, deps, ip, loc)
	reportCollect("redfish", ip, r, err)
}

func runVSphere(reg *driver.Registry, ipStr, locStr string) {
	ip, loc := mustIP("-vsphere", ipStr), parseLocationID(locStr)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	deps, closeDB := collectDeps(ctx, reg)
	defer closeDB()
	r, err := collect.VSphere(ctx, deps, ip, loc)
	reportCollect("vsphere", ip, r, err)
}

func runHyperV(reg *driver.Registry, ipStr, locStr string) {
	ip, loc := mustIP("-hyperv", ipStr), parseLocationID(locStr)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	deps, closeDB := collectDeps(ctx, reg)
	defer closeDB()
	r, err := collect.HyperV(ctx, deps, ip, loc)
	reportCollect("hyperv", ip, r, err)
}

func runONVIF(reg *driver.Registry, ipStr, locStr string) {
	ip, loc := mustIP("-onvif", ipStr), parseLocationID(locStr)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	deps, closeDB := collectDeps(ctx, reg)
	defer closeDB()
	r, err := collect.ONVIF(ctx, deps, ip, loc)
	reportCollect("onvif", ip, r, err)
}

func runUniFi(reg *driver.Registry, ipStr, locStr string) {
	ip, loc := mustIP("-unifi", ipStr), parseLocationID(locStr)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	deps, closeDB := collectDeps(ctx, reg)
	defer closeDB()
	r, err := collect.UniFi(ctx, deps, ip, loc)
	reportCollect("unifi", ip, r, err)
}

func runOmada(reg *driver.Registry, ipStr, cid, locStr string) {
	ip, loc := mustIP("-omada", ipStr), parseLocationID(locStr)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	deps, closeDB := collectDeps(ctx, reg)
	defer closeDB()
	r, err := collect.Omada(ctx, deps, ip, loc, cid)
	reportCollect("omada", ip, r, err)
}

func runRuckus(reg *driver.Registry, ipStr, locStr string) {
	ip, loc := mustIP("-ruckus", ipStr), parseLocationID(locStr)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	deps, closeDB := collectDeps(ctx, reg)
	defer closeDB()
	r, err := collect.Ruckus(ctx, deps, ip, loc)
	reportCollect("ruckus", ip, r, err)
}

func runExtreme(reg *driver.Registry, ipStr, baseURL, locStr string) {
	ip, loc := mustIP("-extreme", ipStr), parseLocationID(locStr)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	deps, closeDB := collectDeps(ctx, reg)
	defer closeDB()
	r, err := collect.Extreme(ctx, deps, ip, loc, baseURL)
	reportCollect("extreme", ip, r, err)
}

func runCUCM(reg *driver.Registry, ipStr, version, locStr string) {
	ip, loc := mustIP("-cucm", ipStr), parseLocationID(locStr)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	deps, closeDB := collectDeps(ctx, reg)
	defer closeDB()
	r, err := collect.CUCM(ctx, deps, ip, loc, version)
	reportCollect("cucm", ip, r, err)
}

func runADImport(reg *driver.Registry, host, baseDN, locStr string) {
	loc := parseLocationID(locStr)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	deps, closeDB := collectDeps(ctx, reg)
	defer closeDB()
	res, err := collect.ADImport(ctx, deps, host, baseDN, loc)
	if err != nil {
		slog.Error("adimport: failed", "host", host, "error", err)
		os.Exit(1)
	}
	fmt.Printf("AD import from %s (%s) — %d found: %d imported, %d skipped(no DNS/IP)\n",
		host, baseDN, res.Found, res.Imported, res.Skipped)
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
