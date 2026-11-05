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

	return s.CreateUpdateWithKey(ctx, base, quote, nil)
}

func (s *ApplicationService) CreateUpdateWithKey(ctx context.Context, base, quote string, key *string) (uuid.UUID, error) {
	var pair, err = models.NewCurrencyPair(base, quote, s.supportedCurrencies)
	if err != nil {
		return uuid.Nil, err
	}
	return s.createUpdate(ctx, pair, key)
}

func (s *ApplicationService) createUpdate(ctx context.Context, pair models.CurrencyPair, key *string) (uuid.UUID, error) {
	prepare := func() (models.UpdateTask, error) {
		return models.UpdateTask{ID: uuid.New(), Pair: pair, Status: models.StatusPending, CreatedAt: time.Now().UTC()}, nil
	}
	var id uuid.UUID
	created := true
	var err error
	if key == nil {
		task, _ := prepare()
		err = s.taskRepository.Create(ctx, task)
		id = task.ID
	} else {
		id, created, err = s.taskRepository.CreateOrGet(ctx, *key, pair, prepare)
	}
	if err != nil {
		return uuid.Nil, err
	}
	if created {
		s.workerPool.Notify()
	}
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
