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
	ctx, cancel := context.WithCancel(ctx)
	stopCancel := context.AfterFunc(s.workerPool.lifecycle, cancel)
	defer stopCancel()
	defer cancel()

	var task models.UpdateTask
	reserved := false
	enqueued := false
	defer func() {
		if reserved {
			if !enqueued {
				s.workerPool.releaseQueueSlot()
				s.workerPool.forget(task.ID)
			}
			s.workerPool.producers.Done()
		}
	}()
	prepare := func() (models.UpdateTask, error) {
		task = models.UpdateTask{ID: uuid.New(), Pair: pair, Status: models.StatusPending, CreatedAt: time.Now().UTC()}
		var err error
		reserved, err = s.workerPool.reserveQueueSlot(task.ID, false)
		if err != nil {
			return models.UpdateTask{}, err
		}
		if !reserved {
			return models.UpdateTask{}, ErrQueueFull
		}
		return task, nil
	}

	var id uuid.UUID
	created := true
	var err error
	if key == nil {
		if _, err = prepare(); err == nil {
			err = s.taskRepository.Create(ctx, task)
			id = task.ID
		}
	} else {
		id, created, err = s.taskRepository.CreateOrGet(ctx, *key, pair, prepare)
	}
	if err != nil {
		return uuid.Nil, err
	}
	if created {
		// The reservation guarantees room, even during graceful shutdown or
		// after request cancellation. Recovery retains responsibility on a crash.
		s.workerPool.enqueueReserved(task)
		enqueued = true
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
