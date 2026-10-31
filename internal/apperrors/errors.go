package apperrors

import "errors"

var (
	ErrInvalidCurrencyPair = errors.New("invalid currency pair")
	ErrUnsupportedCurrency = errors.New("unsupported currency")
	ErrServiceOverloaded   = errors.New("service temporarily unavailable")
)
