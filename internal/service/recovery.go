package service

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/JhoJho19/exchange-rate-service/internal/models"
	"github.com/google/uuid"
)

// RecoveryRepository is separated from ordinary task access so test doubles and
// callers that do not run recovery need not implement lifecycle operations.
// ResetProcessing requires exclusive database ownership and runs before workers.
type RecoveryRepository interface {
	ResetProcessing(context.Context) error
	PendingTasks(context.Context, int, []uuid.UUID) ([]models.UpdateTask, error)
}

// StartRecovery starts one bounded producer after ownership/reset and Start.
// It polls throughout the lifetime, including after transient repository errors.
func (p *WorkerPool) StartRecovery(repo RecoveryRepository) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.started || p.stopping || p.recoveryDone != nil {
		return errors.New("recovery requires a running pool and may only start once")
	}
	ctx, cancel := context.WithCancel(p.lifecycle)
	p.recoveryCancel = cancel
	p.recoveryDone = make(chan struct{})
	go func() {
		defer close(p.recoveryDone)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			if ctx.Err() != nil {
				return
			}
			p.recoverBatch(ctx, repo)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return nil
}

func (p *WorkerPool) recoverBatch(ctx context.Context, repo RecoveryRepository) {
	p.mu.Lock()
	limit := cap(p.queueSlots) - len(p.queueSlots)
	excluded := make([]uuid.UUID, 0, len(p.active))
	for id := range p.active {
		excluded = append(excluded, id)
	}
	// Remember admissions during this query even if workers finish them before
	// its results arrive. This prevents stale query results being enqueued twice.
	p.scanSeen = make(map[uuid.UUID]struct{})
	p.mu.Unlock()
	defer func() { p.mu.Lock(); p.scanSeen = nil; p.mu.Unlock() }()
	if limit == 0 {
		return
	}
	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tasks, err := repo.PendingTasks(queryCtx, limit, excluded)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("Task recovery query failed: %v", err)
		}
		return
	}
	for _, task := range tasks {
		if ctx.Err() != nil {
			return
		}
		reserved, err := p.reserveQueueSlot(task.ID, true)
		if err != nil {
			return
		}
		if !reserved {
			continue
		}
		p.enqueueReserved(task)
		p.producers.Done()
	}
}
