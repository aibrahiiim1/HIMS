// Package migrate applies embedded SQL migrations against Postgres, tracking
// applied versions in a schema_migrations ledger. It is the installer/upgrade
// primitive behind the hims-migrate tool (#26): a clean install runs `up` to
// build the schema from zero; an existing hand-migrated DB runs `baseline`
// once to adopt the ledger, after which `up` only applies genuinely new files.
package migrate

import (
	"context"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Migration is one embedded up-migration.
type Migration struct {
	Version  string // e.g. "000034"
	Filename string
}

// versionOf extracts the leading numeric version from a migration filename
// ("000034_netflow.up.sql" → "000034"). Returns "" if the name doesn't match.
func versionOf(name string) string {
	base := name
	if i := strings.IndexByte(base, '_'); i > 0 {
		return base[:i]
	}
	if i := strings.IndexByte(base, '.'); i > 0 {
		return base[:i]
	}
	return ""
}

// Load reads + sorts the up-migrations from an embedded FS.
func Load(fsys fs.FS) ([]Migration, error) {
	entries, err := fs.Glob(fsys, "*.up.sql")
	if err != nil {
		return nil, err
	}
	out := make([]Migration, 0, len(entries))
	for _, e := range entries {
		v := versionOf(e)
		if v == "" {
			return nil, fmt.Errorf("migrate: cannot parse version from %q", e)
		}
		out = append(out, Migration{Version: v, Filename: e})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

// Pending returns the migrations not present in the applied set, in order.
func Pending(all []Migration, applied map[string]bool) []Migration {
	var p []Migration
	for _, m := range all {
		if !applied[m.Version] {
			p = append(p, m)
		}
	}
	return p
}

// Runner applies migrations using a pgx pool + an embedded FS.
type Runner struct {
	pool *pgxpool.Pool
	fsys fs.FS
}

func New(pool *pgxpool.Pool, fsys fs.FS) *Runner { return &Runner{pool: pool, fsys: fsys} }

const ddl = `CREATE TABLE IF NOT EXISTS schema_migrations (
    version    TEXT PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
)`

func (r *Runner) ensureLedger(ctx context.Context) error {
	_, err := r.pool.Exec(ctx, ddl)
	return err
}

// Applied returns the set of versions already recorded in the ledger.
func (r *Runner) Applied(ctx context.Context) (map[string]bool, error) {
	if err := r.ensureLedger(ctx); err != nil {
		return nil, err
	}
	rows, err := r.pool.Query(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out[v] = true
	}
	return out, rows.Err()
}

// Status returns all migrations + which are applied.
func (r *Runner) Status(ctx context.Context) ([]Migration, map[string]bool, error) {
	all, err := Load(r.fsys)
	if err != nil {
		return nil, nil, err
	}
	applied, err := r.Applied(ctx)
	if err != nil {
		return nil, nil, err
	}
	return all, applied, nil
}

// Up applies every pending migration in order, each in its own transaction, and
// records it in the ledger. Returns the versions applied.
func (r *Runner) Up(ctx context.Context) ([]string, error) {
	all, applied, err := r.Status(ctx)
	if err != nil {
		return nil, err
	}
	var done []string
	for _, m := range Pending(all, applied) {
		sqlBytes, err := fs.ReadFile(r.fsys, m.Filename)
		if err != nil {
			return done, err
		}
		if err := r.applyOne(ctx, m.Version, string(sqlBytes)); err != nil {
			return done, fmt.Errorf("migrate %s: %w", m.Version, err)
		}
		done = append(done, m.Version)
	}
	return done, nil
}

func (r *Runner) applyOne(ctx context.Context, version, sqlText string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, sqlText); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, version); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Baseline records all (or up to a version) embedded migrations as applied
// WITHOUT running them — for adopting an existing, already-migrated database
// into the ledger. Returns the versions recorded.
func (r *Runner) Baseline(ctx context.Context) ([]string, error) {
	all, applied, err := r.Status(ctx)
	if err != nil {
		return nil, err
	}
	var done []string
	for _, m := range all {
		if applied[m.Version] {
			continue
		}
		if _, err := r.pool.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1) ON CONFLICT DO NOTHING`, m.Version); err != nil {
			return done, err
		}
		done = append(done, m.Version)
	}
	return done, nil
}
