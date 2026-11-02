package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/JhoJho19/exchange-rate-service/internal/models"
	"github.com/google/uuid"
)

// ResetProcessing is idempotent per ownership session, and must finish before
// starting workers. Repeated calls cannot reset this session's active work.
func (r *TaskRepository) ResetProcessing(ctx context.Context) error {
	o, err := r.ownership()
	if err != nil {
		return err
	}
	return o.withConnection(ctx, func(ctx context.Context) error {
		if o.prepared {
			return nil
		}
		_, err := o.conn.Exec(ctx, `UPDATE update_tasks SET status = 'PENDING'
   WHERE status = 'PROCESSING'`)
		if err != nil {
			return fmt.Errorf("reset orphaned tasks: %w", err)
		}
		o.prepared = true
		return nil
	})
}

func (r *TaskRepository) PendingTasks(ctx context.Context, limit int, excluded []uuid.UUID) ([]models.UpdateTask, error) {
	if limit <= 0 {
		return nil, errors.New("recovery limit must be positive")
	}
	o, err := r.ownership()
	if err != nil {
		return nil, err
	}
	if excluded == nil {
		excluded = []uuid.UUID{}
	}
	var tasks []models.UpdateTask
	err = o.withConnection(ctx, func(ctx context.Context) error {
		if !o.prepared {
			return errors.New("recovery has not been prepared")
		}
		rows, err := o.conn.Query(ctx, `SELECT id, base_currency, quote_currency, status, created_at
   FROM update_tasks WHERE status = 'PENDING' AND NOT (id = ANY($2::uuid[]))
   ORDER BY created_at, id LIMIT $1`, limit, excluded)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var task models.UpdateTask
			if err := rows.Scan(&task.ID, &task.Pair.BaseCurrency, &task.Pair.QuoteCurrency, &task.Status, &task.CreatedAt); err != nil {
				return err
			}
			tasks = append(tasks, task)
		}
		return rows.Err()
	})
	return tasks, err
}
