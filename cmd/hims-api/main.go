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
	srv := api.NewServer(queries)

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
