package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/JhoJho19/exchange-rate-service/internal/apperrors"
	"github.com/JhoJho19/exchange-rate-service/internal/service"
	"github.com/google/uuid"
)

type updateCreatorFunc func(context.Context, string, string) (uuid.UUID, error)

func (f updateCreatorFunc) CreateUpdate(ctx context.Context, base, quote string) (uuid.UUID, error) {
	return f(ctx, base, quote)
}

func TestCreateUpdateSuccess(t *testing.T) {
	id := uuid.New()
	type contextKey struct{}
	ctx := context.WithValue(context.Background(), contextKey{}, "request")
	calls := 0
	h := NewHandler(updateCreatorFunc(func(gotCtx context.Context, base, quote string) (uuid.UUID, error) {
		calls++
		if gotCtx != ctx || base != "usd" || quote != "EUR" {
			t.Fatalf("unexpected service arguments: %v, %q, %q", gotCtx, base, quote)
		}
		return id, nil
	}))
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/rates/updates", strings.NewReader(`{"base":"usd","quote":"EUR"} `)).WithContext(ctx)
	h.CreateUpdate(w, r)
	if w.Code != http.StatusAccepted || w.Header().Get("Content-Type") != "application/json" || calls != 1 {
		t.Fatalf("unexpected response: code=%d headers=%v calls=%d", w.Code, w.Header(), calls)
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	parsed, err := uuid.Parse(body["id"])
	if err != nil || parsed != id || len(body) != 1 {
		t.Fatalf("unexpected success body: %s", w.Body)
	}
}

func assertJSONError(t *testing.T, w *httptest.ResponseRecorder, status int, message string) {
	t.Helper()
	if w.Code != status || w.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("unexpected response: code=%d headers=%v", w.Code, w.Header())
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON error: %v; body=%s", err, w.Body)
	}
	if len(body) != 1 || body["error"] != message {
		t.Fatalf("unexpected error body: %s", w.Body)
	}
}

func TestCreateUpdateInvalidJSON(t *testing.T) {
	for name, body := range map[string]string{
		"malformed":            `{"base":`,
		"empty":                "",
		"whitespace":           " \n\t",
		"multiple objects":     `{"base":"USD","quote":"EUR"} {}`,
		"trailing scalar":      `{"base":"USD","quote":"EUR"} true`,
		"trailing garbage":     `{"base":"USD","quote":"EUR"} broken`,
		"unknown field":        `{"base":"USD","quote":"EUR","extra":true}`,
		"oversized value":      `{"base":"` + strings.Repeat("X", maxCreateUpdateBodyBytes) + `","quote":"EUR"}`,
		"oversized whitespace": `{"base":"USD","quote":"EUR"}` + strings.Repeat(" ", maxCreateUpdateBodyBytes),
		"null":                 `null`,
		"array":                `[]`,
		"wrong field type":     `{"base":123,"quote":"EUR"}`,
	} {
		t.Run(name, func(t *testing.T) {
			h := NewHandler(updateCreatorFunc(func(context.Context, string, string) (uuid.UUID, error) {
				t.Fatal("invalid request reached service")
				return uuid.Nil, nil
			}))
			w := httptest.NewRecorder()
			h.CreateUpdate(w, httptest.NewRequest(http.MethodPost, "/rates/updates", strings.NewReader(body)))
			assertJSONError(t, w, http.StatusBadRequest, "Invalid JSON request")
		})
	}
}

func TestCreateUpdateServiceErrors(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		status  int
		message string
	}{
		{"invalid pair", apperrors.ErrInvalidCurrencyPair, 400, "Invalid currency pair"},
		{"unsupported currency", apperrors.ErrUnsupportedCurrency, 400, "Unsupported currency"},
		{"overloaded", apperrors.ErrServiceOverloaded, 503, "Service temporarily unavailable"},
		{"queue full", service.ErrQueueFull, 503, "Service temporarily unavailable"},
		{"repository error", errors.New("database credentials and query details"), 500, "Internal server error"},
		{"worker error", errors.New("worker failed"), 500, "Internal server error"},
		{"canceled", context.Canceled, 500, "Internal server error"},
		{"deadline", context.DeadlineExceeded, 500, "Internal server error"},
	}
	for _, tt := range tests {
		for _, wrapped := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/wrapped=%t", tt.name, wrapped), func(t *testing.T) {
				err := tt.err
				if wrapped {
					err = fmt.Errorf("private implementation details: %w", err)
				}
				h := NewHandler(updateCreatorFunc(func(context.Context, string, string) (uuid.UUID, error) {
					return uuid.Nil, err
				}))
				w := httptest.NewRecorder()
				h.CreateUpdate(w, httptest.NewRequest(http.MethodPost, "/rates/updates", strings.NewReader(`{"base":"USD","quote":"EUR"}`)))
				assertJSONError(t, w, tt.status, tt.message)
			})
		}
	}
}

func TestCreateUpdateDomainValidation(t *testing.T) {
	// Invalid pairs fail in the real application before any repository or
	// worker access; nil dependencies ensure these tests remain isolated.
	h := NewHandler(service.NewApplicationService(map[string]struct{}{"USD": {}, "EUR": {}}, nil, nil))
	for _, tt := range []struct{ body, message string }{
		{`{}`, "Invalid currency pair"},
		{`{"base":"USD","quote":"USD"}`, "Invalid currency pair"},
		{`{"base":"USD","quote":"XYZ"}`, "Unsupported currency"},
	} {
		w := httptest.NewRecorder()
		h.CreateUpdate(w, httptest.NewRequest(http.MethodPost, "/rates/updates", strings.NewReader(tt.body)))
		assertJSONError(t, w, http.StatusBadRequest, tt.message)
	}
}

type failingResponseWriter struct {
	header   http.Header
	statuses []int
	writes   int
}

func (w *failingResponseWriter) Header() http.Header       { return w.header }
func (w *failingResponseWriter) WriteHeader(status int)    { w.statuses = append(w.statuses, status) }
func (w *failingResponseWriter) Write([]byte) (int, error) { w.writes++; return 0, io.ErrClosedPipe }

func TestCreateUpdateWriteFailure(t *testing.T) {
	for _, serviceErr := range []error{nil, errors.New("internal failure")} {
		w := &failingResponseWriter{header: make(http.Header)}
		h := NewHandler(updateCreatorFunc(func(context.Context, string, string) (uuid.UUID, error) {
			return uuid.New(), serviceErr
		}))
		h.CreateUpdate(w, httptest.NewRequest(http.MethodPost, "/rates/updates", strings.NewReader(`{"base":"USD","quote":"EUR"}`)))
		if len(w.statuses) != 1 || w.writes != 1 {
			t.Fatalf("attempted second response: statuses=%v writes=%d", w.statuses, w.writes)
		}
	}
}
