package httpapi

import (
	"context"
	"encoding/json"
	"github.com/JhoJho19/exchange-rate-service/internal/models"
	"github.com/JhoJho19/exchange-rate-service/internal/service"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/JhoJho19/exchange-rate-service/internal/apperrors"
	"github.com/google/uuid"
)

type keyedCreator struct {
	updateCreatorFunc
	create func(*string) (uuid.UUID, error)
}

func (s keyedCreator) CreateUpdateWithKey(_ context.Context, _, _ string, key *string) (uuid.UUID, error) {
	return s.create(key)
}
func TestIdempotencyHeader(t *testing.T) {
	for _, tc := range []struct {
		name    string
		values  []string
		present bool
		valid   bool
	}{
		{"missing", nil, false, true}, {"new", []string{"key"}, true, true},
		{"opaque", []string{" key "}, true, true}, {"255 bytes", []string{strings.Repeat("x", 255)}, true, true},
		{"empty", []string{""}, true, false}, {"whitespace", []string{" \t "}, true, false},
		{"too long", []string{strings.Repeat("x", 256)}, true, false}, {"multibyte too long", []string{strings.Repeat("é", 128)}, true, false},
		{"multiple", []string{"a", "b"}, true, false}, {"same twice", []string{"a", "a"}, true, false},
		{"coalesced", []string{"a, b"}, true, false}, {"no values", nil, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			var h = NewHandler(keyedCreator{create: func(key *string) (uuid.UUID, error) {
				calls++
				if tc.present {
					if key == nil || *key != tc.values[0] {
						t.Fatal("key transformed")
					}
				} else if key != nil {
					t.Fatal("missing key supplied")
				}
				return uuid.New(), nil
			}})
			r := httptest.NewRequest(http.MethodPost, "/rates/updates", strings.NewReader(`{"base":"EUR","quote":"MXN"}`))
			if tc.present {
				r.Header["Idempotency-Key"] = tc.values
			}
			w := httptest.NewRecorder()
			h.CreateUpdate(w, r)
			if tc.valid {
				if w.Code != 202 || calls != 1 {
					t.Fatalf("status %d calls %d", w.Code, calls)
				}
			} else {
				assertJSONError(t, w, 400, "Invalid idempotency key")
				if calls != 0 {
					t.Fatal("invalid header reached service")
				}
			}
		})
	}
}
func TestIdempotencyConflictResponse(t *testing.T) {
	var h = NewHandler(keyedCreator{create: func(*string) (uuid.UUID, error) { return uuid.Nil, apperrors.ErrIdempotencyConflict }})
	r := httptest.NewRequest(http.MethodPost, "/rates/updates", strings.NewReader(`{"base":"EUR","quote":"MXN"}`))
	r.Header.Set("Idempotency-Key", "key")
	w := httptest.NewRecorder()
	h.CreateUpdate(w, r)
	assertJSONError(t, w, 409, "Idempotency key already used for a different currency pair")
}

type keyedWorkflowRepository struct {
	workflowRepository
	keys map[string]uuid.UUID
}

func (r *keyedWorkflowRepository) CreateOrGet(_ context.Context, key string, pair models.CurrencyPair, prepare func() (models.UpdateTask, error)) (uuid.UUID, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if id, ok := r.keys[key]; ok {
		if r.tasks[id].Pair != pair {
			return uuid.Nil, false, apperrors.ErrIdempotencyConflict
		}
		return id, false, nil
	}
	task, err := prepare()
	if err != nil {
		return uuid.Nil, false, err
	}
	r.tasks[task.ID] = task
	r.keys[key] = task.ID
	return task.ID, true, nil
}
func TestIdempotentHTTPWorkflow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		repo := &keyedWorkflowRepository{workflowRepository: workflowRepository{tasks: make(map[uuid.UUID]models.UpdateTask)}, keys: make(map[string]uuid.UUID)}
		calls := 0
		provider := workflowProvider(func(context.Context, models.CurrencyPair) (float64, error) { calls++; return 20, nil })
		var pool = service.NewWorkerPool(1, repo, provider, time.Second, service.NewRateLimiter(unlimitedTestPermits{}), service.RetryPolicy{Delay: time.Second})
		var app = service.NewApplicationService(map[string]struct{}{"EUR": {}, "MXN": {}}, repo, pool)
		var handler = NewHandler(app)
		request := func(key string) uuid.UUID {
			t.Helper()
			r := httptest.NewRequest(http.MethodPost, "/rates/updates", strings.NewReader(`{"base":" eur ","quote":"mxn"}`))
			if key != "" {
				r.Header.Set("Idempotency-Key", key)
			}
			w := httptest.NewRecorder()
			handler.CreateUpdate(w, r)
			if w.Code != 202 {
				t.Fatalf("response: %d %s", w.Code, w.Body)
			}
			var accepted UpdateAcceptedResponse
			if err := json.Unmarshal(w.Body.Bytes(), &accepted); err != nil {
				t.Fatal(err)
			}
			return accepted.ID
		}
		id := request("key")
		if request("key") != id {
			t.Fatal("pending repeat changed ID")
		}
		if err := pool.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		defer pool.Stop(context.Background())
		synctest.Wait()
		if request("key") != id {
			t.Fatal("completed repeat changed ID")
		}
		synctest.Wait()
		if calls != 1 {
			t.Fatalf("provider calls: %d", calls)
		}
		first := request("")
		synctest.Wait()
		second := request("")
		synctest.Wait()
		if first == second || first == id || second == id || len(repo.tasks) != 3 || calls != 3 {
			t.Fatal("unkeyed requests not independent")
		}
	})
}
