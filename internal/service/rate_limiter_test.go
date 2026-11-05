package service

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

func TestRateLimiterConcurrentRollingWindows(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		limiter := newTestRateLimiter(2, 3)
		times := make(chan time.Time, 6)
		var wg sync.WaitGroup
		start := time.Now()
		for i := 0; i < 6; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := limiter.Wait(context.Background()); err != nil {
					t.Error(err)
					return
				}
				times <- time.Now()
			}()
		}
		wg.Wait()
		close(times)
		var got []time.Duration
		for at := range times {
			got = append(got, at.Sub(start))
		}
		sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })
		want := []time.Duration{0, 0, time.Minute, time.Hour, time.Hour, time.Hour + time.Minute}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("admissions = %v, want %v", got, want)
			}
		}
	})
}

func TestRateLimiterCooldownExtension(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		limiter := newTestRateLimiter(10, 100)
		start := time.Now()
		limiter.PauseUntil(context.Background(), start.Add(time.Minute))
		done := make(chan error, 1)
		go func() { done <- limiter.Wait(context.Background()) }()
		synctest.Wait()
		time.Sleep(30 * time.Second)
		limiter.PauseUntil(context.Background(), start.Add(2*time.Minute))
		limiter.PauseUntil(context.Background(), start.Add(time.Minute)) // Cannot shorten another worker's pause.
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if elapsed := time.Since(start); elapsed != 2*time.Minute {
			t.Fatalf("waited %v", elapsed)
		}
	})
}

func TestRateLimiterCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		limiter := newTestRateLimiter(1, 1)
		if err := limiter.Wait(context.Background()); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- limiter.Wait(ctx) }()
		synctest.Wait()
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v", err)
		}
		if len(limiter.store.(*memoryPermitStore).attempts) != 1 {
			t.Fatal("cancellation consumed quota")
		}
	})
}
