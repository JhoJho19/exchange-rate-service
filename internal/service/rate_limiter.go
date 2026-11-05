package service

import (
	"context"
	"time"
)

// PermitStore makes one atomic admission decision. Zero delay means a permit
// was committed; a positive delay is advisory and requires another check.
// Implementations must release transactions/connections before returning.
type PermitStore interface {
	AcquirePermit(context.Context) (time.Duration, error)
	PauseUntil(context.Context, time.Time) error
}

// RateLimiter waits outside the store and rechecks shared state on every wakeup.
// There is no local admission fallback when the store is unavailable.
type RateLimiter struct{ store PermitStore }

func NewRateLimiter(store PermitStore) *RateLimiter {
	if store == nil {
		panic("rate limiter requires a permit store")
	}
	return &RateLimiter{store: store}
}

func (l *RateLimiter) Wait(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		delay, err := l.store.AcquirePermit(ctx)
		if err != nil {
			return err
		}
		if delay <= 0 {
			return nil
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (l *RateLimiter) PauseUntil(ctx context.Context, until time.Time) error {
	return l.store.PauseUntil(ctx, until)
}
