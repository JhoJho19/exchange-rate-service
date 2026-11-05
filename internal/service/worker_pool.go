package service

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/JhoJho19/exchange-rate-service/internal/apperrors"
	"github.com/JhoJho19/exchange-rate-service/internal/models"
)

type RetryPolicy struct {
	MaxRetries int
	Delay      time.Duration
}

type WorkerPool struct {
	limiter        *RateLimiter
	retryPolicy    RetryPolicy
	workerCount    int
	waitGroup      sync.WaitGroup
	taskRepository TaskRepository
	rateProvider   RateProvider
	timeout        time.Duration
	mu             sync.Mutex
	started        bool
	stopping       bool
	lifecycle      context.Context
	cancel         context.CancelFunc
	claims         context.Context
	cancelClaims   context.CancelFunc
	done           chan struct{}
	notify         chan struct{}
	leaseDuration  time.Duration
	pollInterval   time.Duration
}

func NewWorkerPool(workerCount int, taskRepository TaskRepository, rateProvider RateProvider, timeout time.Duration, limiter *RateLimiter, retryPolicy RetryPolicy, options ...WorkerPoolOption) *WorkerPool {
	if workerCount <= 0 || limiter == nil || retryPolicy.MaxRetries < 0 || retryPolicy.Delay <= 0 {
		panic("worker pool requires positive worker count, a limiter and valid retry policy")
	}
	ctx, cancel := context.WithCancel(context.Background())
	claims, cancelClaims := context.WithCancel(ctx)
	pool := &WorkerPool{
		lifecycle: ctx, cancel: cancel, claims: claims, cancelClaims: cancelClaims,
		done: make(chan struct{}), notify: make(chan struct{}, workerCount),
		leaseDuration: 30 * time.Second, pollInterval: 100 * time.Millisecond, limiter: limiter, retryPolicy: retryPolicy,
		workerCount: workerCount, taskRepository: taskRepository, rateProvider: rateProvider, timeout: timeout,
	}
	for _, option := range options {
		option(pool)
	}
	return pool
}

type WorkerPoolOption func(*WorkerPool)

// WithLeaseTiming configures polling and lease renewal (every lease/3).
func WithLeaseTiming(lease, poll time.Duration) WorkerPoolOption {
	if lease < time.Second || poll < time.Millisecond {
		panic("lease must be >= 1s and poll interval >= 1ms")
	}
	return func(p *WorkerPool) { p.leaseDuration = lease; p.pollInterval = poll }
}

// Notify is a best-effort wakeup; periodic database polling guarantees delivery.
func (p *WorkerPool) Notify() {
	select {
	case p.notify <- struct{}{}:
	default:
	}
}

// Start uses a pool lifetime context, independent of incoming requests. Cancel
// it only when draining must be interrupted, not when normal draining begins.
// Start may be called once. Persisted tasks are discovered without notifications.
func (workerPool *WorkerPool) Start(ctx context.Context) error {
	workerPool.mu.Lock()
	defer workerPool.mu.Unlock()
	if workerPool.started || workerPool.stopping {
		return errors.New("worker pool already started or stopped")
	}
	workerPool.started = true
	stopParent := context.AfterFunc(ctx, workerPool.cancel)
	if ctx.Err() != nil {
		workerPool.cancel()
	}
	for i := 0; i < workerPool.workerCount; i++ {
		workerPool.waitGroup.Add(1)
		go workerPool.worker(workerPool.lifecycle)
	}
	go func() {
		defer stopParent()
		select {
		case <-workerPool.lifecycle.Done():
			workerPool.beginShutdown()
		case <-workerPool.done:
		}
	}()
	return nil
}

func (workerPool *WorkerPool) worker(ctx context.Context) {
	defer workerPool.waitGroup.Done()
	for {
		if workerPool.claims.Err() != nil {
			return
		}
		claimCtx, cancel := context.WithTimeout(workerPool.claims, 5*time.Second)
		task, claimed, err := workerPool.taskRepository.ClaimNextTask(claimCtx, workerPool.leaseDuration)
		cancel()
		// A claim racing shutdown remains leased in PostgreSQL for another worker.
		if workerPool.claims.Err() != nil {
			return
		}
		if err != nil {
			log.Printf("Failed to claim next task: %v", err)
		}
		if err == nil && claimed {
			workerPool.processLeasedTask(ctx, task)
			continue
		}
		timer := time.NewTimer(workerPool.pollInterval)
		select {
		case <-workerPool.claims.Done():
			timer.Stop()
			return
		case <-workerPool.notify:
			timer.Stop()
		case <-timer.C:
		}
	}
}

func (workerPool *WorkerPool) processLeasedTask(parent context.Context, task models.UpdateTask) {
	ctx, cancel := context.WithCancel(parent)
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(workerPool.leaseDuration / 3)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				renewCtx, stop := context.WithTimeout(ctx, workerPool.leaseDuration/3)
				ok, err := workerPool.taskRepository.RenewLease(renewCtx, task.ID, task.LeaseToken, workerPool.leaseDuration)
				stop()
				if err != nil || !ok {
					log.Printf("Lost lease for task %s: %v", task.ID, err)
					cancel()
					return
				}
			}
		}
	}()
	defer func() { cancel(); <-heartbeatDone }()

	log.Printf("Started processing task %s", task.ID)

	rate, err := workerPool.getRate(ctx, task.Pair)
	// Pool cancellation leaves unfinished tasks available for future recovery.
	if ctx.Err() != nil {
		return
	}

	if err != nil {
		log.Printf("Failed to get rate for task %s: %v", task.ID, err)

		completedAt := time.Now().UTC()

		const safeErrorMessage = "Failed to fetch exchange rate from external provider"

		saved, saveErr := workerPool.taskRepository.FailTask(
			ctx,
			task.ID, task.LeaseToken,
			safeErrorMessage,
			completedAt,
		)

		if saveErr != nil {
			log.Printf("Failed to mark task %s as failed: %v", task.ID, saveErr)
			return
		}

		if !saved {
			log.Printf("Task %s cannot be marked as failed", task.ID)
			return
		}

		log.Printf("Task %s marked as failed", task.ID)
		return
	}

	quote := models.Quote{
		Pair:      task.Pair,
		Rate:      rate,
		UpdatedAt: time.Now().UTC(),
	}

	log.Printf("Received rate for task %s: %f", task.ID, rate)

	completedAt := time.Now().UTC()

	saved, err := workerPool.taskRepository.CompleteTask(
		ctx,
		task.ID, task.LeaseToken,
		quote,
		completedAt,
	)

	if err != nil {
		log.Printf("Failed to complete task %s: %v", task.ID, err)
		return
	}

	if !saved {
		log.Printf("Task %s cannot be completed", task.ID)
		return
	}

	log.Printf("Task %s completed successfully", task.ID)
}

var ErrQueueFull = apperrors.ErrQueueFull

func (workerPool *WorkerPool) beginShutdown() {
	workerPool.mu.Lock()
	defer workerPool.mu.Unlock()
	if workerPool.stopping {
		return
	}
	workerPool.stopping = true
	workerPool.cancelClaims()
	go func() {
		workerPool.waitGroup.Wait()
		workerPool.cancel()
		close(workerPool.done)
	}()
}

// Stop stops claiming and waits for current tasks. On deadline it cancels all
// operations, then joins them before returning, so database resources can be
// closed safely. Repository/provider implementations must honor context.
// Concurrent callers are safe; any caller's deadline may force cancellation.
func (workerPool *WorkerPool) Stop(ctx context.Context) error {
	workerPool.beginShutdown()
	select {
	case <-workerPool.done:
		return nil
	default:
	}
	select {
	case <-workerPool.done:
		return nil
	case <-ctx.Done():
		workerPool.cancel()
		<-workerPool.done
		return ctx.Err()
	}
}

func (workerPool *WorkerPool) getRate(ctx context.Context, pair models.CurrencyPair) (float64, error) {
	delay := workerPool.retryPolicy.Delay
	for attempt := 0; ; attempt++ {
		if err := workerPool.limiter.Wait(ctx); err != nil {
			return 0, err
		}
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		requestCtx, cancel := context.WithTimeout(ctx, workerPool.timeout)
		rate, err := workerPool.rateProvider.GetRate(requestCtx, pair)
		cancel()
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		if !errors.Is(err, apperrors.ErrProviderRateLimited) {
			return rate, err
		}

		until := time.Now().Add(delay)
		var limited *apperrors.ProviderRateLimitError
		if errors.As(err, &limited) && !limited.RetryAt.IsZero() {
			until = limited.RetryAt
		}
		// Protect other tasks even when this task has no retries left.
		if err := workerPool.limiter.PauseUntil(ctx, until); err != nil {
			return 0, err
		}
		if attempt >= workerPool.retryPolicy.MaxRetries {
			return 0, err
		}
		// Saturate exponential fallback rather than overflowing time.Duration.
		if delay <= time.Duration(1<<63-1)/2 {
			delay *= 2
		}
	}
}
