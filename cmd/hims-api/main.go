// Command hims-api is the HIMS REST API server.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"runtime/debug"
	"time"

	"github.com/coralsearesorts/hims/internal/api"
	"github.com/coralsearesorts/hims/internal/drivers"
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
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	// Driver registry — logged at startup so ops can confirm the build.
	reg := drivers.Builtin()
	slog.Info("drivers registered", "names", reg.Names())

	// Postgres pool (URL from env; skip connection during development if unset).
	dbURL := os.Getenv("HIMS_DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://hims:hims@localhost:5432/hims?sslmode=disable"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := postgres.NewPool(ctx, postgres.PoolConfig{URL: dbURL, MaxOpenConns: 20})
	if err != nil {
		slog.Warn("database unavailable at startup; continuing without storage", "error", err)
		// In Phase 1 development without a local DB, still start the API.
		fmt.Println("hims-api: running in no-db mode (set HIMS_DATABASE_URL to connect)")
		http.ListenAndServe(":8090", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, `{"status":"ok","db":"unavailable"}`)
		}))
		return
	}
	defer pool.Close()

	queries := db.New(pool)

	// Encryption-at-rest for credential secrets. Without a key the API still
	// serves; credential writes return 503 until HIMS_ENCRYPTION_KEY is set.
	var cipher *secret.Cipher
	if k := os.Getenv("HIMS_ENCRYPTION_KEY"); k != "" {
		c, err := secret.NewCipher(k)
		if err != nil {
			// Degrade rather than crash-loop: the API still serves everything
			// that doesn't need the key, and Encryption Status reports
			// "invalid_key" with a clear reason. The error never includes the key.
			slog.Error("invalid HIMS_ENCRYPTION_KEY; credential encryption disabled", "error", err)
		} else {
			cipher = c
			slog.Info("credential encryption enabled", "key_id", c.KeyID())
		}
	} else {
		slog.Warn("HIMS_ENCRYPTION_KEY not set; credential writes disabled")
	}

	addr := os.Getenv("HIMS_ADDR")
	if addr == "" {
		addr = ":8090"
	}

	// Single-instance guard: claim the listen socket BEFORE wiring the server.
	// If another hims-api already owns the port, fail fast with a clear message
	// instead of silently leaving a second, conflicting process around (which
	// produced ambiguous encryption states when a no-key and a keyed instance
	// both ran).
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		slog.Error("cannot bind listen address; another hims-api may already be running", "addr", addr, "error", err)
		fmt.Fprintf(os.Stderr, "\nhims-api: address %s is already in use — another hims-api instance is probably running.\n", addr)
		fmt.Fprintf(os.Stderr, "Stop it first, then retry:\n")
		fmt.Fprintf(os.Stderr, "  PowerShell:  Get-Process hims-api | Stop-Process -Force\n")
		fmt.Fprintf(os.Stderr, "  Linux/macOS: pkill hims-api   (or: fuser -k %s/tcp)\n", addr)
		fmt.Fprintf(os.Stderr, "Or set HIMS_ADDR to a free port.\n\n")
		os.Exit(1)
	}

	// drivers + credential scope-resolver enable operator-launched scans.
	srv := api.NewServer(queries, cipher, reg, postgres.New(pool))
	srv.SetRuntime(api.RuntimeInfo{
		StartedAt: time.Now(),
		Version:   version,
		Commit:    gitCommit(),
		Addr:      addr,
		DBURL:     dbURL,
		Env:       getenvDefault("HIMS_ENV", "development"),
	})

	slog.Info("hims-api starting", "addr", addr, "pid", os.Getpid(), "version", version, "commit", gitCommit())
	if err := http.Serve(ln, srv); err != nil {
		slog.Error("server failed", "error", err)
		os.Exit(1)
	}
}
