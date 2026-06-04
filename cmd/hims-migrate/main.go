// Command hims-migrate applies HIMS database migrations from the embedded
// migration set, tracking applied versions in schema_migrations. It is the
// install/upgrade step: run `hims-migrate up` before starting hims-api.
//
// Usage:
//
//	hims-migrate status     # show applied + pending migrations
//	hims-migrate up         # apply pending migrations (default)
//	hims-migrate baseline   # record all migrations as applied WITHOUT running
//	                        # them — adopt an existing, hand-migrated database
//
// Configuration: HIMS_DATABASE_URL (postgres://…).
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/coralsearesorts/hims/internal/migrate"
	"github.com/coralsearesorts/hims/internal/storage/postgres"
	"github.com/coralsearesorts/hims/migrations"
)

func main() {
	cmd := "up"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	dbURL := os.Getenv("HIMS_DATABASE_URL")
	if dbURL == "" {
		fail("HIMS_DATABASE_URL is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, err := postgres.NewPool(ctx, postgres.PoolConfig{URL: dbURL})
	if err != nil {
		fail("connect: " + err.Error())
	}
	defer pool.Close()

	r := migrate.New(pool, migrations.FS)
	switch cmd {
	case "status":
		all, applied, err := r.Status(ctx)
		if err != nil {
			fail(err.Error())
		}
		pending := 0
		for _, m := range all {
			mark := "applied"
			if !applied[m.Version] {
				mark = "PENDING"
				pending++
			}
			fmt.Printf("  %s  %s  %s\n", m.Version, mark, m.Filename)
		}
		fmt.Printf("\n%d migration(s), %d pending\n", len(all), pending)
	case "up":
		done, err := r.Up(ctx)
		if err != nil {
			fail(err.Error())
		}
		if len(done) == 0 {
			fmt.Println("up to date — no pending migrations")
			return
		}
		fmt.Printf("applied %d migration(s): %v\n", len(done), done)
	case "baseline":
		done, err := r.Baseline(ctx)
		if err != nil {
			fail(err.Error())
		}
		fmt.Printf("baselined %d migration(s) as applied (not run): %v\n", len(done), done)
	default:
		fail("unknown command: " + cmd + " (use status|up|baseline)")
	}
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "hims-migrate: "+msg)
	os.Exit(1)
}
