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
	o, err := r.ownership()
	if err != nil {
		return uuid.Nil, false, err
	}
	var id uuid.UUID
	created := false
	err = o.withConnection(ctx, func(ctx context.Context) error {
		if !o.prepared {
			return errors.New("recovery must be prepared before task writes")
		}
		lookup := func() error {
			var existing models.CurrencyPair
			err := o.conn.QueryRow(ctx, `SELECT id, base_currency, quote_currency FROM update_tasks WHERE idempotency_key = $1`, key).Scan(&id, &existing.BaseCurrency, &existing.QuoteCurrency)
			if err != nil {
				return err
			}
			if existing != pair {
				return apperrors.ErrIdempotencyConflict
			}
			return nil
		}
		// The existing ownership gate serializes application writers. Look up first
		// so repeats neither generate a UUID nor require available queue capacity.
		if err := lookup(); !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		task, err := prepare()
		if err != nil {
			return err
		}
		if task.Status != models.StatusPending || task.Pair != pair {
			return errors.New("invalid prepared task")
		}
		err = o.conn.QueryRow(ctx, `INSERT INTO update_tasks
   (id, base_currency, quote_currency, status, created_at, idempotency_key)
   VALUES ($1, $2, $3, $4, $5, $6)
   ON CONFLICT (idempotency_key) DO NOTHING RETURNING id`,
			task.ID, pair.BaseCurrency, pair.QuoteCurrency, task.Status, task.CreatedAt, key).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			// A separate autocommit statement gets a fresh snapshot after INSERT has
			// waited for a competing transaction. Never update an existing task.
			return lookup()
		}
		created = err == nil
		return err
	})
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("create or get task: %w", err)
	}
	return id, created, nil
}
