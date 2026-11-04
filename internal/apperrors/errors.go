package apperrors

import "errors"

var (
	// Application errors
	ErrIdempotencyConflict = errors.New("idempotency key already used for a different currency pair")
	ErrInvalidCurrencyPair = errors.New("invalid currency pair")
	ErrUnsupportedCurrency = errors.New("unsupported currency")
	ErrServiceOverloaded   = errors.New("service temporarily unavailable")
	// ErrQueueFull shares the existing overload error for errors.Is callers.
	ErrQueueFull = ErrServiceOverloaded

	// Provider errors
	ErrProviderUnauthorized     = errors.New("provider unauthorized")
	ErrProviderForbidden        = errors.New("provider access forbidden")
	ErrProviderRateLimited      = errors.New("provider rate limit exceeded")
	ErrProviderTimeout          = errors.New("provider request timeout")
	ErrProviderUnavailable      = errors.New("provider unavailable")
	ErrProviderInvalidResponse  = errors.New("invalid provider response")
	ErrProviderQuoteUnavailable = errors.New("provider quote unavailable")
	ErrProviderUnsupportedPair  = errors.New("provider unsupported currency pair")
	ErrTaskNotFound             = errors.New("update task not found")
	ErrQuoteNotFound            = errors.New("quote not found")
)
