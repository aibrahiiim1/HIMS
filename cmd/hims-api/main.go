// Command hims-api is the HIMS REST API server.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/coralsearesorts/hims/internal/api"
	"github.com/coralsearesorts/hims/internal/drivers"
	"github.com/coralsearesorts/hims/internal/secret"
	"github.com/coralsearesorts/hims/internal/storage/postgres"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

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
			slog.Error("invalid HIMS_ENCRYPTION_KEY", "error", err)
			os.Exit(1)
		}
		cipher = c
		slog.Info("credential encryption enabled", "key_id", c.KeyID())
	} else {
		slog.Warn("HIMS_ENCRYPTION_KEY not set; credential writes disabled")
	}

	srv := api.NewServer(queries, cipher)

	addr := os.Getenv("HIMS_ADDR")
	if addr == "" {
		addr = ":8090"
	}
	slog.Info("hims-api starting", "addr", addr)
	if err := http.ListenAndServe(addr, srv); err != nil {
		slog.Error("server failed", "error", err)
		os.Exit(1)
	}
}
