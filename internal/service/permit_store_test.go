package service

import (
	"context"
	"sync"
	"time"
)

// In-memory store exists only in tests, so production cannot bypass PostgreSQL.
type memoryPermitStore struct {
	mu                 sync.Mutex
	perMinute, perHour int
	attempts           []time.Time
	blockedUntil       time.Time
}

func newTestRateLimiter(perMinute, perHour int) *RateLimiter {
	return NewRateLimiter(&memoryPermitStore{perMinute: perMinute, perHour: perHour})
}
func (l *memoryPermitStore) AcquirePermit(ctx context.Context) (time.Duration, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	now := time.Now()
	first := 0
	for first < len(l.attempts) && !l.attempts[first].Add(time.Hour).After(now) {
		first++
	}
	l.attempts = l.attempts[:copy(l.attempts, l.attempts[first:])]
	next := l.blockedUntil
	if n := len(l.attempts); n >= l.perHour {
		if until := l.attempts[n-l.perHour].Add(time.Hour); until.After(next) {
			next = until
		}
	}
	if n := len(l.attempts); n >= l.perMinute {
		if until := l.attempts[n-l.perMinute].Add(time.Minute); until.After(next) {
			next = until
		}
	}
	if next.After(now) {
		return next.Sub(now), nil
	}
	l.attempts = append(l.attempts, now)
	return 0, nil
}
func (l *memoryPermitStore) PauseUntil(ctx context.Context, until time.Time) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if until.After(l.blockedUntil) {
		l.blockedUntil = until
	}
	return nil
}
