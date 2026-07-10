// Package postgres creates a pgx connection pool and applies the lab's minimal
// schema. Orders and worker share it: orders inserts, worker updates.
package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Schema is applied idempotently on startup. Note: no raw PII is stored — the
// card is reduced to its last four digits before insert (see orders service).
const Schema = `
CREATE TABLE IF NOT EXISTS orders (
    id           UUID PRIMARY KEY,
    customer_id  TEXT        NOT NULL,
    amount_cents BIGINT      NOT NULL,
    currency     TEXT        NOT NULL,
    card_last4   TEXT        NOT NULL,
    status       TEXT        NOT NULL DEFAULT 'pending',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS orders_status_idx ON orders (status);
`

// Connect opens a pool and applies the schema.
func Connect(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("pgx pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	// Serialize schema creation across services: CREATE TABLE/INDEX IF NOT
	// EXISTS is not safe under concurrent DDL (Postgres can raise a duplicate
	// pg_type error). A transaction-scoped advisory lock makes it single-writer.
	if err := applySchema(ctx, pool); err != nil {
		pool.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return pool, nil
}

// applySchema runs the DDL inside a transaction guarded by an advisory lock so
// concurrently-starting services don't race on CREATE ... IF NOT EXISTS.
func applySchema(ctx context.Context, pool *pgxpool.Pool) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Arbitrary but stable lock key shared by all services.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(4242424242)`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, Schema); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
