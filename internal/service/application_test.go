package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/JhoJho19/exchange-rate-service/internal/apperrors"
	"github.com/JhoJho19/exchange-rate-service/internal/models"
	"github.com/google/uuid"
)

type fakeTaskRepository struct {
	TaskRepository
	createCalls int
	createdTask models.UpdateTask
	createErr   error
}

func (r *fakeTaskRepository) Create(_ context.Context, task models.UpdateTask) error {
	r.createCalls++
	r.createdTask = task
	return r.createErr
}

func (*fakeTaskRepository) GetByID(context.Context, uuid.UUID) (models.UpdateTask, error) {
	panic("unexpected GetByID call")
}

func (*fakeTaskRepository) CompleteTask(context.Context, uuid.UUID, uuid.UUID, models.Quote, time.Time) (bool, error) {
	panic("unexpected CompleteTask call")
}

func (*fakeTaskRepository) FailTask(context.Context, uuid.UUID, uuid.UUID, string, time.Time) (bool, error) {
	panic("unexpected FailTask call")
}

func (*fakeTaskRepository) GetLatestQuote(context.Context, models.CurrencyPair) (models.Quote, error) {
	panic("unexpected GetLatestQuote call")
}

func TestApplicationServiceCreateUpdate(t *testing.T) {
	supportedCurrencies := map[string]struct{}{
		"EUR": {},
		"MXN": {},
	}

	t.Run("successful task creation", func(t *testing.T) {
		repository := &fakeTaskRepository{}
		service := NewApplicationService(supportedCurrencies, repository,
			NewWorkerPool(1, repository, nil, time.Second, newTestRateLimiter(10, 100), RetryPolicy{MaxRetries: 2, Delay: time.Minute}))

		before := time.Now().UTC()
		id, err := service.CreateUpdate(context.Background(), " eUr\t", "\nmXn ")
		after := time.Now().UTC()

		if err != nil {
			t.Fatalf("CreateUpdate() unexpected error: %v", err)
		}
		if id == uuid.Nil {
			t.Error("CreateUpdate() returned a nil UUID")
		}
		if id.Version() != 4 || id.Variant() != uuid.RFC4122 {
			t.Errorf("CreateUpdate() UUID = %s, want an RFC 4122 version 4 UUID", id)
		}
		if repository.createCalls != 1 {
			t.Fatalf("Create() calls = %d, want 1", repository.createCalls)
		}

		task := repository.createdTask
		if task.ID != id {
			t.Errorf("task ID = %s, want returned ID %s", task.ID, id)
		}
		wantPair := models.CurrencyPair{BaseCurrency: "EUR", QuoteCurrency: "MXN"}
		if task.Pair != wantPair {
			t.Errorf("task pair = %+v, want %+v", task.Pair, wantPair)
		}
		if task.Status != models.StatusPending {
			t.Errorf("task status = %s, want %s", task.Status, models.StatusPending)
		}
		if task.CreatedAt.IsZero() || task.CreatedAt.Before(before) || task.CreatedAt.After(after) {
			t.Errorf("task CreatedAt = %v, want a nonzero timestamp between %v and %v", task.CreatedAt, before, after)
		}
		if task.CreatedAt.Location() != time.UTC {
			t.Errorf("task CreatedAt location = %v, want UTC", task.CreatedAt.Location())
		}
	})

	repositoryErr := errors.New("repository create failed")
	tests := []struct {
		name        string
		base        string
		quote       string
		createErr   error
		expectedErr error
		wantCalls   int
	}{
		{name: "invalid currency pair", base: "EUR", quote: "EUR", expectedErr: apperrors.ErrInvalidCurrencyPair},
		{name: "unsupported currency", base: "EUR", quote: "GBP", expectedErr: apperrors.ErrUnsupportedCurrency},
		{name: "repository failure", base: "EUR", quote: "MXN", createErr: repositoryErr, expectedErr: repositoryErr, wantCalls: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repository := &fakeTaskRepository{createErr: tt.createErr}
			service := NewApplicationService(supportedCurrencies, repository,
				NewWorkerPool(1, repository, nil, time.Second, newTestRateLimiter(10, 100), RetryPolicy{MaxRetries: 2, Delay: time.Minute}))

			id, err := service.CreateUpdate(context.Background(), tt.base, tt.quote)

			if !errors.Is(err, tt.expectedErr) {
				t.Errorf("CreateUpdate() error = %v, expected %v", err, tt.expectedErr)
			}
			if id != uuid.Nil {
				t.Errorf("CreateUpdate() ID = %s, want nil UUID", id)
			}
			if repository.createCalls != tt.wantCalls {
				t.Errorf("Create() calls = %d, want %d", repository.createCalls, tt.wantCalls)
			}
		})
	}
}

func (*fakeTaskRepository) ClaimNextTask(context.Context, time.Duration) (models.UpdateTask, bool, error) {
	return models.UpdateTask{}, false, nil
}
func (*fakeTaskRepository) RenewLease(context.Context, uuid.UUID, uuid.UUID, time.Duration) (bool, error) {
	return true, nil
}

func TestCreateNotifiesOnlyAfterSuccessfulPersistence(t *testing.T) {
	for _, repoErr := range []error{errors.New("database error"), apperrors.ErrQueueFull, nil} {
		repo := &fakeTaskRepository{createErr: repoErr}
		p := lifecyclePool(repo, nil, 1, 1)
		_, err := queueService(repo, p).CreateUpdate(context.Background(), "EUR", "MXN")
		if !errors.Is(err, repoErr) {
			t.Fatalf("error %v want %v", err, repoErr)
		}
		want := 0
		if repoErr == nil {
			want = 1
		}
		if len(p.notify) != want {
			t.Fatal("incorrect wakeup after persistence")
		}
	}
}

func TestRequestCancellationAfterCommitRetainsTask(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	repo := &queueTaskRepository{create: func(context.Context, models.UpdateTask) error { cancel(); return nil }}
	p := lifecyclePool(repo, nil, 1, 1)
	id, err := queueService(repo, p).CreateUpdate(ctx, "EUR", "MXN")
	if err != nil || len(repo.tasks) != 1 || repo.tasks[0].ID != id {
		t.Fatalf("lost committed task: %s %v", id, err)
	}
}
