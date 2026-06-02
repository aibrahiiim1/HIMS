// Package postgres is HIMS's storage layer: a thin set of repositories over
// the sqlc-generated db package, plus bridges that adapt rows into the
// domain/engine types (e.g. feeding the credential resolver).
package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/coralsearesorts/hims/internal/domain"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

// Store bundles the repositories around a pgx pool.
type Store struct {
	pool *pgxpool.Pool
	q    *db.Queries
}

// PoolConfig configures the connection pool.
type PoolConfig struct {
	URL          string
	MaxOpenConns int32
}

// NewPool opens a pgx pool.
func NewPool(ctx context.Context, cfg PoolConfig) (*pgxpool.Pool, error) {
	pc, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		return nil, err
	}
	if cfg.MaxOpenConns > 0 {
		pc.MaxConns = cfg.MaxOpenConns
	}
	return pgxpool.NewWithConfig(ctx, pc)
}

// New builds a Store over an existing pool.
func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, q: db.New(pool)}
}

// mapErr translates pgx errors into domain sentinels.
func mapErr(resource string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return &domain.NotFoundError{Resource: resource}
	}
	return err
}

// stamp is a tiny helper for callers that need a poll/observed time without
// importing time everywhere.
func stamp() time.Time { return time.Now().UTC() }
