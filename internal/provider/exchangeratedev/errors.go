package exchangeratedev

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"

	"github.com/JhoJho19/exchange-rate-service/internal/apperrors"
)

func mapHTTPStatus(statusCode int) error {
	switch statusCode {
	case http.StatusUnauthorized:
		return apperrors.ErrProviderUnauthorized

	case http.StatusForbidden:
		return apperrors.ErrProviderForbidden

	case http.StatusTooManyRequests:
		return apperrors.ErrProviderRateLimited

	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return apperrors.ErrProviderTimeout

	default:
		if statusCode >= 500 {
			return apperrors.ErrProviderUnavailable
		}
	}

	return nil
}

func mapProviderError(statusCode int, body []byte) error {
	if mapped := mapHTTPStatus(statusCode); mapped != nil {
		return mapped
	}

	var payload struct {
		Result string `json:"result"`
		Code   string `json:"code"`
	}

	if err := json.Unmarshal(body, &payload); err != nil || payload.Result != "error" {
		return apperrors.ErrProviderInvalidResponse
	}

	switch payload.Code {
	case "invalid_pair", "unsupported_base":
		return apperrors.ErrProviderUnsupportedPair

	case "invalid_api_key":
		return apperrors.ErrProviderUnauthorized

	case "forbidden", "plan_gated":
		return apperrors.ErrProviderForbidden

	case "rate_limited", "ip_rate_limited", "quota_exceeded":
		return apperrors.ErrProviderRateLimited

	case "data_unavailable", "no_data_for_date",
		"live_unavailable", "source_unavailable":
		return apperrors.ErrProviderQuoteUnavailable

	case "auth_unavailable", "quota_unavailable",
		"service_unavailable":
		return apperrors.ErrProviderUnavailable

	default:
		return apperrors.ErrProviderInvalidResponse
	}
}

func mapTransportError(err error) error {
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return apperrors.ErrProviderTimeout
	}

	var netErr net.Error

	if errors.As(err, &netErr) && netErr.Timeout() {
		return apperrors.ErrProviderTimeout
	}

	return apperrors.ErrProviderUnavailable
}
