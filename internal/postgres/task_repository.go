package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/JhoJho19/exchange-rate-service/internal/apperrors"
	"github.com/JhoJho19/exchange-rate-service/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type TaskRepository struct {
	pool      *pgxpool.Pool
	queueSize int
}

func NewTaskRepository(pool *pgxpool.Pool, queueSize int) *TaskRepository {
	if queueSize <= 0 {
		panic("queue size must be positive")
	}
	return &TaskRepository{
		pool: pool, queueSize: queueSize,
	}
}

func (r *TaskRepository) Create(ctx context.Context, task models.UpdateTask) error {
	if task.Status != models.StatusPending {
		return fmt.Errorf("new task must have PENDING status")
	}

	_, _, err := r.create(ctx, nil, task.Pair, func() (models.UpdateTask, error) { return task, nil })
	return err
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
			error_message,
			lease_token,
			leased_until
		FROM update_tasks
		WHERE id = $1
	`

	var task models.UpdateTask

	var rate sql.NullFloat64
	var updatedAt sql.NullTime
	var completedAt sql.NullTime
	var errorMessage sql.NullString
	var leaseToken uuid.NullUUID
	var leasedUntil sql.NullTime

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
		&leaseToken,
		&leasedUntil,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.UpdateTask{}, fmt.Errorf(
				"task %s: %w",
				id,
				apperrors.ErrTaskNotFound,
			)
		}

		return models.UpdateTask{}, fmt.Errorf("failed to get task by ID: %w", err)
	}

	if leaseToken.Valid {
		task.LeaseToken = leaseToken.UUID
	}
	if leasedUntil.Valid {
		task.LeasedUntil = leasedUntil.Time
	}

	if rate.Valid != updatedAt.Valid {
		return models.UpdateTask{}, fmt.Errorf("task %s has incomplete quote data", id)
	}

	if rate.Valid && updatedAt.Valid {
		task.Quote = &models.Quote{
			Pair:      task.Pair,
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

func (r *TaskRepository) GetLatestQuote(ctx context.Context, pair models.CurrencyPair,
) (models.Quote, error) {
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
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Quote{}, fmt.Errorf(
				"currency pair %s/%s: %w",
				pair.BaseCurrency,
				pair.QuoteCurrency,
				apperrors.ErrQuoteNotFound,
			)
		}

		return models.Quote{}, fmt.Errorf(
			"failed to get latest quote: %w",
			err,
		)
	}

	quote.Pair = pair

	return quote, nil
}

func (r *TaskRepository) CompleteTask(ctx context.Context, id uuid.UUID, token uuid.UUID, quote models.Quote, completedAt time.Time) (bool, error) {
	const query = `
        UPDATE update_tasks
        SET
            status = 'COMPLETED',
            rate = $2,
            updated_at = $3,
            completed_at = $4,
            error_message = NULL, leased_until = NULL, lease_token = NULL
        WHERE id = $1
          AND status = 'PROCESSING' AND lease_token = $5 AND leased_until > clock_timestamp()
    `

	commandTag, err := r.pool.Exec(
		ctx,
		query,
		id,
		quote.Rate,
		quote.UpdatedAt,
		completedAt,
		token,
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

func (r *TaskRepository) FailTask(ctx context.Context, id uuid.UUID, token uuid.UUID, errorMessage string, completedAt time.Time) (bool, error) {
	const query = `
        UPDATE update_tasks
        SET
            status = 'FAILED',
            rate = NULL,
            updated_at = NULL,
            completed_at = $3,
            error_message = $2, leased_until = NULL, lease_token = NULL
        WHERE id = $1
          AND status = 'PROCESSING' AND lease_token = $4 AND leased_until > clock_timestamp()
    `

	commandTag, err := r.pool.Exec(
		ctx,
		query,
		id,
		errorMessage,
		completedAt,
		token,
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
