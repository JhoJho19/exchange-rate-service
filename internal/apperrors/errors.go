package apperrors

import "errors"

var (
	// Application errors
	ErrInvalidCurrencyPair = errors.New("invalid currency pair")
	ErrUnsupportedCurrency = errors.New("unsupported currency")
	ErrServiceOverloaded   = errors.New("service temporarily unavailable")

	// Provider errors
	ErrProviderUnauthorized     = errors.New("provider unauthorized")
	ErrProviderForbidden        = errors.New("provider access forbidden")
	ErrProviderRateLimited      = errors.New("provider rate limit exceeded")
	ErrProviderTimeout          = errors.New("provider request timeout")
	ErrProviderUnavailable      = errors.New("provider unavailable")
	ErrProviderInvalidResponse  = errors.New("invalid provider response")
	ErrProviderQuoteUnavailable = errors.New("provider quote unavailable")
	ErrProviderUnsupportedPair  = errors.New("provider unsupported currency pair")
)
