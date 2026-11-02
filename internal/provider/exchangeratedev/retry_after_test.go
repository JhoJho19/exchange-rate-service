package exchangeratedev

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/JhoJho19/exchange-rate-service/internal/apperrors"
	"github.com/JhoJho19/exchange-rate-service/internal/models"
)

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		value string
		want  time.Time
	}{
		{"60", now.Add(time.Minute)}, {" 60 ", now.Add(time.Minute)}, {"0", now},
		{now.Add(time.Hour).Format(http.TimeFormat), now.Add(time.Hour)},
		{now.Add(-time.Hour).Format(http.TimeFormat), now.Add(-time.Hour)},
		{"", time.Time{}}, {"-1", time.Time{}}, {"+1", time.Time{}}, {"1.5", time.Time{}},
		{"invalid", time.Time{}}, {"999999999999999999999999", time.Time{}}, {"9223372036854775807", time.Time{}},
	} {
		t.Run(tc.value, func(t *testing.T) {
			if got := parseRetryAfter(tc.value, now); !got.Equal(tc.want) {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

type retryTransport func(*http.Request) (*http.Response, error)

func (f retryTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestGetRatePreservesRetryAfter(t *testing.T) {
	for _, header := range []string{"60", "Wed, 02 Dec 2026 12:00:00 GMT", "", "invalid"} {
		t.Run(header, func(t *testing.T) {
			client := NewClient("", "https://provider.example/v1", time.Second)
			client.httpClient.Transport = retryTransport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": []string{header}}, Body: io.NopCloser(strings.NewReader("untrusted diagnostics"))}, nil
			})
			before := time.Now()
			_, err := client.GetRate(context.Background(), models.CurrencyPair{BaseCurrency: "EUR", QuoteCurrency: "MXN"})
			after := time.Now()
			var limited *apperrors.ProviderRateLimitError
			if !errors.Is(err, apperrors.ErrProviderRateLimited) || !errors.As(err, &limited) {
				t.Fatalf("error lost rate-limit identity: %v", err)
			}
			if err.Error() != apperrors.ErrProviderRateLimited.Error() {
				t.Fatal("unsafe error message")
			}
			switch header {
			case "60":
				if limited.RetryAt.Before(before.Add(time.Minute)) || limited.RetryAt.After(after.Add(time.Minute)) {
					t.Fatalf("incorrect retry time: %v", limited.RetryAt)
				}
			case "", "invalid":
				if !limited.RetryAt.IsZero() {
					t.Fatal("invalid header should use fallback")
				}
			default:
				want, _ := http.ParseTime(header)
				if !limited.RetryAt.Equal(want) {
					t.Fatalf("got %v want %v", limited.RetryAt, want)
				}
			}
		})
	}
}
