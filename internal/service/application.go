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
	workerPool          *WorkerPool
}

func NewApplicationService(supportedCurrencies map[string]struct{}, taskRepository TaskRepository, workerPool *WorkerPool) *ApplicationService {
	return &ApplicationService{
		supportedCurrencies: supportedCurrencies,
		taskRepository:      taskRepository,
		workerPool:          workerPool,
	}
}

func (s *ApplicationService) CreateUpdate(ctx context.Context, base string, quote string,
) (uuid.UUID, error) {

	pair, err := models.NewCurrencyPair(base, quote, s.supportedCurrencies)
	if err != nil {
		return uuid.Nil, err
	}

	id := uuid.New()

	reserved, err := s.workerPool.reserveQueueSlot(id, false)
	if err != nil {
		return uuid.Nil, err
	}
	if !reserved {
		return uuid.Nil, ErrQueueFull
	}

	defer s.workerPool.producers.Done()
	// Forced shutdown also interrupts persistence, without coupling worker work
	// to the incoming request context after persistence succeeds.
	ctx, cancel := context.WithCancel(ctx)
	stopCancel := context.AfterFunc(s.workerPool.lifecycle, cancel)
	defer stopCancel()
	defer cancel()

	task := models.UpdateTask{
		ID:        id,
		Pair:      pair,
		Status:    models.StatusPending,
		CreatedAt: time.Now().UTC(),
	}

	err = s.taskRepository.Create(ctx, task)
	if err != nil {
		s.workerPool.releaseQueueSlot()
		s.workerPool.forget(id)
		return uuid.Nil, err
	}

	// Once persisted, enqueue even if the request context has been canceled.
	s.workerPool.enqueueReserved(task)

	return id, nil
}

func (s *ApplicationService) GetUpdate(ctx context.Context, id uuid.UUID,
) (models.UpdateTask, error) {
	return s.taskRepository.GetByID(ctx, id)
}

func (s *ApplicationService) GetLatestQuote(ctx context.Context, base string, quote string,
) (models.Quote, error) {

	pair, err := models.NewCurrencyPair(
		base,
		quote,
		s.supportedCurrencies,
	)

	if err != nil {
		return models.Quote{}, err
	}

	return s.taskRepository.GetLatestQuote(ctx, pair)
}
