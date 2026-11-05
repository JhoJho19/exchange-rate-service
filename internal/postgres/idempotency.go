package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/JhoJho19/exchange-rate-service/internal/apperrors"
	"github.com/JhoJho19/exchange-rate-service/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (r *TaskRepository) CreateOrGet(ctx context.Context, key string, pair models.CurrencyPair, prepare func() (models.UpdateTask, error)) (uuid.UUID, bool, error) {
	return r.create(ctx, &key, pair, prepare)
}

// All admissions serialize on the singleton row. READ COMMITTED gives the count
// and key lookup a fresh snapshot after the lock has been acquired.
func (r *TaskRepository) create(ctx context.Context, key *string, pair models.CurrencyPair, prepare func() (models.UpdateTask, error)) (uuid.UUID, bool, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return uuid.Nil, false, err
	}
	defer tx.Rollback(context.Background())
	var guard bool
	if err = tx.QueryRow(ctx, `SELECT singleton FROM task_queue_guard WHERE singleton = true FOR UPDATE`).Scan(&guard); err != nil {
		return uuid.Nil, false, fmt.Errorf("lock queue: %w", err)
	}
	lookup := func() (uuid.UUID, error) {
		var id uuid.UUID
		var existing models.CurrencyPair
		err := tx.QueryRow(ctx, `SELECT id, base_currency, quote_currency FROM update_tasks WHERE idempotency_key=$1`, key).Scan(&id, &existing.BaseCurrency, &existing.QuoteCurrency)
		if err == nil && existing != pair {
			err = apperrors.ErrIdempotencyConflict
		}
		return id, err
	}
	if key != nil {
		id, err := lookup()
		if !errors.Is(err, pgx.ErrNoRows) {
			return id, false, err
		}
	}
	var count int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM update_tasks WHERE status IN ('PENDING','PROCESSING')`).Scan(&count); err != nil {
		return uuid.Nil, false, err
	}
	if count >= r.queueSize {
		return uuid.Nil, false, apperrors.ErrQueueFull
	}
	task, err := prepare()
	if err != nil {
		return uuid.Nil, false, err
	}
	if task.Status != models.StatusPending || task.Pair != pair {
		return uuid.Nil, false, errors.New("invalid prepared task")
	}
	var id uuid.UUID
	err = tx.QueryRow(ctx, `INSERT INTO update_tasks(id,base_currency,quote_currency,status,created_at,idempotency_key)
 VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(idempotency_key) DO NOTHING RETURNING id`, task.ID, pair.BaseCurrency, pair.QuoteCurrency, task.Status, task.CreatedAt, key).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		id, err = lookup()
		return id, false, err
	}
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("insert task: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return uuid.Nil, false, err
	}
	return id, true, nil
}
