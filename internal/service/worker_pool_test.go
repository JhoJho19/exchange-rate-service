package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JhoJho19/exchange-rate-service/internal/apperrors"
	"github.com/JhoJho19/exchange-rate-service/internal/models"
	"github.com/google/uuid"
)

type queueTaskRepository struct {
	fakeTaskRepository
	claim    func(context.Context, uuid.UUID) (bool, error)
	create   func(context.Context, models.UpdateTask) error
	complete func(uuid.UUID, models.Quote)
	fail     func(uuid.UUID, string)
}

func (r *queueTaskRepository) Create(ctx context.Context, task models.UpdateTask) error {
	return r.create(ctx, task)
}

func (r *queueTaskRepository) TryStartProcessing(ctx context.Context, id uuid.UUID) (bool, error) {
	if r.claim != nil {
		return r.claim(ctx, id)
	}
	return true, nil
}

func (r *queueTaskRepository) CompleteTask(_ context.Context, id uuid.UUID, quote models.Quote, _ time.Time) (bool, error) {
	r.complete(id, quote)
	return true, nil
}

func (r *queueTaskRepository) FailTask(_ context.Context, id uuid.UUID, message string, _ time.Time) (bool, error) {
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

func TestCreateUpdateQueueCapacity(t *testing.T) {
	repository := &fakeTaskRepository{}
	pool := NewWorkerPool(1, 1, repository, nil, time.Second, NewRateLimiter(10, 100), RetryPolicy{MaxRetries: 2, Delay: time.Minute})
	service := queueService(repository, pool)
	id, err := service.CreateUpdate(context.Background(), "EUR", "MXN")
	if err != nil || id == uuid.Nil {
		t.Fatalf("CreateUpdate() = %s, %v", id, err)
	}
	if len(pool.jobs) != 1 || len(pool.queueSlots) != 1 {
		t.Fatal("persisted task must be queued with a reservation")
	}
	if queued := <-pool.jobs; queued.ID != id || queued != repository.createdTask {
		t.Fatalf("queued task differs from persisted task: %+v", queued)
	}
	// Keep the reservation held, as if a worker has not yet released it.
	id, err = service.CreateUpdate(context.Background(), "EUR", "MXN")
	if id != uuid.Nil || !errors.Is(err, apperrors.ErrQueueFull) || !errors.Is(err, apperrors.ErrServiceOverloaded) {
		t.Fatalf("full queue: got %s, %v", id, err)
	}
	if repository.createCalls != 1 {
		t.Fatal("full queue wrote to the repository")
	}
	_, err = service.CreateUpdate(context.Background(), "EUR", "EUR")
	if !errors.Is(err, apperrors.ErrInvalidCurrencyPair) {
		t.Fatalf("validation must precede reservation: %v", err)
	}
}

func TestCreateUpdateReleasesReservationOnPersistenceError(t *testing.T) {
	for _, repoErr := range []error{errors.New("database failed"), context.Canceled} {
		t.Run(repoErr.Error(), func(t *testing.T) {
			repository := &fakeTaskRepository{createErr: repoErr}
			pool := NewWorkerPool(1, 1, repository, nil, time.Second, NewRateLimiter(10, 100), RetryPolicy{MaxRetries: 2, Delay: time.Minute})
			service := queueService(repository, pool)
			id, err := service.CreateUpdate(context.Background(), "EUR", "MXN")
			if id != uuid.Nil || !errors.Is(err, repoErr) || len(pool.queueSlots) != 0 || len(pool.jobs) != 0 {
				t.Fatalf("failed persistence leaked capacity: id=%s err=%v", id, err)
			}
			repository.createErr = nil
			if _, err := service.CreateUpdate(context.Background(), "EUR", "MXN"); err != nil {
				t.Fatalf("reservation was not reusable: %v", err)
			}
		})
	}
}

func TestCreateUpdateEnqueuesAfterContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	repository := &queueTaskRepository{create: func(context.Context, models.UpdateTask) error {
		cancel() // Persistence succeeded just before request cancellation.
		return nil
	}}
	pool := NewWorkerPool(1, 1, repository, nil, time.Second, NewRateLimiter(10, 100), RetryPolicy{MaxRetries: 2, Delay: time.Minute})
	id, err := queueService(repository, pool).CreateUpdate(ctx, "EUR", "MXN")
	if err != nil || len(pool.jobs) != 1 {
		t.Fatalf("persisted task was not enqueued: %v", err)
	}
	if task := <-pool.jobs; task.ID != id || task.Status != models.StatusPending {
		t.Fatalf("unexpected queued task: %+v", task)
	}
}

func TestConcurrentCreateUpdateReservations(t *testing.T) {
	const capacity, requests = 7, 100
	entered := make(chan struct{}, requests)
	release := make(chan struct{})
	var creates atomic.Int32
	repository := &queueTaskRepository{create: func(context.Context, models.UpdateTask) error {
		creates.Add(1)
		entered <- struct{}{}
		<-release
		return nil
	}}
	pool := NewWorkerPool(1, capacity, repository, nil, time.Second, NewRateLimiter(10, 100), RetryPolicy{MaxRetries: 2, Delay: time.Minute})
	service := queueService(repository, pool)
	results := make(chan error, requests)
	var wg sync.WaitGroup
	defer func() { close(release); wg.Wait() }()
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := service.CreateUpdate(context.Background(), "EUR", "MXN")
			results <- err
		}()
	}
	for i := 0; i < capacity; i++ {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("reservations did not reach the repository")
		}
	}
	for i := 0; i < requests-capacity; i++ {
		select {
		case err := <-results:
			if !errors.Is(err, apperrors.ErrQueueFull) {
				t.Fatalf("expected overflow while persistence is blocked, got %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("overflow request blocked")
		}
	}
	if creates.Load() != capacity || len(pool.queueSlots) != capacity || len(pool.jobs) != 0 {
		t.Fatal("concurrent reservations exceeded capacity")
	}
	// Unblock successful persistence without closing the channel twice in cleanup.
	for i := 0; i < capacity; i++ {
		release <- struct{}{}
	}
	for i := 0; i < capacity; i++ {
		if err := <-results; err != nil {
			t.Fatalf("reserved enqueue failed: %v", err)
		}
	}
	if len(pool.jobs) != capacity {
		t.Fatalf("queued %d tasks, want %d", len(pool.jobs), capacity)
	}
	if err := pool.Enqueue(models.UpdateTask{}); !errors.Is(err, apperrors.ErrQueueFull) {
		t.Fatalf("Enqueue bypassed reservations: %v", err)
	}
}

func TestWorkerReleasesCapacityBeforeProviderCompletes(t *testing.T) {
	for _, providerErr := range []error{nil, errors.New("provider failed")} {
		name := "success"
		if providerErr != nil {
			name = "failure"
		}
		t.Run(name, func(t *testing.T) {
			started := make(chan struct{}, 2)
			release := make(chan struct{})
			finished := make(chan uuid.UUID, 2)
			repository := &queueTaskRepository{
				create: func(context.Context, models.UpdateTask) error { return nil },
				complete: func(id uuid.UUID, quote models.Quote) {
					if quote.Rate != 20 || quote.UpdatedAt.IsZero() {
						t.Error("incorrect completed quote")
					}
					finished <- id
				},
				fail: func(id uuid.UUID, message string) {
					if message != "Failed to fetch exchange rate from external provider" {
						t.Error("incorrect safe error")
					}
					finished <- id
				},
			}
			provider := queueRateProvider(func(context.Context, models.CurrencyPair) (float64, error) {
				started <- struct{}{}
				<-release
				return 20, providerErr
			})
			pool := NewWorkerPool(1, 1, repository, provider, time.Second, NewRateLimiter(10, 100), RetryPolicy{MaxRetries: 2, Delay: time.Minute})
			service := queueService(repository, pool)
			first, err := service.CreateUpdate(context.Background(), "EUR", "MXN")
			if err != nil {
				t.Fatal(err)
			}
			pool.Start(context.Background())
			defer func() { close(release); pool.Stop(context.Background()) }()
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("worker did not start")
			}
			if len(pool.queueSlots) != 0 {
				t.Fatal("worker retained reservation during provider call")
			}
			second, err := service.CreateUpdate(context.Background(), "EUR", "MXN")
			if err != nil {
				t.Fatalf("capacity was not released: %v", err)
			}
			for _, wantID := range []uuid.UUID{first, second} {
				release <- struct{}{}
				select {
				case id := <-finished:
					if id != wantID {
						t.Errorf("finished %s, want %s", id, wantID)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("worker did not finish")
				}
			}
		})
	}
}
