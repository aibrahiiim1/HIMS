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

	"github.com/coralsearesorts/hims/internal/credresolver"
	"github.com/coralsearesorts/hims/internal/discovery"
	"github.com/coralsearesorts/hims/internal/drivers"
	"github.com/coralsearesorts/hims/internal/monitoring"
	"github.com/coralsearesorts/hims/internal/storage/postgres"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	target := flag.String("ip", "", "single IP to discover (one-shot mode)")
	monitor := flag.Bool("monitor", false, "run the scheduled monitoring loop")
	seed := flag.Bool("seed", false, "seed default monitoring checks, then exit")
	tick := flag.Duration("tick", 30*time.Second, "monitoring sweep interval")
	flag.Parse()

	reg := drivers.Builtin()
	slog.Info("hims-collector", "drivers", reg.Names())

	if *monitor || *seed {
		runMonitoring(*monitor, *seed, *tick)
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

	engine := monitoring.NewEngine(db.New(pool), monitoring.NewPoller(nil, 3*time.Second), slog.Default())

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
