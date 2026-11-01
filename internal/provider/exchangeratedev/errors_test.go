package exchangeratedev

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/JhoJho19/exchange-rate-service/internal/apperrors"
)

func TestMapHTTPStatus(t *testing.T) {
	tests := []struct {
		name        string
		statusCode  int
		expectedErr error
	}{
		{"unauthorized", http.StatusUnauthorized, apperrors.ErrProviderUnauthorized},
		{"forbidden", http.StatusForbidden, apperrors.ErrProviderForbidden},
		{"rate limited", http.StatusTooManyRequests, apperrors.ErrProviderRateLimited},
		{"request timeout", http.StatusRequestTimeout, apperrors.ErrProviderTimeout},
		{"gateway timeout", http.StatusGatewayTimeout, apperrors.ErrProviderTimeout},
		{"internal server error", http.StatusInternalServerError, apperrors.ErrProviderUnavailable},
		{"service unavailable", http.StatusServiceUnavailable, apperrors.ErrProviderUnavailable},
		{"OK", http.StatusOK, nil},
		{"bad request", http.StatusBadRequest, nil},
		{"not found", http.StatusNotFound, nil},
		{"unprocessable entity", http.StatusUnprocessableEntity, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := mapHTTPStatus(tt.statusCode); !errors.Is(err, tt.expectedErr) {
				t.Errorf("mapHTTPStatus(%d) = %v, expected %v", tt.statusCode, err, tt.expectedErr)
			}
		})
	}
}

func TestMapProviderError(t *testing.T) {
	tests := []struct {
		code        string
		expectedErr error
	}{
		{"invalid_pair", apperrors.ErrProviderUnsupportedPair},
		{"unsupported_base", apperrors.ErrProviderUnsupportedPair},
		{"invalid_api_key", apperrors.ErrProviderUnauthorized},
		{"forbidden", apperrors.ErrProviderForbidden},
		{"plan_gated", apperrors.ErrProviderForbidden},
		{"rate_limited", apperrors.ErrProviderRateLimited},
		{"ip_rate_limited", apperrors.ErrProviderRateLimited},
		{"quota_exceeded", apperrors.ErrProviderRateLimited},
		{"data_unavailable", apperrors.ErrProviderQuoteUnavailable},
		{"no_data_for_date", apperrors.ErrProviderQuoteUnavailable},
		{"live_unavailable", apperrors.ErrProviderQuoteUnavailable},
		{"source_unavailable", apperrors.ErrProviderQuoteUnavailable},
		{"auth_unavailable", apperrors.ErrProviderUnavailable},
		{"quota_unavailable", apperrors.ErrProviderUnavailable},
		{"service_unavailable", apperrors.ErrProviderUnavailable},
		{"unknown_code", apperrors.ErrProviderInvalidResponse},
	}

	for _, tt := range tests {
		for _, statusCode := range []int{http.StatusOK, http.StatusBadRequest} {
			t.Run(fmt.Sprintf("%s/%d", tt.code, statusCode), func(t *testing.T) {
				body := fmt.Appendf(nil, `{"result":"error","code":%q}`, tt.code)
				if err := mapProviderError(statusCode, body); !errors.Is(err, tt.expectedErr) {
					t.Errorf("mapProviderError() = %v, expected %v", err, tt.expectedErr)
				}
			})
		}
	}
}

func TestMapProviderErrorInvalidResponse(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"invalid JSON", `{"result":"error",`},
		{"empty body", ""},
		{"missing result", `{"code":"invalid_api_key"}`},
		{"missing code", `{"result":"error"}`},
		{"missing both fields", `{}`},
		{"empty code", `{"result":"error","code":""}`},
		{"non-error result", `{"result":"success","code":"invalid_api_key"}`},
		{"wrong field type", `{"result":"error","code":123}`},
		{"null payload", `null`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := mapProviderError(http.StatusBadRequest, []byte(tt.body)); !errors.Is(err, apperrors.ErrProviderInvalidResponse) {
				t.Errorf("mapProviderError() = %v, expected %v", err, apperrors.ErrProviderInvalidResponse)
			}
		})
	}
}

func TestMapProviderErrorHTTPStatusPrecedence(t *testing.T) {
	tests := []struct {
		statusCode  int
		expectedErr error
	}{
		{http.StatusUnauthorized, apperrors.ErrProviderUnauthorized},
		{http.StatusForbidden, apperrors.ErrProviderForbidden},
		{http.StatusTooManyRequests, apperrors.ErrProviderRateLimited},
		{http.StatusRequestTimeout, apperrors.ErrProviderTimeout},
		{http.StatusGatewayTimeout, apperrors.ErrProviderTimeout},
		{http.StatusInternalServerError, apperrors.ErrProviderUnavailable},
		{http.StatusServiceUnavailable, apperrors.ErrProviderUnavailable},
	}
	bodies := []struct {
		name string
		body string
	}{
		{"conflicting provider code", `{"result":"error","code":"invalid_pair"}`},
		{"unknown provider code", `{"result":"error","code":"unknown_code"}`},
		{"malformed JSON", `{"result":"error",`},
		{"missing fields", `{}`},
	}

	for _, tt := range tests {
		for _, body := range bodies {
			t.Run(fmt.Sprintf("%d/%s", tt.statusCode, body.name), func(t *testing.T) {
				if err := mapProviderError(tt.statusCode, []byte(body.body)); !errors.Is(err, tt.expectedErr) {
					t.Errorf("mapProviderError() = %v, expected %v", err, tt.expectedErr)
				}
			})
		}
	}
}

type testNetworkError struct {
	timeout bool
	message string
}

var _ net.Error = testNetworkError{}

func (e testNetworkError) Error() string   { return e.message }
func (e testNetworkError) Timeout() bool   { return e.timeout }
func (e testNetworkError) Temporary() bool { return false }

func TestMapTransportError(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		expectedErr error
	}{
		{"canceled", context.Canceled, context.Canceled},
		{"deadline exceeded", context.DeadlineExceeded, apperrors.ErrProviderTimeout},
		{"network timeout", testNetworkError{timeout: true, message: "network timeout"}, apperrors.ErrProviderTimeout},
		{"network failure", testNetworkError{message: "connection refused"}, apperrors.ErrProviderUnavailable},
		{"generic error", errors.New("network failure"), apperrors.ErrProviderUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := mapTransportError(tt.err); !errors.Is(err, tt.expectedErr) {
				t.Errorf("mapTransportError() = %v, expected %v", err, tt.expectedErr)
			}
		})
		t.Run(tt.name+"/wrapped", func(t *testing.T) {
			if err := mapTransportError(fmt.Errorf("request failed: %w", tt.err)); !errors.Is(err, tt.expectedErr) {
				t.Errorf("mapTransportError() = %v, expected %v", err, tt.expectedErr)
			}
		})
	}
}

func TestNormalizedErrorMessagesAreSafe(t *testing.T) {
	const apiKey = "synthetic-secret-api-key"
	const requestURL = "https://provider.example/rates?api_key=" + apiKey
	const providerMessage = "untrusted provider diagnostic"
	const sensitiveMessage = providerMessage + " " + requestURL

	providerBody := func(code string) []byte {
		return fmt.Appendf(nil, `{"result":"error","code":%q,"message":%q}`, code, sensitiveMessage)
	}
	transportError := func(err error) error {
		return &url.Error{Op: "Get", URL: requestURL, Err: err}
	}
	tests := []struct {
		name        string
		err         error
		expectedErr error
	}{
		{"known provider code", mapProviderError(http.StatusBadRequest, providerBody("invalid_api_key")), apperrors.ErrProviderUnauthorized},
		{"untrusted provider code", mapProviderError(http.StatusBadRequest, providerBody(sensitiveMessage)), apperrors.ErrProviderInvalidResponse},
		{"malformed body", mapProviderError(http.StatusBadRequest, []byte(sensitiveMessage)), apperrors.ErrProviderInvalidResponse},
		{"HTTP status", mapProviderError(http.StatusTooManyRequests, []byte(sensitiveMessage)), apperrors.ErrProviderRateLimited},
		{"transport canceled", mapTransportError(transportError(context.Canceled)), context.Canceled},
		{"transport deadline", mapTransportError(transportError(context.DeadlineExceeded)), apperrors.ErrProviderTimeout},
		{"transport timeout", mapTransportError(transportError(testNetworkError{timeout: true, message: sensitiveMessage})), apperrors.ErrProviderTimeout},
		{"transport failure", mapTransportError(transportError(errors.New(sensitiveMessage))), apperrors.ErrProviderUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !errors.Is(tt.err, tt.expectedErr) {
				t.Fatalf("normalized error = %v, expected %v", tt.err, tt.expectedErr)
			}
			for _, sensitive := range []string{apiKey, requestURL, providerMessage} {
				if strings.Contains(tt.err.Error(), sensitive) {
					t.Errorf("normalized error exposes sensitive value %q: %v", sensitive, tt.err)
				}
			}
		})
	}
}
