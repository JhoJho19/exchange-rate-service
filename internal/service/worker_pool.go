package service

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/JhoJho19/exchange-rate-service/internal/apperrors"
	"github.com/JhoJho19/exchange-rate-service/internal/models"
	"github.com/google/uuid"
)

type RetryPolicy struct {
	MaxRetries int
	Delay      time.Duration
}

type WorkerPool struct {
	limiter        *RateLimiter
	retryPolicy    RetryPolicy
	jobs           chan models.UpdateTask
	queueSlots     chan struct{}
	workerCount    int
	waitGroup      sync.WaitGroup
	taskRepository TaskRepository
	rateProvider   RateProvider
	timeout        time.Duration
	mu             sync.Mutex
	started        bool
	stopping       bool
	producers      sync.WaitGroup
	lifecycle      context.Context
	cancel         context.CancelFunc
	done           chan struct{}
	active         map[uuid.UUID]struct{}
	scanSeen       map[uuid.UUID]struct{}
	recoveryCancel context.CancelFunc
	recoveryDone   chan struct{}
}

func NewWorkerPool(workerCount, queueSize int, taskRepository TaskRepository, rateProvider RateProvider, timeout time.Duration, limiter *RateLimiter, retryPolicy RetryPolicy) *WorkerPool {
	if workerCount <= 0 || queueSize <= 0 || limiter == nil || retryPolicy.MaxRetries < 0 || retryPolicy.Delay <= 0 {
		panic("worker pool requires positive worker/queue sizes, a limiter and valid retry policy")
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &WorkerPool{
		lifecycle:      ctx,
		cancel:         cancel,
		done:           make(chan struct{}),
		active:         make(map[uuid.UUID]struct{}),
		limiter:        limiter,
		retryPolicy:    retryPolicy,
		jobs:           make(chan models.UpdateTask, queueSize),
		queueSlots:     make(chan struct{}, queueSize),
		workerCount:    workerCount,
		waitGroup:      sync.WaitGroup{},
		taskRepository: taskRepository,
		rateProvider:   rateProvider,
		timeout:        timeout,
	}
}

// Start uses a pool lifetime context, independent of incoming requests. Cancel
// it only when draining must be interrupted, not when normal draining begins.
// Start may be called once. Submissions before Start are buffered. A stopped
// pool cannot be restarted; Stop before Start leaves persisted tasks recoverable.
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
		if ctx.Err() != nil {
			return
		}
		var task models.UpdateTask
		select {
		case <-ctx.Done():
			return
		case next, ok := <-workerPool.jobs:
			if !ok {
				return
			}
			task = next
		}
		workerPool.releaseQueueSlot()
		if ctx.Err() != nil {
			return
		}
		workerPool.processTask(ctx, task)
		workerPool.forget(task.ID)
	}
}

func (workerPool *WorkerPool) processTask(ctx context.Context, task models.UpdateTask) {
	claimed, err := workerPool.taskRepository.TryStartProcessing(
		ctx,
		task.ID,
	)

	if err != nil {
		log.Printf("Failed to claim task %s: %v", task.ID, err)
		return
	}

	if !claimed {
		log.Printf("Task %s cannot be claimed", task.ID)
		return
	}

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
			task.ID,
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
		task.ID,
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

// ErrQueueFull is kept for callers of Enqueue.
var ErrQueueFull = apperrors.ErrQueueFull

// reserveQueueSlot registers the ID before persistence, closing the race with
// recovery seeing a just-inserted PENDING row. Duplicate submission is a no-op.
func (workerPool *WorkerPool) reserveQueueSlot(id uuid.UUID, recovery bool) (bool, error) {
	workerPool.mu.Lock()
	defer workerPool.mu.Unlock()
	if workerPool.stopping || workerPool.lifecycle.Err() != nil {
		return false, ErrQueueFull
	}
	if _, exists := workerPool.active[id]; exists {
		return false, nil
	}
	if recovery {
		if _, seen := workerPool.scanSeen[id]; seen {
			return false, nil
		}
	}
	select {
	case workerPool.queueSlots <- struct{}{}:
		workerPool.producers.Add(1)
		workerPool.active[id] = struct{}{}
		if workerPool.scanSeen != nil {
			workerPool.scanSeen[id] = struct{}{}
		}
		return true, nil
	default:
		return false, ErrQueueFull
	}
}

func (workerPool *WorkerPool) forget(id uuid.UUID) {
	workerPool.mu.Lock()
	delete(workerPool.active, id)
	workerPool.mu.Unlock()
}

func (workerPool *WorkerPool) releaseQueueSlot() {
	<-workerPool.queueSlots
}

// enqueueReserved consumes an existing reservation. All sends to jobs must use
// reservations, so the buffer always has room without waiting for a worker.
func (workerPool *WorkerPool) enqueueReserved(task models.UpdateTask) {
	workerPool.jobs <- task
}

func (workerPool *WorkerPool) Enqueue(task models.UpdateTask) error {
	reserved, err := workerPool.reserveQueueSlot(task.ID, false)
	if err != nil || !reserved {
		return err
	}
	defer workerPool.producers.Done()
	workerPool.enqueueReserved(task)
	return nil
}

// beginShutdown serializes admission with producer registration. No Add can
// race with Wait after stopping has been set under mu.
func (workerPool *WorkerPool) beginShutdown() {
	workerPool.mu.Lock()
	defer workerPool.mu.Unlock()
	if workerPool.stopping {
		return
	}
	workerPool.stopping = true
	if workerPool.recoveryCancel != nil {
		workerPool.recoveryCancel()
	}
	go func() {
		workerPool.producers.Wait()
		if workerPool.recoveryDone != nil {
			<-workerPool.recoveryDone
		}
		close(workerPool.jobs)
		workerPool.waitGroup.Wait()
		workerPool.cancel()
		close(workerPool.done)
	}()
}

// Stop rejects admission immediately and drains. On deadline it cancels all
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
		workerPool.limiter.PauseUntil(until)
		if attempt >= workerPool.retryPolicy.MaxRetries {
			return 0, err
		}
		// Saturate exponential fallback rather than overflowing time.Duration.
		if delay <= time.Duration(1<<63-1)/2 {
			delay *= 2
		}
	}
}
