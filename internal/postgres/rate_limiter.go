package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RateLimiterStore shares exact rolling windows across all instances using the
// same database. All instances must configure the same minute/hour limits.
type RateLimiterStore struct {
	pool      *pgxpool.Pool
	perMinute int
	perHour   int
}

func NewRateLimiterStore(pool *pgxpool.Pool, perMinute, perHour int) *RateLimiterStore {
	if pool == nil || perMinute <= 0 || perHour <= 0 {
		panic("rate limiter requires a database and positive limits")
	}
	return &RateLimiterStore{pool: pool, perMinute: perMinute, perHour: perHour}
}

// AcquirePermit commits an attempt or returns a database-derived wait duration.
// The guard serializes quota checks, inserts, pruning and cooldown updates.
// Windows are (now - window, now]; retries consume permits just like first calls.
func (s *RateLimiterStore) AcquirePermit(ctx context.Context) (time.Duration, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return 0, fmt.Errorf("begin rate limit check: %w", err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	var blocked time.Time
	if err = tx.QueryRow(ctx, `SELECT blocked_until FROM provider_rate_limit WHERE singleton=true FOR UPDATE`).Scan(&blocked); err != nil {
		return 0, fmt.Errorf("lock rate limiter: %w", err)
	}
	// Read time AFTER waiting for the guard, not transaction_timestamp()/now().
	var now time.Time
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return 0, err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM provider_attempts WHERE attempted_at <= $1::timestamptz - interval '1 hour'`, now); err != nil {
		return 0, err
	}
	var next time.Time
	// The Nth newest attempt determines when there will be fewer than N attempts
	// in each window, including after a configuration change lowering a quota.
	err = tx.QueryRow(ctx, `SELECT GREATEST($1::timestamptz, $2::timestamptz,
  (SELECT attempted_at + interval '1 minute' FROM provider_attempts
   ORDER BY attempted_at DESC OFFSET $3 LIMIT 1),
  (SELECT attempted_at + interval '1 hour' FROM provider_attempts
   ORDER BY attempted_at DESC OFFSET $4 LIMIT 1))`, now, blocked, s.perMinute-1, s.perHour-1).Scan(&next)
	if err != nil {
		return 0, err
	}
	delay := next.Sub(now)
	if delay <= 0 {
		if _, err = tx.Exec(ctx, `INSERT INTO provider_attempts(attempted_at) VALUES($1)`, now); err != nil {
			return 0, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit permit: %w", err)
	}
	return delay, nil
}

// PauseUntil atomically extends the shared cooldown and uses the same row lock
// as admission. Already committed permits cannot be revoked by a later 429.
func (s *RateLimiterStore) PauseUntil(ctx context.Context, until time.Time) error {
	tag, err := s.pool.Exec(ctx, `UPDATE provider_rate_limit SET blocked_until=GREATEST(blocked_until,$1::timestamptz) WHERE singleton=true`, until)
	if err != nil {
		return fmt.Errorf("pause provider: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("rate limiter guard is missing")
	}
	return nil
}
