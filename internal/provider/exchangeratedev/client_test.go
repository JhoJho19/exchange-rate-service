package exchangeratedev

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
)

var testPair = models.CurrencyPair{BaseCurrency: "USD", QuoteCurrency: "EUR"}

const validRateResponse = `{"result":"success","base":"USD","rates":{"EUR":0.92}}`

func TestGetRateRequest(t *testing.T) {
	for _, tt := range []struct {
		name, baseSuffix, apiKey, path string
	}{
		{"root without key", "", "", "/latest/USD"},
		{"root trailing slash", "/", "", "/latest/USD"},
		{"prefix with key", "/v1", "test-key", "/v1/latest/USD"},
		{"prefix trailing slash and query", "/v1/?symbols=GBP&other=kept", "test-key", "/v1/latest/USD"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != tt.path {
					t.Errorf("request = %s %s, want GET %s", r.Method, r.URL.Path, tt.path)
				}
				if values := r.URL.Query()["symbols"]; len(values) != 1 || values[0] != "EUR" {
					t.Errorf("symbols = %v, want [EUR]", values)
				}
				if strings.Contains(tt.baseSuffix, "other=") && r.URL.Query().Get("other") != "kept" {
					t.Error("configured query parameter was lost")
				}
				if got := r.Header.Get("Accept"); got != "application/json" {
					t.Errorf("Accept = %q", got)
				}
				if tt.apiKey == "" {
					if _, present := r.Header["Authorization"]; present {
						t.Error("Authorization header present without API key")
					}
				} else if got := r.Header.Get("Authorization"); got != "Bearer "+tt.apiKey {
					t.Errorf("Authorization = %q, want Bearer %s", got, tt.apiKey)
				}
				fmt.Fprint(w, validRateResponse)
			}))
			t.Cleanup(server.Close)
			client := NewClient(tt.apiKey, server.URL+tt.baseSuffix, time.Second)
			rate, err := client.GetRate(context.Background(), testPair)
			if !errors.Is(err, nil) || rate != 0.92 {
				t.Fatalf("GetRate() = (%v, %v), want (0.92, nil)", rate, err)
			}
		})
	}
}

func TestGetRateResponse(t *testing.T) {
	for _, tt := range []struct {
		name, body string
		wantRate   float64
		wantErr    error
	}{
		{"additional fields", `{"result":"success","base":"USD","rates":{"EUR":0.92,"GBP":0.8},"extra":{"nested":true}}`, 0.92, nil},
		// GetRate returns only a float64. Even unusable timestamp values must not
		// affect its result; assignment of Quote.UpdatedAt belongs to the service.
		{"provider timestamps ignored", `{"result":"success","base":"USD","rates":{"EUR":0.92},"timestamp":{"invalid":true},"effective_at":"not-a-date","data_updated_at":[false]}`, 0.92, nil},
		{"missing currency", `{"result":"success","base":"USD","rates":{"GBP":0.8}}`, 0, apperrors.ErrProviderInvalidResponse},
		{"zero rate", `{"result":"success","base":"USD","rates":{"EUR":0}}`, 0, apperrors.ErrProviderInvalidResponse},
		{"negative rate", `{"result":"success","base":"USD","rates":{"EUR":-1}}`, 0, apperrors.ErrProviderInvalidResponse},
		{"malformed JSON", `{"result":`, 0, apperrors.ErrProviderInvalidResponse},
		{"wrong base", `{"result":"success","base":"GBP","rates":{"EUR":0.92}}`, 0, apperrors.ErrProviderInvalidResponse},
		{"missing result", `{"base":"USD","rates":{"EUR":0.92}}`, 0, apperrors.ErrProviderInvalidResponse},
		{"empty result", `{"result":"","base":"USD","rates":{"EUR":0.92}}`, 0, apperrors.ErrProviderInvalidResponse},
		{"unknown result", `{"result":"unknown","base":"USD","rates":{"EUR":0.92}}`, 0, apperrors.ErrProviderInvalidResponse},
		{"wrong result type", `{"result":123,"base":"USD","rates":{"EUR":0.92}}`, 0, apperrors.ErrProviderInvalidResponse},
		{"provider error with HTTP 200", `{"result":"error","code":"invalid_pair"}`, 0, apperrors.ErrProviderUnsupportedPair},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, tt.body)
			}))
			t.Cleanup(server.Close)
			rate, err := NewClient("", server.URL, time.Second).GetRate(context.Background(), testPair)
			if !errors.Is(err, tt.wantErr) || rate != tt.wantRate {
				t.Fatalf("GetRate() = (%v, %v), want (%v, %v)", rate, err, tt.wantRate, tt.wantErr)
			}
		})
	}
}

func TestGetRateHTTPErrors(t *testing.T) {
	// All provider code mappings already have exhaustive helper tests. These
	// cases check HTTP integration and status precedence over a conflicting code.
	for _, tt := range []struct {
		name    string
		status  int
		body    string
		wantErr error
	}{
		{"known code", 400, `{"result":"error","code":"invalid_pair"}`, apperrors.ErrProviderUnsupportedPair},
		{"unknown code", 400, `{"result":"error","code":"unknown"}`, apperrors.ErrProviderInvalidResponse},
		{"missing code", 400, `{"result":"error"}`, apperrors.ErrProviderInvalidResponse},
		{"unauthorized", 401, `{"result":"error","code":"invalid_pair"}`, apperrors.ErrProviderUnauthorized},
		{"forbidden", 403, "", apperrors.ErrProviderForbidden},
		{"request timeout", 408, "", apperrors.ErrProviderTimeout},
		{"rate limited", 429, "", apperrors.ErrProviderRateLimited},
		{"internal error", 500, "", apperrors.ErrProviderUnavailable},
		{"bad gateway", 502, "", apperrors.ErrProviderUnavailable},
		{"unavailable", 503, "", apperrors.ErrProviderUnavailable},
		{"gateway timeout", 504, "", apperrors.ErrProviderTimeout},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				fmt.Fprint(w, tt.body)
			}))
			t.Cleanup(server.Close)
			rate, err := NewClient("", server.URL, time.Second).GetRate(context.Background(), testPair)
			if !errors.Is(err, tt.wantErr) || rate != 0 {
				t.Fatalf("GetRate() = (%v, %v), want (0, %v)", rate, err, tt.wantErr)
			}
		})
	}
}

func TestGetRateCancellationAndTimeouts(t *testing.T) {
	for _, name := range []string{"cancellation", "context deadline", "client timeout"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			clientTimeout := 5 * time.Second
			wantErr := apperrors.ErrProviderTimeout
			switch name {
			case "cancellation":
				wantErr = context.Canceled
			case "context deadline":
				var deadlineCancel context.CancelFunc
				ctx, deadlineCancel = context.WithTimeout(ctx, 100*time.Millisecond)
				defer deadlineCancel()
			case "client timeout":
				clientTimeout = 100 * time.Millisecond
			}
			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if name == "cancellation" {
					// Cancel only after the server receives the request.
					cancel()
				}
				select {
				case <-r.Context().Done():
				case <-release:
				}
			}))
			t.Cleanup(server.Close)
			t.Cleanup(func() { close(release) })
			rate, err := NewClient("", server.URL, clientTimeout).GetRate(ctx, testPair)
			if !errors.Is(err, wantErr) || rate != 0 {
				t.Fatalf("GetRate() = (%v, %v), want (0, %v)", rate, err, wantErr)
			}
		})
	}
}

func TestGetRateServerUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("closed server unexpectedly received request")
	}))
	server.Close()
	rate, err := NewClient("", server.URL, time.Second).GetRate(context.Background(), testPair)
	if !errors.Is(err, apperrors.ErrProviderUnavailable) || rate != 0 {
		t.Fatalf("GetRate() = (%v, %v), want (0, %v)", rate, err, apperrors.ErrProviderUnavailable)
	}
}

func TestGetRateBodyReadError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A valid JSON body shorter than Content-Length causes unexpected EOF
		// during reading, rather than a JSON decoding failure.
		w.Header().Set("Content-Length", fmt.Sprint(len(validRateResponse)+10))
		fmt.Fprint(w, validRateResponse)
	}))
	t.Cleanup(server.Close)
	rate, err := NewClient("", server.URL, time.Second).GetRate(context.Background(), testPair)
	if !errors.Is(err, apperrors.ErrProviderUnavailable) || rate != 0 {
		t.Fatalf("GetRate() = (%v, %v), want (0, %v)", rate, err, apperrors.ErrProviderUnavailable)
	}
}

func TestGetRateResponseSizeLimit(t *testing.T) {
	for _, size := range []int{maxResponseSize, maxResponseSize + 1} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			// JSON whitespace keeps both payloads valid; only size distinguishes them.
			body := validRateResponse + strings.Repeat(" ", size-len(validRateResponse))
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, body)
			}))
			t.Cleanup(server.Close)
			wantRate, wantErr := 0.92, error(nil)
			if size > maxResponseSize {
				wantRate, wantErr = 0, apperrors.ErrProviderInvalidResponse
			}
			rate, err := NewClient("", server.URL, time.Second).GetRate(context.Background(), testPair)
			if !errors.Is(err, wantErr) || rate != wantRate {
				t.Fatalf("GetRate() = (%v, %v), want (%v, %v)", rate, err, wantRate, wantErr)
			}
		})
	}
}
