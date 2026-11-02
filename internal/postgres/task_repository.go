package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	"github.com/JhoJho19/exchange-rate-service/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type TaskRepository struct {
	pool    *pgxpool.Pool
	ownerMu sync.RWMutex
	owner   *ownership
}

func NewTaskRepository(pool *pgxpool.Pool) *TaskRepository {
	return &TaskRepository{
		pool: pool,
	}
}

func (r *TaskRepository) Create(ctx context.Context, task models.UpdateTask) error {
	if task.Status != models.StatusPending {
		return fmt.Errorf("new task must have PENDING status")
	}

	const query = `
		INSERT INTO update_tasks (
			id,
			base_currency,
			quote_currency,
			status,
			created_at
		)
		VALUES ($1, $2, $3, $4, $5)
	`

	_, err := r.exec(
		ctx,
		query,
		task.ID,
		task.Pair.BaseCurrency,
		task.Pair.QuoteCurrency,
		task.Status,
		task.CreatedAt,
	)

	if err != nil {
		return fmt.Errorf("failed to create task: %w", err)
	}

	return nil
}

func (r *TaskRepository) GetByID(ctx context.Context, id uuid.UUID) (models.UpdateTask, error) {
	const query = `
		SELECT
			id,
			base_currency,
			quote_currency,
			status,
			rate,
			updated_at,
			created_at,
			completed_at,
			error_message
		FROM update_tasks
		WHERE id = $1
	`

	var task models.UpdateTask

	var rate sql.NullFloat64
	var updatedAt sql.NullTime
	var completedAt sql.NullTime
	var errorMessage sql.NullString

	err := r.pool.QueryRow(ctx, query, id).Scan(
		&task.ID,
		&task.Pair.BaseCurrency,
		&task.Pair.QuoteCurrency,
		&task.Status,
		&rate,
		&updatedAt,
		&task.CreatedAt,
		&completedAt,
		&errorMessage,
	)

	if err != nil {
		return models.UpdateTask{}, fmt.Errorf("failed to get task by ID: %w", err)
	}

	if rate.Valid != updatedAt.Valid {
		return models.UpdateTask{}, fmt.Errorf("task %s has incomplete quote data", id)
	}

	if rate.Valid && updatedAt.Valid {
		task.Quote = &models.Quote{
			Rate:      rate.Float64,
			UpdatedAt: updatedAt.Time,
		}
	}

	if completedAt.Valid {
		task.CompletedAt = &completedAt.Time
	}

	if errorMessage.Valid {
		task.ErrorMessage = errorMessage.String
	}

	return task, nil
}

func (r *TaskRepository) MarkProcessing(ctx context.Context, taskID uuid.UUID) error {
	const query = `
		UPDATE update_tasks
		SET status = 'PROCESSING'
		WHERE id = $1 AND status = 'PENDING'
	`

	cmdTag, err := r.exec(ctx, query, taskID)
	if err != nil {
		return fmt.Errorf("failed to mark task as processing: %w", err)
	}

	if cmdTag.RowsAffected() == 0 {
		return fmt.Errorf("no pending task found with ID: %s", taskID)
	}

	return nil
}

func (r *TaskRepository) MarkCompleted(ctx context.Context, taskID uuid.UUID, quote models.Quote) error {
	const query = `
		UPDATE update_tasks
		SET
			status = 'COMPLETED',
			rate = $2,
			updated_at = $3,
			completed_at = NOW()
		WHERE id = $1
			AND status = 'PROCESSING'
	`

	cmdTag, err := r.exec(
		ctx,
		query,
		taskID,
		quote.Rate,
		quote.UpdatedAt,
	)

	if err != nil {
		return fmt.Errorf("failed to mark task as completed: %w", err)
	}

	if cmdTag.RowsAffected() == 0 {
		return fmt.Errorf("no processing task found with ID: %s", taskID)
	}

	return nil
}

func (r *TaskRepository) MarkFailed(ctx context.Context, taskID uuid.UUID, errorMessage string) error {
	const query = `
		UPDATE update_tasks
		SET
			status = 'FAILED',
			error_message = $2,
			completed_at = NOW()
		WHERE id = $1
			AND status = 'PROCESSING'
	`

	cmdTag, err := r.exec(ctx, query, taskID, errorMessage)
	if err != nil {
		return fmt.Errorf("failed to mark task as failed: %w", err)
	}

	if cmdTag.RowsAffected() == 0 {
		return fmt.Errorf("no processing task found with ID: %s", taskID)
	}

	return nil
}

func (r *TaskRepository) GetLatestQuote(ctx context.Context, pair models.CurrencyPair) (models.Quote, error) {
	const query = `
		SELECT rate, updated_at
		FROM update_tasks
		WHERE base_currency = $1
			AND quote_currency = $2
			AND status = 'COMPLETED'
		ORDER BY updated_at DESC, id DESC
		LIMIT 1
	`

	var quote models.Quote

	err := r.pool.QueryRow(
		ctx,
		query,
		pair.BaseCurrency,
		pair.QuoteCurrency,
	).Scan(
		&quote.Rate,
		&quote.UpdatedAt,
	)

	if err != nil {
		return models.Quote{}, fmt.Errorf("failed to get latest quote: %w", err)
	}

	return quote, nil
}

func (r *TaskRepository) TryStartProcessing(ctx context.Context, id uuid.UUID) (bool, error) {
	const query = `
		UPDATE update_tasks
		SET status = 'PROCESSING'
		WHERE id = $1
		  AND status = 'PENDING'
	`

	commandTag, err := r.exec(ctx, query, id)
	if err != nil {
		return false, fmt.Errorf("failed to start processing task %s: %w", id, err)
	}

	return commandTag.RowsAffected() == 1, nil
}

func (r *TaskRepository) CompleteTask(ctx context.Context, id uuid.UUID, quote models.Quote, completedAt time.Time) (bool, error) {
	const query = `
        UPDATE update_tasks
        SET
            status = 'COMPLETED',
            rate = $2,
            updated_at = $3,
            completed_at = $4,
            error_message = NULL
        WHERE id = $1
          AND status = 'PROCESSING'
    `

	commandTag, err := r.exec(
		ctx,
		query,
		id,
		quote.Rate,
		quote.UpdatedAt,
		completedAt,
	)

	if err != nil {
		return false, fmt.Errorf(
			"failed to complete task %s: %w",
			id,
			err,
		)
	}

	return commandTag.RowsAffected() == 1, nil
}

func (r *TaskRepository) FailTask(ctx context.Context, id uuid.UUID, errorMessage string, completedAt time.Time) (bool, error) {
	const query = `
        UPDATE update_tasks
        SET
            status = 'FAILED',
            rate = NULL,
            updated_at = NULL,
            completed_at = $3,
            error_message = $2
        WHERE id = $1
          AND status = 'PROCESSING'
    `

	commandTag, err := r.exec(
		ctx,
		query,
		id,
		errorMessage,
		completedAt,
	)

	if err != nil {
		return false, fmt.Errorf(
			"failed to mark task %s as failed: %w",
			id,
			err,
		)
	}

	return commandTag.RowsAffected() == 1, nil
}
