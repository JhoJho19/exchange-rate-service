package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/JhoJho19/exchange-rate-service/internal/apperrors"
	"github.com/JhoJho19/exchange-rate-service/internal/models"
	"github.com/JhoJho19/exchange-rate-service/internal/service"
	"github.com/google/uuid"
)

// Like the recovery tests' repository, this fake retains tasks by ID and
// conditionally transitions their status. It does not verify PostgreSQL SQL.
type workflowRepository struct {
	service.TaskRepository
	mu    sync.Mutex
	tasks map[uuid.UUID]models.UpdateTask
}

func (r *workflowRepository) Create(_ context.Context, task models.UpdateTask) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, existing := range r.tasks {
		if existing.Status == models.StatusPending || existing.Status == models.StatusProcessing {
			return apperrors.ErrQueueFull
		}
	}
	r.tasks[task.ID] = task
	return nil
}

func (r *workflowRepository) GetByID(_ context.Context, id uuid.UUID) (models.UpdateTask, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	task, ok := r.tasks[id]
	if !ok {
		return models.UpdateTask{}, apperrors.ErrTaskNotFound
	}
	return task, nil
}

func (r *workflowRepository) CompleteTask(_ context.Context, id uuid.UUID, token uuid.UUID, quote models.Quote, at time.Time) (bool, error) {
	return r.finish(id, &quote, "", at)
}

func (r *workflowRepository) FailTask(_ context.Context, id uuid.UUID, token uuid.UUID, message string, at time.Time) (bool, error) {
	return r.finish(id, nil, message, at)
}

func (r *workflowRepository) finish(id uuid.UUID, quote *models.Quote, message string, at time.Time) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	task, ok := r.tasks[id]
	if !ok || task.Status != models.StatusProcessing {
		return false, nil
	}
	task.Status = models.StatusCompleted
	if quote == nil {
		task.Status = models.StatusFailed
	}
	task.Quote, task.ErrorMessage, task.CompletedAt = quote, message, &at
	r.tasks[id] = task
	return true, nil
}

func (r *workflowRepository) GetLatestQuote(_ context.Context, pair models.CurrencyPair) (models.Quote, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
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

type workflowProvider func(context.Context, models.CurrencyPair) (float64, error)

func (p workflowProvider) GetRate(ctx context.Context, pair models.CurrencyPair) (float64, error) {
	return p(ctx, pair)
}

func TestAsynchronousHTTPWorkflow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		repo := &workflowRepository{tasks: make(map[uuid.UUID]models.UpdateTask)}
		type outcome struct {
			rate float64
			err  error
		}
		responses := make(chan outcome)
		pair := models.CurrencyPair{BaseCurrency: "EUR", QuoteCurrency: "MXN"}
		provider := workflowProvider(func(ctx context.Context, got models.CurrencyPair) (float64, error) {
			if got != pair {
				t.Errorf("provider pair = %v, want %v", got, pair)
			}
			select {
			case result := <-responses:
				return result.rate, result.err
			case <-ctx.Done():
				return 0, ctx.Err()
			}
		})
		pool := service.NewWorkerPool(1, repo, provider, time.Minute, service.NewRateLimiter(unlimitedTestPermits{}), service.RetryPolicy{Delay: time.Second})
		ctx, cancel := context.WithCancel(context.Background())
		defer func() {
			cancel()
			if err := pool.Stop(context.Background()); err != nil {
				t.Error(err)
			}
		}()
		app := service.NewApplicationService(map[string]struct{}{"EUR": {}, "MXN": {}}, repo, pool)
		router := NewRouter(NewHandler(app))
		request := func(method, path, body string, status int) *httptest.ResponseRecorder {
			t.Helper()
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
			if w.Code != status || w.Header().Get("Content-Type") != "application/json" {
				t.Fatalf("%s %s: status=%d body=%s; want %d JSON", method, path, w.Code, w.Body, status)
			}
			return w
		}
		create := func() uuid.UUID {
			t.Helper()
			w := request(http.MethodPost, "/rates/updates", `{"base":" eur ","quote":" mxn "}`, http.StatusAccepted)
			var accepted UpdateAcceptedResponse
			if err := json.Unmarshal(w.Body.Bytes(), &accepted); err != nil {
				t.Fatal(err)
			}
			if accepted.ID == uuid.Nil || accepted.ID.Version() != 4 || accepted.ID.Variant() != uuid.RFC4122 {
				t.Fatalf("invalid task UUID: %s", accepted.ID)
			}
			return accepted.ID
		}
		task := func(id uuid.UUID, status models.UpdateStatus) models.UpdateTask {
			t.Helper()
			got, err := app.GetUpdate(context.Background(), id)
			if err != nil || got.Status != status {
				t.Fatalf("task %s: %+v, %v; want %s", id, got, err, status)
			}
			return got
		}
		assertQuote := func(path string, want models.Quote) {
			t.Helper()
			w := request(http.MethodGet, path, "", http.StatusOK)
			var got TaskResponse
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.Rate == nil || *got.Rate != want.Rate || got.UpdatedAt == nil || !got.UpdatedAt.Equal(want.UpdatedAt) {
				t.Fatalf("quote response=%s; want rate=%v at %v", w.Body, want.Rate, want.UpdatedAt)
			}
		}
		const latest = "/rates/latest/EUR/MXN"
		updatePath := func(id uuid.UUID) string { return "/rates/updates/" + id.String() }
		request(http.MethodGet, latest, "", http.StatusNotFound)
		request(http.MethodGet, updatePath(uuid.New()), "", http.StatusNotFound)
		first := create()
		if pending := task(first, models.StatusPending); pending.CompletedAt != nil || pending.Quote != nil {
			t.Fatal("pending task already finalized")
		}
		request(http.MethodGet, updatePath(first), "", http.StatusAccepted)
		// No workers have started, so the single queue slot is deterministically full.
		request(http.MethodPost, "/rates/updates", `{"base":"EUR","quote":"MXN"}`, http.StatusServiceUnavailable)
		if len(repo.tasks) != 1 {
			t.Fatal("rejected task was persisted")
		}
		if err := pool.Start(ctx); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if processing := task(first, models.StatusProcessing); processing.CompletedAt != nil {
			t.Fatal("processing task already finalized")
		}
		request(http.MethodGet, updatePath(first), "", http.StatusAccepted)
		request(http.MethodGet, latest, "", http.StatusNotFound)

		var original, newest models.UpdateTask
		for i, result := range []outcome{{rate: 20}, {rate: 21}, {err: apperrors.ErrProviderUnavailable}} {
			id := first
			if i > 0 {
				id = create()
				if id == first || (i == 2 && id == newest.ID) {
					t.Fatal("task UUID reused")
				}
				synctest.Wait()
				task(id, models.StatusProcessing)
				request(http.MethodGet, updatePath(id), "", http.StatusAccepted)
				assertQuote(latest, *newest.Quote)
			}
			// Advance virtual time, not wall time, while the provider is blocked.
			// This distinguishes receipt time from creation and orders completed quotes.
			time.Sleep(time.Second)
			receivedAt := time.Now()
			responses <- result
			synctest.Wait()
			status := models.StatusCompleted
			if result.err != nil {
				status = models.StatusFailed
			}
			completed := task(id, status)
			if completed.CompletedAt == nil || !completed.CompletedAt.Equal(receivedAt) {
				t.Fatalf("CompletedAt=%v, want %v", completed.CompletedAt, receivedAt)
			}
			if result.err != nil {
				request(http.MethodGet, updatePath(id), "", http.StatusBadGateway)
				if completed.Quote != nil {
					t.Fatal("failed task retained a quote")
				}
			} else {
				if completed.Quote == nil || completed.Quote.Rate != result.rate || !completed.Quote.UpdatedAt.Equal(receivedAt) {
					t.Fatalf("unexpected completed quote: %+v", completed.Quote)
				}
				newest = completed
				if i == 0 {
					original = completed
				}
				assertQuote(updatePath(id), *completed.Quote)
			}
			assertQuote(latest, *newest.Quote)
			assertQuote(updatePath(first), *original.Quote)
			historical := task(first, models.StatusCompleted)
			if !historical.CompletedAt.Equal(*original.CompletedAt) {
				t.Fatal("historical completion time changed")
			}
		}
		// Retrieval much later must return stored instants, not read-time timestamps.
		time.Sleep(time.Hour)
		assertQuote(updatePath(first), *original.Quote)
		assertQuote(latest, *newest.Quote)
	})
}

func (r *workflowRepository) ClaimNextTask(context.Context, time.Duration) (models.UpdateTask, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, task := range r.tasks {
		if task.Status == models.StatusPending {
			task.Status = models.StatusProcessing
			task.LeaseToken = uuid.New()
			r.tasks[id] = task
			return task, true, nil
		}
	}
	return models.UpdateTask{}, false, nil
}
func (*workflowRepository) RenewLease(context.Context, uuid.UUID, uuid.UUID, time.Duration) (bool, error) {
	return true, nil
}

// HTTP workflow tests exercise routing and persistence, not quota admission.
type unlimitedTestPermits struct{}

func (unlimitedTestPermits) AcquirePermit(context.Context) (time.Duration, error) { return 0, nil }
func (unlimitedTestPermits) PauseUntil(context.Context, time.Time) error          { return nil }
