package service

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/JhoJho19/exchange-rate-service/internal/apperrors"
	"github.com/JhoJho19/exchange-rate-service/internal/models"
	"github.com/google/uuid"
)

type keyedRepository struct {
	fakeTaskRepository
	mu           sync.Mutex
	tasks        map[string]models.UpdateTask
	err          error
	prepareCalls int
}

func (r *keyedRepository) CreateOrGet(_ context.Context, key string, pair models.CurrencyPair, prepare func() (models.UpdateTask, error)) (uuid.UUID, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if task, ok := r.tasks[key]; ok {
		if task.Pair != pair {
			return uuid.Nil, false, apperrors.ErrIdempotencyConflict
		}
		return task.ID, false, nil
	}
	r.prepareCalls++
	task, err := prepare()
	if err != nil {
		return uuid.Nil, false, err
	}
	if r.err != nil {
		return uuid.Nil, false, r.err
	}
	r.tasks[key] = task
	return task.ID, true, nil
}
func keyedService(r *keyedRepository) (*ApplicationService, *WorkerPool) {
	var pool = NewWorkerPool(1, 1, r, nil, time.Second, NewRateLimiter(10, 100), RetryPolicy{Delay: time.Second})
	return queueService(r, pool), pool
}
func TestIdempotentConcurrentCreation(t *testing.T) {
	r := &keyedRepository{tasks: make(map[string]models.UpdateTask)}
	svc, pool := keyedService(r)
	key := "key"
	ids := make(chan uuid.UUID, 10)
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			id, err := svc.CreateUpdateWithKey(context.Background(), " eur ", "mxn", &key)
			if err != nil {
				t.Error(err)
			}
			ids <- id
		})
	}
	wg.Wait()
	close(ids)
	want := r.tasks[key].ID
	for id := range ids {
		if id != want || id == uuid.Nil {
			t.Fatalf("unexpected ID %s", id)
		}
	}
	if len(r.tasks) != 1 || r.prepareCalls != 1 || len(pool.jobs) != 1 || len(pool.active) != 1 || len(pool.queueSlots) != 1 {
		t.Fatal("duplicate creation or queue reservation")
	}
}
func TestIdempotentExistingStates(t *testing.T) {
	for _, status := range []models.UpdateStatus{models.StatusPending, models.StatusProcessing, models.StatusCompleted, models.StatusFailed} {
		t.Run(string(status), func(t *testing.T) {
			key := "key"
			now := time.Now()
			task := models.UpdateTask{ID: uuid.New(), Pair: models.CurrencyPair{BaseCurrency: "EUR", QuoteCurrency: "MXN"}, Status: status, CreatedAt: now, CompletedAt: &now, ErrorMessage: "retained", Quote: &models.Quote{Rate: 2, UpdatedAt: now}}
			r := &keyedRepository{tasks: map[string]models.UpdateTask{key: task}}
			svc, pool := keyedService(r)
			pool.stopping = true
			id, err := svc.CreateUpdateWithKey(context.Background(), "eur", " mxn ", &key)
			if err != nil || id != task.ID {
				t.Fatalf("repeat: %s %v", id, err)
			}
			_, err = svc.CreateUpdateWithKey(context.Background(), "MXN", "EUR", &key)
			if !errors.Is(err, apperrors.ErrIdempotencyConflict) {
				t.Fatal(err)
			}
			if r.prepareCalls != 0 || len(pool.jobs) != 0 || !reflect.DeepEqual(task, r.tasks[key]) {
				t.Fatal("repeat modified or enqueued existing task")
			}
		})
	}
}
func TestIdempotentPersistenceFailureReleasesReservation(t *testing.T) {
	sentinel := errors.New("database error")
	r := &keyedRepository{tasks: make(map[string]models.UpdateTask), err: sentinel}
	svc, pool := keyedService(r)
	key := "key"
	if _, err := svc.CreateUpdateWithKey(context.Background(), "EUR", "MXN", &key); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	if len(pool.jobs) != 0 || len(pool.active) != 0 || len(pool.queueSlots) != 0 {
		t.Fatal("leaked reservation")
	}
	r.err = nil
	if _, err := svc.CreateUpdateWithKey(context.Background(), "EUR", "MXN", &key); err != nil {
		t.Fatal(err)
	}
	other := "other"
	if _, err := svc.CreateUpdateWithKey(context.Background(), "EUR", "MXN", &other); !errors.Is(err, ErrQueueFull) {
		t.Fatal(err)
	}
	if len(r.tasks) != 1 {
		t.Fatal("persisted despite overflow")
	}
}
