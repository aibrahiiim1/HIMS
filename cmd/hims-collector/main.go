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
	"github.com/coralsearesorts/hims/internal/credresolver"
	"github.com/coralsearesorts/hims/internal/discovery"
	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/driver"
	"github.com/coralsearesorts/hims/internal/drivers"
	"github.com/coralsearesorts/hims/internal/monitoring"
	"github.com/coralsearesorts/hims/internal/secret"
	"github.com/coralsearesorts/hims/internal/storage/postgres"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	target := flag.String("ip", "", "single IP to discover (one-shot mode, no DB)")
	discover := flag.String("discover", "", "discover an IP AND persist it to the CMDB (needs DB + creds)")
	location := flag.String("location", "", "location UUID to scope the discovered device to")
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
	var locationID *uuid.UUID
	if locStr != "" {
		l, err := uuid.Parse(locStr)
		if err != nil {
			slog.Error("invalid -location UUID", "value", locStr, "error", err)
			os.Exit(1)
		}
		locationID = &l
	}

	dbURL := os.Getenv("HIMS_DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://hims:hims@localhost:5432/hims?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	pool, err := postgres.NewPool(ctx, postgres.PoolConfig{URL: dbURL, MaxOpenConns: 5})
	if err != nil {
		slog.Error("discover: database unavailable", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	queries := db.New(pool)
	store := postgres.New(pool) // CandidateFetcher (scope resolver)

	cipher, err := secret.NewCipher(os.Getenv("HIMS_ENCRYPTION_KEY"))
	if err != nil {
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
		// plain (the community) is used only here and never logged.
		return discovery.DecryptedCred{
			ID: id, Kind: domain.CredentialKind(cred.Kind), Community: string(plain), Weak: cred.Weak,
		}, nil
	}

	cfg := discovery.PipelineConfig{
		Registry: reg, Fetcher: store, Decrypt: decrypt,
		PingTimeout: 2 * time.Second, SNMPTimeout: 3 * time.Second,
	}
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
