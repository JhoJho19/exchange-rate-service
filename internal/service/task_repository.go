package service

import (
	"context"

	"github.com/JhoJho19/exchange-rate-service/internal/models"

	"github.com/google/uuid"
)

type TaskRepository interface {
	Create(ctx context.Context, task models.UpdateTask) error // create a new update task in the database with a pending status

	GetByID(ctx context.Context, id uuid.UUID) (models.UpdateTask, error) // get an update task by its ID

	MarkProcessing(ctx context.Context, id uuid.UUID) error // transfer task status from pending to processing

	MarkCompleted(ctx context.Context, id uuid.UUID, quote models.Quote) error // mark a task as completed

	MarkFailed(ctx context.Context, id uuid.UUID, errorMessage string) error // mark a task as failed

	GetLatestQuote(ctx context.Context, pair models.CurrencyPair) (models.Quote, error) // get the latest quote for a given currency pair
}
