package service

import (
	"context"
	"sync"
	"time"
)

// RateLimiter shares rolling minute/hour quotas and provider cooldown across
// all workers in this process. Every admitted attempt consumes both quotas.
type RateLimiter struct {
	mu           sync.Mutex
	perMinute    int
	perHour      int
	attempts     []time.Time
	blockedUntil time.Time
}

func NewRateLimiter(perMinute, perHour int) *RateLimiter {
	if perMinute <= 0 || perHour <= 0 {
		panic("rate limits must be positive")
	}
	return &RateLimiter{perMinute: perMinute, perHour: perHour}
}

func (l *RateLimiter) Wait(ctx context.Context) error {
	for {
		l.mu.Lock()
		if err := ctx.Err(); err != nil {
			l.mu.Unlock()
			return err
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
		if !next.After(now) {
			l.attempts = append(l.attempts, now)
			l.mu.Unlock()
			return nil
		}
		l.mu.Unlock()

		timer := time.NewTimer(next.Sub(now))
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
			// Recheck both quotas and any cooldown extension before admission.
		}
	}
}

// PauseUntil only extends a cooldown; an older response cannot shorten it.
func (l *RateLimiter) PauseUntil(until time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if until.After(l.blockedUntil) {
		l.blockedUntil = until
	}
}
