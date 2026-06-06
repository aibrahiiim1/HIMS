// Command hims-api is the HIMS REST API server.
//
// It runs identically as a foreground process, a Windows service (Service
// Control Manager), or under systemd/Docker on Linux. main() only decides HOW it
// was launched; run() holds the actual startup so every mode shares one code path.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"strconv"
	"syscall"
	"time"

	"github.com/coralsearesorts/hims/internal/api"
	"github.com/coralsearesorts/hims/internal/drivers"
	"github.com/coralsearesorts/hims/internal/osinv"
	"github.com/coralsearesorts/hims/internal/secret"
	"github.com/coralsearesorts/hims/internal/storage/postgres"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

// Build identity. version may be set with -ldflags; commit is taken from the
// embedded VCS stamp (go build records it automatically from the git repo).
var (
	version = "dev"
	commit  = ""
)

func gitCommit() string {
	if commit != "" {
		return commit
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			if s.Key == "vcs.revision" {
				if len(s.Value) > 12 {
					return s.Value[:12]
				}
				return s.Value
			}
		}
	}
	return "unknown"
}

func getenvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	runConsole := flag.Bool("console", false, "force console/foreground mode (never run as a Windows service)")
	flag.Parse()

	if *showVersion {
		fmt.Printf("hims-api %s (%s)\n", version, gitCommit())
		return
	}

	// Windows service launch: when started by the SCM, hand control to the service
	// adapter (service_windows.go), which sets up file logging and calls run().
	if !*runConsole && runUnderServiceManager() {
		runAsService()
		return
	}

	// Foreground (interactive, systemd Type=simple, or Docker): log JSON to stdout
	// so journald / `docker logs` capture it. Cancel on SIGINT/SIGTERM for a clean
	// stop under systemd/Docker.
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	mode := getenvDefault("HIMS_SERVICE_MODE", "foreground") // systemd/docker set this explicitly
	if err := run(ctx, mode, getenvDefault("HIMS_LOG_FILE", "")); err != nil {
		slog.Error("hims-api exited with error", "error", err)
		os.Exit(1)
	}
}

// run wires and serves the API until ctx is cancelled, then shuts down
// gracefully. It is the single startup path shared by console + service modes.
func run(ctx context.Context, serviceMode, logPath string) error {
	// Driver registry — logged at startup so ops can confirm the build.
	reg := drivers.Builtin()
	slog.Info("drivers registered", "names", reg.Names())
	slog.Info("winrm client configured", osinv.WinRMClientInfo()...)

	dbURL := getenvDefault("HIMS_DATABASE_URL", "postgres://hims:hims@localhost:5432/hims?sslmode=disable")

	connCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	pool, err := postgres.NewPool(connCtx, postgres.PoolConfig{URL: dbURL, MaxOpenConns: 20})
	cancel()
	if err != nil {
		// A production service must not silently serve a broken no-DB mode; only the
		// interactive/dev foreground path tolerates a missing database.
		if serviceMode != "foreground" {
			return fmt.Errorf("database unavailable (HIMS_DATABASE_URL): %w", err)
		}
		slog.Warn("database unavailable at startup; continuing without storage", "error", err)
		fmt.Println("hims-api: running in no-db mode (set HIMS_DATABASE_URL to connect)")
		srv := &http.Server{Addr: getenvDefault("HIMS_ADDR", ":8090"), Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, `{"status":"ok","db":"unavailable"}`)
		})}
		return serveUntil(ctx, srv, nil)
	}
	defer pool.Close()

	queries := db.New(pool)

	// Encryption-at-rest for credential secrets.
	var cipher *secret.Cipher
	if k := os.Getenv("HIMS_ENCRYPTION_KEY"); k != "" {
		c, cerr := secret.NewCipher(k)
		if cerr != nil {
			slog.Error("invalid HIMS_ENCRYPTION_KEY", "error", cerr) // never logs the key
		} else {
			cipher = c
			slog.Info("credential encryption enabled", "key_id", c.KeyID())
		}
	} else {
		slog.Warn("HIMS_ENCRYPTION_KEY not set")
	}

	// STARTUP GUARD: refuse to start if encrypted credentials already exist but no
	// usable key is loaded — starting would silently break decryption and could let
	// an operator overwrite/lose encrypted secrets. A fresh install (0 encrypted
	// rows) still starts so the key can be set later.
	if cipher == nil {
		gctx, gcancel := context.WithTimeout(ctx, 5*time.Second)
		n, cntErr := queries.CountEncryptedCredentials(gctx)
		gcancel()
		if cntErr == nil && n > 0 {
			fmt.Fprintf(os.Stderr, "\nhims-api: REFUSING TO START — %d encrypted credential(s) exist but HIMS_ENCRYPTION_KEY is missing or invalid.\n", n)
			fmt.Fprintf(os.Stderr, "Set the same HIMS_ENCRYPTION_KEY this data was sealed with, then restart.\n\n")
			return fmt.Errorf("encryption key required: %d encrypted credential(s) exist but no valid HIMS_ENCRYPTION_KEY", n)
		}
	}

	addr := getenvDefault("HIMS_ADDR", ":8090")

	// Single-instance guard: claim the listen socket before wiring the server.
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nhims-api: address %s is already in use — another hims-api instance is probably running.\n", addr)
		fmt.Fprintf(os.Stderr, "  PowerShell:  Get-Process hims-api | Stop-Process -Force\n")
		fmt.Fprintf(os.Stderr, "  Linux:       sudo systemctl stop hims-api   (or: pkill hims-api)\n")
		return fmt.Errorf("cannot bind %s: %w", addr, err)
	}

	srv := api.NewServer(queries, cipher, reg, postgres.New(pool))
	srv.SetRuntime(api.RuntimeInfo{
		StartedAt:   time.Now(),
		Version:     version,
		Commit:      gitCommit(),
		Addr:        addr,
		DBURL:       dbURL,
		Env:         getenvDefault("HIMS_ENV", "development"),
		ServiceMode: serviceMode,
		LogPath:     logPath,
	})

	// Background workers, all bound to the run context so they stop on shutdown.
	srv.StartTopologyRebuilder(ctx, 10*time.Minute)
	srv.StartMonitoring(ctx, 30*time.Second)
	srv.StartNotifier(ctx, 30*time.Second)
	if err := srv.BootstrapAdmin(ctx, os.Getenv("HIMS_ADMIN_USER"), os.Getenv("HIMS_ADMIN_PASSWORD")); err != nil {
		slog.Error("admin bootstrap failed", "error", err)
	}
	srv.StartSessionGC(ctx, time.Hour)
	srv.StartReportScheduler(ctx, 5*time.Minute)
	flowFlush := time.Minute
	if v := os.Getenv("HIMS_NETFLOW_FLUSH"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			flowFlush = time.Duration(n) * time.Second
		}
	}
	srv.StartFlowCollector(ctx, getenvDefault("HIMS_NETFLOW_ADDR", ":2055"), flowFlush)

	slog.Info("hims-api starting", "addr", addr, "pid", os.Getpid(), "version", version, "commit", gitCommit(), "service_mode", serviceMode)
	httpSrv := &http.Server{Handler: srv}
	return serveUntil(ctx, httpSrv, ln)
}

// serveUntil serves on ln (or httpSrv.Addr if ln is nil) until ctx is cancelled,
// then performs a graceful shutdown with a bounded timeout.
func serveUntil(ctx context.Context, httpSrv *http.Server, ln net.Listener) error {
	errCh := make(chan error, 1)
	go func() {
		if ln != nil {
			errCh <- httpSrv.Serve(ln)
		} else {
			errCh <- httpSrv.ListenAndServe()
		}
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		slog.Info("shutdown signal received; draining connections")
		sdCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(sdCtx)
		return nil
	}
}
