package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/JhoJho19/exchange-rate-service/internal/apperrors"
	"github.com/JhoJho19/exchange-rate-service/internal/models"
	"github.com/google/uuid"
)

func TestWorkerRateLimitRetries(t *testing.T) {
	for _, tc := range []struct {
		name              string
		failures, retries int
		providerErr       error
		wantCalls         int
		wantFailed        bool
	}{
		{"retry succeeds", 2, 2, apperrors.ErrProviderRateLimited, 3, false},
		{"retry exhausted", 10, 2, apperrors.ErrProviderRateLimited, 3, true},
		{"retries disabled", 10, 0, apperrors.ErrProviderRateLimited, 1, true},
		{"other errors not retried", 10, 2, apperrors.ErrProviderUnavailable, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var calls int
				var completed, failed bool
				repo := &queueTaskRepository{
					complete: func(uuid.UUID, models.Quote) { completed = true },
					fail:     func(uuid.UUID, string) { failed = true },
				}
				limiter := NewRateLimiter(100, 100)
				start := time.Now()
				provider := queueRateProvider(func(ctx context.Context, _ models.CurrencyPair) (float64, error) {
					calls++
					deadline, ok := ctx.Deadline()

					remaining := time.Until(deadline)
					if !ok || remaining < 9*time.Second || remaining > 10*time.Second {
						t.Errorf("unexpected provider timeout: %v", remaining)
					}

					if calls <= tc.failures {
						return 0, tc.providerErr
					}
					return 20, nil
				})
				pool := NewWorkerPool(1, 1, repo, provider, 10*time.Second, limiter, RetryPolicy{MaxRetries: tc.retries, Delay: time.Minute})
				if err := pool.Enqueue(models.UpdateTask{ID: uuid.New()}); err != nil {
					t.Fatal(err)
				}
				pool.Start(context.Background())
				pool.Stop(context.Background()) // Normal draining does not cancel ongoing retries.
				if calls != tc.wantCalls || failed != tc.wantFailed || completed == tc.wantFailed {
					t.Fatalf("calls=%d completed=%v failed=%v", calls, completed, failed)
				}
				if calls == 3 && time.Since(start) != 3*time.Minute {
					t.Fatalf("fallback backoff = %v", time.Since(start))
				}
				if len(limiter.attempts) != calls {
					t.Fatal("retries did not consume shared quota")
				}
				if errors.Is(tc.providerErr, apperrors.ErrProviderRateLimited) && tc.wantFailed && !limiter.blockedUntil.After(time.Now()) {
					t.Fatal("final 429 did not pause other tasks")
				}
			})
		})
	}
}

func TestWorkerRetryAfterAndQuota(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		var calls int
		provider := queueRateProvider(func(context.Context, models.CurrencyPair) (float64, error) {
			calls++
			if calls == 1 {
				return 0, &apperrors.ProviderRateLimitError{RetryAt: start.Add(90 * time.Second)}
			}
			return 20, nil
		})
		limiter := NewRateLimiter(1, 100)
		pool := NewWorkerPool(1, 1, nil, provider, time.Second, limiter, RetryPolicy{MaxRetries: 2, Delay: time.Second})
		rate, err := pool.getRate(context.Background(), models.CurrencyPair{})
		if err != nil || rate != 20 || calls != 2 || time.Since(start) != 90*time.Second {
			t.Fatalf("rate=%v err=%v calls=%d wait=%v", rate, err, calls, time.Since(start))
		}
	})
}

func TestWorkersShareQuotaAndCooldown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		start := time.Now()
		starts := make(chan time.Time, 2)
		provider := queueRateProvider(func(context.Context, models.CurrencyPair) (float64, error) {
			starts <- time.Now()
			if calls.Add(1) == 1 {
				return 0, &apperrors.ProviderRateLimitError{RetryAt: start.Add(2 * time.Minute)}
			}
			return 20, nil
		})
		repo := &queueTaskRepository{complete: func(uuid.UUID, models.Quote) {}, fail: func(uuid.UUID, string) {}}
		pool := NewWorkerPool(2, 2, repo, provider, time.Second, NewRateLimiter(1, 100), RetryPolicy{MaxRetries: 0, Delay: time.Second})
		for i := 0; i < 2; i++ {
			if err := pool.Enqueue(models.UpdateTask{ID: uuid.New()}); err != nil {
				t.Fatal(err)
			}
		}
		pool.Start(context.Background())
		pool.Stop(context.Background())
		first, second := <-starts, <-starts
		if !first.Equal(start) || second.Sub(start) != 2*time.Minute {
			t.Fatalf("starts: %v, %v", first, second)
		}
	})
}

func TestWorkerPoolCancellationPreservesUnfinishedTasks(t *testing.T) {
	for _, stage := range []string{"before claim", "waiting for quota", "during provider"} {
		t.Run(stage, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				var claims, calls, finished atomic.Int32
				repo := &queueTaskRepository{
					claim:    func(context.Context, uuid.UUID) (bool, error) { claims.Add(1); return true, nil },
					complete: func(uuid.UUID, models.Quote) { finished.Add(1) },
					fail:     func(uuid.UUID, string) { finished.Add(1) },
				}
				provider := queueRateProvider(func(ctx context.Context, _ models.CurrencyPair) (float64, error) {
					calls.Add(1)
					<-ctx.Done()
					return 0, apperrors.ErrProviderTimeout
				})
				limiter := NewRateLimiter(1, 1)
				if stage == "waiting for quota" {
					if err := limiter.Wait(context.Background()); err != nil {
						t.Fatal(err)
					}
				}
				if stage == "before claim" {
					cancel()
				}
				pool := NewWorkerPool(1, 1, repo, provider, 10*time.Second, limiter, RetryPolicy{MaxRetries: 2, Delay: time.Minute})
				if err := pool.Enqueue(models.UpdateTask{ID: uuid.New()}); err != nil {
					t.Fatal(err)
				}
				pool.Start(ctx)
				synctest.Wait()
				cancel()
				pool.Stop(context.Background())
				if finished.Load() != 0 {
					t.Fatal("shutdown finalized unfinished task")
				}
				wantClaims := int32(1)
				if stage == "before claim" {
					wantClaims = 0
				}
				wantCalls := int32(0)
				if stage == "during provider" {
					wantCalls = 1
				}
				if claims.Load() != wantClaims || calls.Load() != wantCalls {
					t.Fatalf("claims=%d calls=%d", claims.Load(), calls.Load())
				}
			})
		})
	}
}

func TestProviderTimeoutStillFailsTask(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		failed := false
		repo := &queueTaskRepository{fail: func(uuid.UUID, string) { failed = true }}
		provider := queueRateProvider(func(ctx context.Context, _ models.CurrencyPair) (float64, error) {
			<-ctx.Done()
			return 0, apperrors.ErrProviderTimeout
		})
		pool := NewWorkerPool(1, 1, repo, provider, time.Second, NewRateLimiter(10, 100), RetryPolicy{MaxRetries: 2, Delay: time.Minute})
		if err := pool.Enqueue(models.UpdateTask{ID: uuid.New()}); err != nil {
			t.Fatal(err)
		}
		pool.Start(context.Background())
		pool.Stop(context.Background())
		if !failed {
			t.Fatal("provider timeout did not fail task")
		}
	})
}
