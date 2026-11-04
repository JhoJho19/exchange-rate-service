package service

import (
	"context"
	"time"

	"github.com/JhoJho19/exchange-rate-service/internal/models"

	"github.com/google/uuid"
)

type TaskRepository interface {
	// CreateOrGet calls prepare only for an absent key, while serializing creation.
	// prepare generates the task and reserves queue capacity; it must not call the repository.
	CreateOrGet(ctx context.Context, key string, pair models.CurrencyPair, prepare func() (models.UpdateTask, error)) (uuid.UUID, bool, error)

	Create(ctx context.Context, task models.UpdateTask) error // create a new update task in the database with a pending status

	GetByID(ctx context.Context, id uuid.UUID) (models.UpdateTask, error) // get an update task by its ID

	GetLatestQuote(ctx context.Context, pair models.CurrencyPair) (models.Quote, error) // get the latest quote for a given currency pair

	TryStartProcessing(ctx context.Context, id uuid.UUID) (bool, error)

	CompleteTask(ctx context.Context, id uuid.UUID, quote models.Quote, completedAt time.Time) (bool, error)

	FailTask(ctx context.Context, id uuid.UUID, errorMessage string, completedAt time.Time) (bool, error)
}
