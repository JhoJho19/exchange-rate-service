package service

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/JhoJho19/exchange-rate-service/internal/models"
	"github.com/google/uuid"
)

// The queue fake discards completion times; retain them at the repository boundary.
type timestampRepository struct {
	queueTaskRepository
	id          uuid.UUID
	quote       *models.Quote
	completedAt *time.Time
	completions int
	failures    int
}

func (r *timestampRepository) CompleteTask(_ context.Context, id uuid.UUID, quote models.Quote, at time.Time) (bool, error) {
	r.id, r.quote, r.completedAt = id, &quote, &at
	r.completions++
	return true, nil
}

func (r *timestampRepository) FailTask(_ context.Context, id uuid.UUID, _ string, at time.Time) (bool, error) {
	r.id, r.completedAt = id, &at
	r.failures++
	return true, nil
}

func TestWorkerCompletionTimestamps(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "success"},
		{name: "provider failure", err: errors.New("provider unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var created models.UpdateTask
				repo := &timestampRepository{queueTaskRepository: queueTaskRepository{
					create: func(_ context.Context, task models.UpdateTask) error {
						created = task
						return nil
					},
				}}
				var receivedAt time.Time
				provider := queueRateProvider(func(_ context.Context, pair models.CurrencyPair) (float64, error) {
					if pair != created.Pair {
						t.Errorf("provider pair = %v, want %v", pair, created.Pair)
					}
					// Virtual time distinguishes response receipt from task creation.
					time.Sleep(5 * time.Second)
					receivedAt = time.Now()
					return 20, tc.err
				})
				pool := lifecyclePool(repo, provider, 1, 1)
				id, err := queueService(repo, pool).CreateUpdate(context.Background(), "EUR", "MXN")
				if err != nil {
					t.Fatal(err)
				}
				if created.CompletedAt != nil || created.Quote != nil || created.Status != models.StatusPending {
					t.Fatalf("new task already finalized: %+v", created)
				}
				if err := pool.Start(context.Background()); err != nil {
					t.Fatal(err)
				}
				if err := pool.Stop(context.Background()); err != nil {
					t.Fatal(err)
				}
				if repo.id != id || repo.completedAt == nil {
					t.Fatalf("completion ID = %s, time = %v", repo.id, repo.completedAt)
				}
				if !repo.completedAt.Equal(receivedAt) || repo.completedAt.Location() != time.UTC || !repo.completedAt.After(created.CreatedAt) {
					t.Fatalf("CompletedAt = %v, want response time %v in UTC after creation", repo.completedAt, receivedAt)
				}
				if tc.err != nil {
					if repo.failures != 1 || repo.completions != 0 || repo.quote != nil {
						t.Fatal("provider failure persisted a successful quote")
					}
					return
				}
				if repo.completions != 1 || repo.failures != 0 || repo.quote == nil {
					t.Fatal("successful task was not completed exactly once")
				}
				if repo.quote.Pair != created.Pair || repo.quote.Rate != 20 || !repo.quote.UpdatedAt.Equal(receivedAt) || repo.quote.UpdatedAt.Location() != time.UTC {
					t.Fatalf("quote = %+v, want requested pair/rate and response time %v in UTC", repo.quote, receivedAt)
				}
			})
		})
	}
}

func TestWorkerSkipsUnclaimedTask(t *testing.T) {
	for _, claimErr := range []error{nil, errors.New("claim unavailable")} {
		name := "claim refused"
		if claimErr != nil {
			name = "claim error"
		}
		t.Run(name, func(t *testing.T) {
			id := uuid.New()
			claims, calls, finalizations := 0, 0, 0
			repo := &queueTaskRepository{
				claim: func(_ context.Context, got uuid.UUID) (bool, error) {
					claims++
					if got != id {
						t.Errorf("claim ID = %s, want %s", got, id)
					}
					return false, claimErr
				},
				complete: func(uuid.UUID, models.Quote) { finalizations++ },
				fail:     func(uuid.UUID, string) { finalizations++ },
			}
			pool := lifecyclePool(repo, queueRateProvider(func(context.Context, models.CurrencyPair) (float64, error) {
				calls++
				return 20, nil
			}), 1, 1)
			pool.processTask(context.Background(), models.UpdateTask{ID: id})
			if claims != 1 || calls != 0 || finalizations != 0 || len(pool.limiter.attempts) != 0 {
				t.Fatalf("unclaimed task consumed work: claims=%d provider=%d finalizations=%d quota=%d", claims, calls, finalizations, len(pool.limiter.attempts))
			}
		})
	}
}
