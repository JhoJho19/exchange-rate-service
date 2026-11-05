package service

import (
	"context"
	"sync"
	"time"

	"github.com/JhoJho19/exchange-rate-service/internal/models"
	"github.com/google/uuid"
)

type queueTaskRepository struct {
	fakeTaskRepository
	mu       sync.Mutex
	tasks    []models.UpdateTask
	claim    func(context.Context, uuid.UUID) (bool, error)
	create   func(context.Context, models.UpdateTask) error
	complete func(uuid.UUID, models.Quote)
	fail     func(uuid.UUID, string)
}

func (r *queueTaskRepository) Create(ctx context.Context, task models.UpdateTask) error {
	if r.create != nil {
		if err := r.create(ctx, task); err != nil {
			return err
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tasks = append(r.tasks, task)
	return nil
}

func (r *queueTaskRepository) CompleteTask(_ context.Context, id uuid.UUID, token uuid.UUID, quote models.Quote, _ time.Time) (bool, error) {
	r.complete(id, quote)
	return true, nil
}

func (r *queueTaskRepository) FailTask(_ context.Context, id uuid.UUID, token uuid.UUID, message string, _ time.Time) (bool, error) {
	r.fail(id, message)
	return true, nil
}

type queueRateProvider func(context.Context, models.CurrencyPair) (float64, error)

func (p queueRateProvider) GetRate(ctx context.Context, pair models.CurrencyPair) (float64, error) {
	return p(ctx, pair)
}

func queueService(repository TaskRepository, pool *WorkerPool) *ApplicationService {
	return NewApplicationService(map[string]struct{}{"EUR": {}, "MXN": {}}, repository, pool)
}

func (r *queueTaskRepository) ClaimNextTask(ctx context.Context, d time.Duration) (models.UpdateTask, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.tasks) == 0 {
		return models.UpdateTask{}, false, nil
	}
	task := r.tasks[0]
	if r.claim != nil {
		ok, err := r.claim(ctx, task.ID)
		if err != nil || !ok {
			return models.UpdateTask{}, false, err
		}
	}
	r.tasks = r.tasks[1:]
	task.LeaseToken = uuid.New()
	return task, true, nil
}
