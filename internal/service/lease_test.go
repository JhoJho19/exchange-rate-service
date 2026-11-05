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

type leaseRepository struct {
	queueTaskRepository
	renew func(context.Context, uuid.UUID, uuid.UUID, time.Duration) (bool, error)
	next  func(context.Context, time.Duration) (models.UpdateTask, bool, error)
}

func (r *leaseRepository) RenewLease(ctx context.Context, id, token uuid.UUID, d time.Duration) (bool, error) {
	return r.renew(ctx, id, token, d)
}
func (r *leaseRepository) ClaimNextTask(ctx context.Context, d time.Duration) (models.UpdateTask, bool, error) {
	return r.next(ctx, d)
}

func TestHeartbeatCancelsWorkOnLeaseLoss(t *testing.T) {
	for _, mode := range []string{"rejected", "error", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var finalized, renewed atomic.Int32
				task := models.UpdateTask{ID: uuid.New(), LeaseToken: uuid.New()}
				repo := &leaseRepository{queueTaskRepository: queueTaskRepository{complete: func(uuid.UUID, models.Quote) { finalized.Add(1) }, fail: func(uuid.UUID, string) { finalized.Add(1) }}}
				repo.renew = func(ctx context.Context, id, token uuid.UUID, d time.Duration) (bool, error) {
					renewed.Add(1)
					if id != task.ID || token != task.LeaseToken {
						t.Error("wrong heartbeat identity")
					}
					switch mode {
					case "error":
						return false, errors.New("database unavailable")
					case "timeout":
						<-ctx.Done()
						return false, ctx.Err()
					default:
						return false, nil
					}
				}
				p := lifecyclePool(repo, queueRateProvider(func(ctx context.Context, _ models.CurrencyPair) (float64, error) { <-ctx.Done(); return 0, ctx.Err() }), 1, 1)
				p.processLeasedTask(context.Background(), task)
				if renewed.Load() != 1 || finalized.Load() != 0 {
					t.Fatalf("renewed=%d finalized=%d", renewed.Load(), finalized.Load())
				}
			})
		})
	}
}
func TestHeartbeatRenewsDuringLongProviderCall(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var renewed atomic.Int32
		var finalized atomic.Int32
		task := models.UpdateTask{ID: uuid.New(), LeaseToken: uuid.New()}
		repo := &leaseRepository{queueTaskRepository: queueTaskRepository{complete: func(uuid.UUID, models.Quote) { finalized.Add(1) }}}
		repo.renew = func(context.Context, uuid.UUID, uuid.UUID, time.Duration) (bool, error) {
			renewed.Add(1)
			return true, nil
		}
		p := lifecyclePool(repo, queueRateProvider(func(context.Context, models.CurrencyPair) (float64, error) {
			time.Sleep(45 * time.Second)
			return 1.2, nil
		}), 1, 1)
		p.processLeasedTask(context.Background(), task)
		if renewed.Load() < 4 || finalized.Load() != 1 {
			t.Fatalf("renewed=%d finalized=%d", renewed.Load(), finalized.Load())
		}
		before := renewed.Load()
		time.Sleep(time.Minute)
		if renewed.Load() != before {
			t.Fatal("heartbeat leaked")
		}
	})
}
func TestWorkerPollsWithoutLocalSubmission(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		task := models.UpdateTask{ID: uuid.New(), LeaseToken: uuid.New()}
		var claims atomic.Int32
		done := make(chan struct{})
		repo := &leaseRepository{queueTaskRepository: queueTaskRepository{complete: func(uuid.UUID, models.Quote) { close(done) }}}
		repo.next = func(context.Context, time.Duration) (models.UpdateTask, bool, error) {
			if claims.Add(1) == 1 {
				return task, true, nil
			}
			return models.UpdateTask{}, false, nil
		}
		repo.renew = func(context.Context, uuid.UUID, uuid.UUID, time.Duration) (bool, error) { return true, nil }
		p := lifecyclePool(repo, queueRateProvider(func(context.Context, models.CurrencyPair) (float64, error) { return 1.2, nil }), 1, 1)
		if err := p.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		<-done
		if err := p.Stop(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
}

func TestPollingRetriesDatabaseErrorsWithoutNotification(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var claims atomic.Int32
		done := make(chan struct{})
		repo := &leaseRepository{queueTaskRepository: queueTaskRepository{complete: func(uuid.UUID, models.Quote) { close(done) }}}
		repo.next = func(context.Context, time.Duration) (models.UpdateTask, bool, error) {
			switch claims.Add(1) {
			case 1:
				return models.UpdateTask{}, false, errors.New("database unavailable")
			case 2:
				return models.UpdateTask{}, false, nil
			case 3:
				return models.UpdateTask{ID: uuid.New(), LeaseToken: uuid.New()}, true, nil
			default:
				return models.UpdateTask{}, false, nil
			}
		}
		repo.renew = func(context.Context, uuid.UUID, uuid.UUID, time.Duration) (bool, error) { return true, nil }
		p := lifecyclePool(repo, queueRateProvider(func(context.Context, models.CurrencyPair) (float64, error) {
			if claims.Load() != 3 {
				t.Error("provider called without a claim")
			}
			return 1, nil
		}), 1, 1)
		_ = p.Start(context.Background())
		<-done
		_ = p.Stop(context.Background())
	})
}

func TestConfiguredLeaseAndPolling(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var claims, renewals atomic.Int32
		done := make(chan struct{})
		repo := &leaseRepository{queueTaskRepository: queueTaskRepository{complete: func(uuid.UUID, models.Quote) { close(done) }}}
		started := time.Now()
		repo.next = func(_ context.Context, d time.Duration) (models.UpdateTask, bool, error) {
			if d != 3*time.Second {
				t.Errorf("lease=%v", d)
			}
			n := claims.Add(1)
			if n == 2 {
				if time.Since(started) != 250*time.Millisecond {
					t.Error("poll interval ignored")
				}
				return models.UpdateTask{ID: uuid.New(), LeaseToken: uuid.New()}, true, nil
			}
			return models.UpdateTask{}, false, nil
		}
		repo.renew = func(_ context.Context, _ uuid.UUID, _ uuid.UUID, d time.Duration) (bool, error) {
			if d != 3*time.Second {
				t.Errorf("renew duration=%v", d)
			}
			renewals.Add(1)
			return true, nil
		}
		provider := queueRateProvider(func(context.Context, models.CurrencyPair) (float64, error) {
			time.Sleep(3500 * time.Millisecond)
			return 1, nil
		})
		p := NewWorkerPool(1, repo, provider, time.Minute, newTestRateLimiter(10, 100), RetryPolicy{Delay: time.Second}, WithLeaseTiming(3*time.Second, 250*time.Millisecond))
		_ = p.Start(context.Background())
		<-done
		_ = p.Stop(context.Background())
		if renewals.Load() != 3 {
			t.Fatalf("heartbeat every lease/3: %d", renewals.Load())
		}
	})
}
