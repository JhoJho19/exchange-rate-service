package service

import (
	"context"
	"time"

	"github.com/JhoJho19/exchange-rate-service/internal/models"
	"github.com/google/uuid"
)

type ApplicationService struct {
	supportedCurrencies map[string]struct{}
	taskRepository      TaskRepository
}

func NewApplicationService(supportedCurrencies map[string]struct{}, taskRepository TaskRepository) *ApplicationService {
	return &ApplicationService{
		supportedCurrencies: supportedCurrencies,
		taskRepository:      taskRepository,
	}
}

func (s *ApplicationService) CreateUpdate(ctx context.Context, base string, quote string,
) (uuid.UUID, error) {

	pair, err := models.NewCurrencyPair(base, quote, s.supportedCurrencies)
	if err != nil {
		return uuid.Nil, err
	}

	id := uuid.New()

	task := models.UpdateTask{
		ID:        id,
		Pair:      pair,
		Status:    models.StatusPending,
		CreatedAt: time.Now().UTC(),
	}

	err = s.taskRepository.Create(ctx, task)
	if err != nil {
		return uuid.Nil, err
	}

	return id, nil
}
