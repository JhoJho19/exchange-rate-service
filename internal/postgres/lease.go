package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/JhoJho19/exchange-rate-service/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ClaimNextTask uses one statement and database time; no connection is retained.
func (r *TaskRepository) ClaimNextTask(ctx context.Context, duration time.Duration) (models.UpdateTask, bool, error) {
	token := uuid.New()
	if duration < time.Microsecond || token == uuid.Nil {
		return models.UpdateTask{}, false, errors.New("invalid lease")
	}
	var task models.UpdateTask
	err := r.pool.QueryRow(ctx, `WITH candidate AS (
 SELECT id FROM update_tasks
 WHERE (status = 'PENDING' OR (status = 'PROCESSING' AND leased_until <= clock_timestamp()))
 ORDER BY created_at, id FOR UPDATE SKIP LOCKED LIMIT 1
 ) UPDATE update_tasks AS t
 SET status = 'PROCESSING', lease_token = $1,
 leased_until = clock_timestamp() + $2 * interval '1 microsecond'
 FROM candidate WHERE t.id = candidate.id
 RETURNING t.id, t.base_currency, t.quote_currency, t.status, t.created_at, t.lease_token, t.leased_until`,
		token, duration.Microseconds()).Scan(&task.ID, &task.Pair.BaseCurrency, &task.Pair.QuoteCurrency, &task.Status, &task.CreatedAt, &task.LeaseToken, &task.LeasedUntil)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.UpdateTask{}, false, nil
	}
	if err != nil {
		return models.UpdateTask{}, false, fmt.Errorf("claim task: %w", err)
	}
	return task, true, nil
}

func (r *TaskRepository) RenewLease(ctx context.Context, id, token uuid.UUID, duration time.Duration) (bool, error) {
	if duration < time.Microsecond {
		return false, errors.New("invalid lease duration")
	}
	tag, err := r.pool.Exec(ctx, `UPDATE update_tasks
 SET leased_until = clock_timestamp() + $3 * interval '1 microsecond'
 WHERE id = $1 AND lease_token = $2 AND status = 'PROCESSING'
 AND leased_until > clock_timestamp()`, id, token, duration.Microseconds())
	if err != nil {
		return false, fmt.Errorf("renew task lease: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}
