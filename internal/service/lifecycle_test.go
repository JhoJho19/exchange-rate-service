package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/JhoJho19/exchange-rate-service/internal/models"
	"github.com/google/uuid"
)

func lifecyclePool(repo TaskRepository, provider RateProvider, workers, capacity int) *WorkerPool {
	return NewWorkerPool(workers, repo, provider, time.Minute, newTestRateLimiter(10000, 10000), RetryPolicy{Delay: time.Second})
}

func TestStopFinishesCurrentAndLeavesPending(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		entered, release := make(chan struct{}), make(chan struct{})
		var completed atomic.Int32
		repo := &queueTaskRepository{complete: func(uuid.UUID, models.Quote) { completed.Add(1) }}
		p := lifecyclePool(repo, queueRateProvider(func(context.Context, models.CurrencyPair) (float64, error) { close(entered); <-release; return 1, nil }), 1, 2)
		for range 2 {
			_ = repo.Create(context.Background(), models.UpdateTask{ID: uuid.New()})
		}
		_ = p.Start(context.Background())
		<-entered
		stopped := make(chan error, 1)
		go func() { stopped <- p.Stop(context.Background()) }()
		synctest.Wait()
		select {
		case <-stopped:
			t.Fatal("did not wait for current task")
		default:
		}
		close(release)
		if err := <-stopped; err != nil {
			t.Fatal(err)
		}
		if completed.Load() != 1 || len(repo.tasks) != 1 {
			t.Fatal("shutdown claimed pending work")
		}
		if err := p.Start(context.Background()); err == nil {
			t.Fatal("restarted")
		}
	})
}

func TestStopDeadlineCancelsCurrent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		entered := make(chan struct{})
		repo := &queueTaskRepository{complete: func(uuid.UUID, models.Quote) { t.Error("completed canceled task") }, fail: func(uuid.UUID, string) { t.Error("failed canceled task") }}
		p := lifecyclePool(repo, queueRateProvider(func(ctx context.Context, _ models.CurrencyPair) (float64, error) {
			close(entered)
			<-ctx.Done()
			return 0, ctx.Err()
		}), 1, 1)
		_ = repo.Create(context.Background(), models.UpdateTask{ID: uuid.New()})
		_ = p.Start(context.Background())
		<-entered
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := p.Stop(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
	})
}

func TestStopCancelsBlockedClaim(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		entered := make(chan struct{})
		repo := &leaseRepository{next: func(ctx context.Context, _ time.Duration) (models.UpdateTask, bool, error) {
			close(entered)
			<-ctx.Done()
			return models.UpdateTask{}, false, ctx.Err()
		}}
		p := lifecyclePool(repo, nil, 1, 1)
		_ = p.Start(context.Background())
		<-entered
		if err := p.Stop(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
}

func TestStopBeforeStart(t *testing.T) {
	p := lifecyclePool(nil, nil, 1, 1)
	_ = p.Stop(context.Background())
	if err := p.Start(context.Background()); err == nil {
		t.Fatal("started stopped pool")
	}
}
