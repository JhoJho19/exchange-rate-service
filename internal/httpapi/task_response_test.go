package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/JhoJho19/exchange-rate-service/internal/models"
)

func TestResponseForTask(t *testing.T) {
	updatedAt := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	completedAt := updatedAt.Add(time.Second)

	supportedCurrencies := map[string]struct{}{
		"EUR": {}, "MXN": {},
	}

	pair, err := models.NewCurrencyPair("EUR", "MXN", supportedCurrencies)
	if err != nil {
		t.Fatalf("failed to create currency pair: %v", err)
	}

	quote := &models.Quote{
		Pair:      pair,
		Rate:      20.45,
		UpdatedAt: updatedAt,
	}

	tests := []struct {
		name     string
		status   models.UpdateStatus
		quote    *models.Quote
		wantCode int
		wantJSON string
	}{
		{
			name:     "pending",
			status:   models.StatusPending,
			wantCode: http.StatusAccepted,
			wantJSON: `{"message":"Exchange rate update is still in progress"}`,
		},
		{
			name:     "processing",
			status:   models.StatusProcessing,
			wantCode: http.StatusAccepted,
			wantJSON: `{"message":"Exchange rate update is still in progress"}`,
		},
		{
			name:     "completed",
			status:   models.StatusCompleted,
			quote:    quote,
			wantCode: http.StatusOK,
			wantJSON: `{"rate":20.45,"updated_at":"2026-10-30T12:00:00Z"}`,
		},
		{
			name:     "failed",
			status:   models.StatusFailed,
			wantCode: http.StatusBadGateway,
			wantJSON: `{"error":"Failed to fetch exchange rate from external provider"}`,
		},
		{
			name:     "completed without quote",
			status:   models.StatusCompleted,
			wantCode: http.StatusInternalServerError,
			wantJSON: `{"error":"Internal server error"}`,
		},
		{
			name:     "unknown status",
			status:   models.UpdateStatus("UNKNOWN"),
			wantCode: http.StatusInternalServerError,
			wantJSON: `{"error":"Internal server error"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := models.UpdateTask{
				ID:           uuid.New(),
				Pair:         pair,
				Status:       tt.status,
				Quote:        tt.quote,
				ErrorMessage: "internal error details",
				CreatedAt:    updatedAt.Add(-time.Minute),
				CompletedAt:  &completedAt,
			}

			gotCode, gotBody := responseForTask(task)

			if gotCode != tt.wantCode {
				t.Errorf("HTTP code = %d, want %d", gotCode, tt.wantCode)
			}

			gotJSON, err := json.Marshal(gotBody)
			if err != nil {
				t.Fatalf("failed to marshal response: %v", err)
			}

			if string(gotJSON) != tt.wantJSON {
				t.Errorf("JSON = %s, want %s", gotJSON, tt.wantJSON)
			}
		})
	}
}
