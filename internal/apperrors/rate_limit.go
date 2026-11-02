package apperrors

import "time"

// ProviderRateLimitError preserves Retry-After without exposing provider data.
// A zero RetryAt means the caller should use its configured fallback delay.
type ProviderRateLimitError struct {
	RetryAt time.Time
}

func (*ProviderRateLimitError) Error() string { return ErrProviderRateLimited.Error() }
func (*ProviderRateLimitError) Unwrap() error { return ErrProviderRateLimited }
