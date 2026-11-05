package service

import (
	"context"
	"time"

	"github.com/JhoJho19/exchange-rate-service/internal/models"

	"github.com/google/uuid"
)

type TaskRepository interface {
	// CreateOrGet checks the key before database queue capacity.
	// prepare generates a task; it must not call the repository.
	CreateOrGet(ctx context.Context, key string, pair models.CurrencyPair, prepare func() (models.UpdateTask, error)) (uuid.UUID, bool, error)

	Create(ctx context.Context, task models.UpdateTask) error // create a new update task in the database with a pending status

	GetByID(ctx context.Context, id uuid.UUID) (models.UpdateTask, error) // get an update task by its ID

	GetLatestQuote(ctx context.Context, pair models.CurrencyPair) (models.Quote, error) // get the latest quote for a given currency pair

	ClaimNextTask(context.Context, time.Duration) (models.UpdateTask, bool, error)
	RenewLease(context.Context, uuid.UUID, uuid.UUID, time.Duration) (bool, error)

	CompleteTask(ctx context.Context, id uuid.UUID, token uuid.UUID, quote models.Quote, completedAt time.Time) (bool, error)

	FailTask(ctx context.Context, id uuid.UUID, token uuid.UUID, errorMessage string, completedAt time.Time) (bool, error)
}
