package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/JhoJho19/exchange-rate-service/internal/apperrors"
	"github.com/JhoJho19/exchange-rate-service/internal/models"
	"github.com/google/uuid"
)

func lifecyclePool(repo TaskRepository, provider RateProvider, workers, capacity int) *WorkerPool {
	return NewWorkerPool(workers, capacity, repo, provider, time.Minute, NewRateLimiter(10000, 10000), RetryPolicy{Delay: time.Second})
}

func TestShutdownWaitsForPersistingProducerAndDrains(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		entered, release := make(chan struct{}), make(chan struct{})
		var finished atomic.Int32
		repo := &queueTaskRepository{
			create:   func(context.Context, models.UpdateTask) error { close(entered); <-release; return nil },
			complete: func(uuid.UUID, models.Quote) { finished.Add(1) },
		}
		p := lifecyclePool(repo, queueRateProvider(func(context.Context, models.CurrencyPair) (float64, error) { return 20, nil }), 2, 2)
		if err := p.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		svc := queueService(repo, p)
		created := make(chan error, 1)
		go func() { _, err := svc.CreateUpdate(context.Background(), "EUR", "MXN"); created <- err }()
		<-entered
		stopped := make(chan error, 2)
		for range 2 {
			go func() { stopped <- p.Stop(context.Background()) }()
		}
		synctest.Wait()
		select {
		case <-stopped:
			t.Fatal("Stop returned before persistence")
		default:
		}
		if _, err := svc.CreateUpdate(context.Background(), "EUR", "MXN"); !errors.Is(err, apperrors.ErrServiceOverloaded) {
			t.Fatalf("admission: %v", err)
		}
		if err := p.Enqueue(models.UpdateTask{ID: uuid.New()}); !errors.Is(err, ErrQueueFull) {
			t.Fatalf("Enqueue: %v", err)
		}
		close(release)
		if err := <-created; err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if err := <-stopped; err != nil {
				t.Fatal(err)
			}
		}
		if finished.Load() != 1 || len(p.queueSlots) != 0 {
			t.Fatal("accepted work not drained")
		}
		if err := p.Stop(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := p.Start(context.Background()); err == nil {
			t.Fatal("restarted stopped pool")
		}
	})
}

func TestShutdownDeadlineCancelsAndJoinsOperations(t *testing.T) {
	for _, stage := range []string{"persistence", "provider", "limiter"} {
		t.Run(stage, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var operations, finalized atomic.Int32
				entered := make(chan struct{}, 1)
				repo := &queueTaskRepository{
					create: func(ctx context.Context, _ models.UpdateTask) error {
						if stage != "persistence" {
							return nil
						}
						operations.Add(1)
						defer operations.Add(-1)
						entered <- struct{}{}
						<-ctx.Done()
						return ctx.Err()
					},
					claim: func(context.Context, uuid.UUID) (bool, error) {
						if stage == "limiter" {
							entered <- struct{}{}
						}
						return true, nil
					},
					complete: func(uuid.UUID, models.Quote) { finalized.Add(1) },
					fail:     func(uuid.UUID, string) { finalized.Add(1) },
				}
				provider := queueRateProvider(func(ctx context.Context, _ models.CurrencyPair) (float64, error) {
					operations.Add(1)
					defer operations.Add(-1)
					entered <- struct{}{}
					<-ctx.Done()
					return 0, ctx.Err()
				})
				p := lifecyclePool(repo, provider, 1, 2)
				if stage == "limiter" {
					p.limiter.PauseUntil(time.Now().Add(time.Hour))
				}
				if err := p.Start(context.Background()); err != nil {
					t.Fatal(err)
				}
				created := make(chan error, 1)
				go func() {
					_, err := queueService(repo, p).CreateUpdate(context.Background(), "EUR", "MXN")
					created <- err
				}()
				<-entered
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if err := p.Stop(ctx); !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("Stop: %v", err)
				}
				<-created
				if operations.Load() != 0 || finalized.Load() != 0 {
					t.Fatal("returned with live operations or finalized shutdown work")
				}
				synctest.Wait()
			})
		})
	}
}

func TestConcurrentCreateEnqueueStartAndStop(t *testing.T) {
	repo := &queueTaskRepository{
		create:   func(context.Context, models.UpdateTask) error { return nil },
		complete: func(uuid.UUID, models.Quote) {},
	}
	for range 30 {
		p := lifecyclePool(repo, queueRateProvider(func(context.Context, models.CurrencyPair) (float64, error) { return 1, nil }), 2, 10)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := range 60 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				switch i % 4 {
				case 0:
					_ = p.Start(context.Background())
				case 1:
					_ = p.Stop(context.Background())
				case 2:
					_, err := queueService(repo, p).CreateUpdate(context.Background(), "EUR", "MXN")
					if err != nil && !errors.Is(err, ErrQueueFull) {
						t.Error(err)
					}
				case 3:
					if err := p.Enqueue(models.UpdateTask{ID: uuid.New()}); err != nil && !errors.Is(err, ErrQueueFull) {
						t.Error(err)
					}
				}
			}()
		}
		close(start)
		wg.Wait()
		_ = p.Stop(context.Background())
	}
}

func TestStartOnceAndStopBeforeStart(t *testing.T) {
	p := lifecyclePool(nil, nil, 1, 1)
	if err := p.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := p.Start(context.Background()); err == nil {
		t.Fatal("second Start succeeded")
	}
	_ = p.Stop(context.Background())
	q := lifecyclePool(nil, nil, 1, 1)
	_ = q.Stop(context.Background())
	if err := q.Start(context.Background()); err == nil {
		t.Fatal("Start after Stop succeeded")
	}
}
