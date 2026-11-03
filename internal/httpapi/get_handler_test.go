package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/JhoJho19/exchange-rate-service/internal/apperrors"
	"github.com/JhoJho19/exchange-rate-service/internal/models"
	"github.com/JhoJho19/exchange-rate-service/internal/service"
	"github.com/google/uuid"
)

type readService struct {
	updateCreatorFunc
	getUpdate func(context.Context, uuid.UUID) (models.UpdateTask, error)
	getLatest func(context.Context, string, string) (models.Quote, error)
}

func (s readService) GetUpdate(ctx context.Context, id uuid.UUID) (models.UpdateTask, error) {
	return s.getUpdate(ctx, id)
}
func (s readService) GetLatestQuote(ctx context.Context, base, quote string) (models.Quote, error) {
	return s.getLatest(ctx, base, quote)
}

func assertJSONResponse(t *testing.T, w *httptest.ResponseRecorder, status int, body string) {
	t.Helper()
	if w.Code != status || w.Header().Get("Content-Type") != "application/json" || strings.TrimSpace(w.Body.String()) != body {
		t.Fatalf("response: status=%d headers=%v body=%s; want %d %s", w.Code, w.Header(), w.Body, status, body)
	}
}

func TestGetUpdateThroughRouter(t *testing.T) {
	id := uuid.New()
	quote := &models.Quote{Rate: 0.89, UpdatedAt: time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)}
	for _, tc := range []struct {
		name      string
		status    models.UpdateStatus
		quote     *models.Quote
		err       error
		invalidID bool
		code      int
		body      string
	}{
		{name: "completed", status: models.StatusCompleted, quote: quote, code: 200, body: `{"rate":0.89,"updated_at":"2026-11-03T12:00:00Z"}`},
		{name: "pending", status: models.StatusPending, code: 202, body: `{"message":"Exchange rate update is still in progress"}`},
		{name: "processing", status: models.StatusProcessing, code: 202, body: `{"message":"Exchange rate update is still in progress"}`},
		{name: "failed", status: models.StatusFailed, code: 502, body: `{"error":"Failed to fetch exchange rate from external provider"}`},
		{name: "missing quote", status: models.StatusCompleted, code: 500, body: `{"error":"Internal server error"}`},
		{name: "not found", err: apperrors.ErrTaskNotFound, code: 404, body: `{"error":"Update not found"}`},
		{name: "wrapped not found", err: fmt.Errorf("private: %w", apperrors.ErrTaskNotFound), code: 404, body: `{"error":"Update not found"}`},
		{name: "internal", err: errors.New("private database details"), code: 500, body: `{"error":"Internal server error"}`},
		{name: "invalid UUID", invalidID: true, code: 400, body: `{"error":"Invalid update ID"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			var handler = NewHandler(readService{getUpdate: func(got context.Context, gotID uuid.UUID) (models.UpdateTask, error) {
				calls++
				if tc.invalidID || gotID != id || got != ctx {
					t.Fatal("unexpected service call")
				}
				return models.UpdateTask{Status: tc.status, Quote: tc.quote, ErrorMessage: "private provider details"}, tc.err
			}})
			pathID := id.String()
			if tc.invalidID {
				pathID = "invalid"
			}
			w := httptest.NewRecorder()
			NewRouter(handler).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/rates/updates/"+pathID, nil).WithContext(ctx))
			assertJSONResponse(t, w, tc.code, tc.body)
			if !tc.invalidID && calls != 1 {
				t.Fatalf("calls=%d", calls)
			}
		})
	}
}

func TestGetLatestErrorsThroughRouter(t *testing.T) {
	for _, tc := range []struct {
		err     error
		code    int
		message string
	}{
		{apperrors.ErrQuoteNotFound, 404, "Quote not found"},
		{apperrors.ErrInvalidCurrencyPair, 400, "Invalid or unsupported currency pair"},
		{apperrors.ErrUnsupportedCurrency, 400, "Invalid or unsupported currency pair"},
		{errors.New("private database details"), 500, "Internal server error"},
	} {
		for _, wrapped := range []bool{false, true} {
			t.Run(fmt.Sprintf("%v/wrapped=%t", tc.err, wrapped), func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				var handler = NewHandler(readService{getLatest: func(got context.Context, base, quote string) (models.Quote, error) {
					if got != ctx || base != "usd" || quote != "EUR" {
						t.Fatal("unexpected service arguments")
					}
					err := tc.err
					if wrapped {
						err = fmt.Errorf("private: %w", err)
					}
					return models.Quote{}, err
				}})
				w := httptest.NewRecorder()
				NewRouter(handler).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/rates/latest/usd/EUR", nil).WithContext(ctx))
				assertJSONError(t, w, tc.code, tc.message)
			})
		}
	}
}

// Embed the interface so unexpected repository operations fail immediately.
// The fake models the repository's completed-only lookup contract.
type quoteRepository struct {
	service.TaskRepository
	tasks []models.UpdateTask
	calls int
}

func (r *quoteRepository) GetLatestQuote(_ context.Context, pair models.CurrencyPair) (models.Quote, error) {
	r.calls++
	if pair != (models.CurrencyPair{BaseCurrency: "USD", QuoteCurrency: "EUR"}) {
		panic("pair was not normalized")
	}
	var latest *models.Quote
	for _, task := range r.tasks {
		if task.Pair == pair && task.Status == models.StatusCompleted && task.Quote != nil && (latest == nil || task.Quote.UpdatedAt.After(latest.UpdatedAt)) {
			latest = task.Quote
		}
	}
	if latest == nil {
		return models.Quote{}, apperrors.ErrQuoteNotFound
	}
	return *latest, nil
}

func TestGetLatestApplicationThroughRouter(t *testing.T) {
	pair := models.CurrencyPair{BaseCurrency: "USD", QuoteCurrency: "EUR"}
	quote := models.Quote{Pair: pair, Rate: 0.89, UpdatedAt: time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)}
	for _, status := range []models.UpdateStatus{models.StatusPending, models.StatusProcessing, models.StatusFailed} {
		t.Run(string(status), func(t *testing.T) {
			repo := &quoteRepository{tasks: []models.UpdateTask{
				{Pair: pair, Status: models.StatusCompleted, Quote: &models.Quote{Rate: 0.8, UpdatedAt: quote.UpdatedAt.Add(-time.Hour)}},
				{Pair: pair, Status: models.StatusCompleted, Quote: &quote},
				{Pair: pair, Status: status, CreatedAt: quote.UpdatedAt.Add(time.Minute)},
			}}
			var app = service.NewApplicationService(map[string]struct{}{"USD": {}, "EUR": {}}, repo, nil)
			var router = NewRouter(NewHandler(app))
			for _, path := range []string{"/rates/latest/USD/EUR", "/rates/latest/usd/eur", "/rates/latest/%20usd%09/%0Aeur%20"} {
				w := httptest.NewRecorder()
				router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
				assertJSONResponse(t, w, 200, `{"rate":0.89,"updated_at":"2026-11-03T12:00:00Z"}`)
			}
			repo.tasks = repo.tasks[2:]
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/rates/latest/USD/EUR", nil))
			assertJSONError(t, w, 404, "Quote not found")
		})
	}
	for _, path := range []string{"/rates/latest/USD/USD", "/rates/latest/US/EUR", "/rates/latest/USD/XYZ"} {
		var app = service.NewApplicationService(map[string]struct{}{"USD": {}, "EUR": {}}, nil, nil)
		w := httptest.NewRecorder()
		NewRouter(NewHandler(app)).ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		assertJSONError(t, w, 400, "Invalid or unsupported currency pair")
	}
}

func TestCreateUpdateThroughRouter(t *testing.T) {
	id := uuid.New()
	var handler = NewHandler(updateCreatorFunc(func(_ context.Context, base, quote string) (uuid.UUID, error) {
		if base != "USD" || quote != "EUR" {
			t.Fatal("unexpected pair")
		}
		return id, nil
	}))
	w := httptest.NewRecorder()
	NewRouter(handler).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/rates/updates", strings.NewReader(`{"base":"USD","quote":"EUR"}`)))
	assertJSONResponse(t, w, 202, `{"id":"`+id.String()+`"}`)
}
