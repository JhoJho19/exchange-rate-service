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

func (*fakeTaskRepository) MarkProcessing(context.Context, uuid.UUID) error {
	panic("unexpected MarkProcessing call")
}

func (*fakeTaskRepository) MarkCompleted(context.Context, uuid.UUID, models.Quote) error {
	panic("unexpected MarkCompleted call")
}

func (*fakeTaskRepository) MarkFailed(context.Context, uuid.UUID, string) error {
	panic("unexpected MarkFailed call")
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
		service := NewApplicationService(supportedCurrencies, repository)

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
			service := NewApplicationService(supportedCurrencies, repository)

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
