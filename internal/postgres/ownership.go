package postgres

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Stable, database-local session lock for the single active worker pool.
const workerOwnershipLock int64 = 0x455852415445

var ErrOwnershipUnavailable = errors.New("exclusive worker ownership unavailable")

type ownershipConnection interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
	Ping(context.Context) error
	Close(context.Context) error
	IsClosed() bool
}

// All mutations use this exact session, never a pooled fallback. Losing the
// session therefore fences old writes at PostgreSQL itself, even before the
// heartbeat notices. The connection is never reconnected or reacquired.
type ownership struct {
	conn        ownershipConnection
	gate        chan struct{}
	ctx         context.Context
	cancel      context.CancelFunc
	monitorDone chan struct{}
	closeOnce   sync.Once
	prepared    bool // protected by gate
}

func acquireOwnership(ctx context.Context, conn ownershipConnection) (*ownership, error) {
	var acquired bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", workerOwnershipLock).Scan(&acquired); err != nil {
		return nil, fmt.Errorf("acquire worker ownership: %w", err)
	}
	if !acquired {
		return nil, ErrOwnershipUnavailable
	}
	life, cancel := context.WithCancel(context.Background())
	o := &ownership{conn: conn, gate: make(chan struct{}, 1), ctx: life, cancel: cancel, monitorDone: make(chan struct{})}
	go o.monitor()
	return o, nil
}

// withConnection provides cancellation while waiting for the serialized session
// and bounds every DB operation. Callbacks must consume/close rows before return.
func (o *ownership) withConnection(ctx context.Context, fn func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	stop := context.AfterFunc(o.ctx, cancel)
	defer stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case o.gate <- struct{}{}:
	}
	defer func() { <-o.gate }()
	if o.ctx.Err() != nil {
		return ErrOwnershipUnavailable
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	err := fn(ctx)
	if o.conn.IsClosed() {
		o.cancel()
	}
	return err
}

func (o *ownership) monitor() {
	defer close(o.monitorDone)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-o.ctx.Done():
			return
		case <-ticker.C:
			if err := o.withConnection(o.ctx, o.conn.Ping); err != nil {
				o.cancel() // Fail closed, including an unresponsive ownership session.
				return
			}
		}
	}
}

func (o *ownership) close() {
	o.closeOnce.Do(func() {
		o.cancel()
		<-o.monitorDone
		o.gate <- struct{}{}
		defer func() { <-o.gate }()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		// Closing, rather than returning this session to a pool, releases the lock.
		_ = o.conn.Close(ctx)
	})
}

// AcquireOwnership must precede recovery and worker startup. A separate physical
// connection avoids consuming the pool's last slot. A repository is single-use.
func (r *TaskRepository) AcquireOwnership(ctx context.Context) error {
	r.ownerMu.Lock()
	defer r.ownerMu.Unlock()
	if r.owner != nil {
		return errors.New("repository ownership already initialized")
	}
	conn, err := pgx.ConnectConfig(ctx, r.pool.Config().ConnConfig.Copy())
	if err != nil {
		return fmt.Errorf("connect ownership session: %w", err)
	}
	owner, err := acquireOwnership(ctx, conn)
	if err != nil {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = conn.Close(closeCtx)
		return err
	}
	r.owner = owner
	return nil
}

func (r *TaskRepository) ownership() (*ownership, error) {
	r.ownerMu.RLock()
	defer r.ownerMu.RUnlock()
	if r.owner == nil {
		return nil, ErrOwnershipUnavailable
	}
	return r.owner, nil
}

// OwnershipDone closes on loss or explicit release. Call after acquisition.
func (r *TaskRepository) OwnershipDone() <-chan struct{} {
	o, err := r.ownership()
	if err != nil {
		ch := make(chan struct{})
		close(ch)
		return ch
	}
	return o.ctx.Done()
}

// CloseOwnership is called only after all producers and workers have joined.
func (r *TaskRepository) CloseOwnership() {
	if o, err := r.ownership(); err == nil {
		o.close()
	}
}

func (r *TaskRepository) exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	var tag pgconn.CommandTag
	o, err := r.ownership()
	if err != nil {
		return tag, err
	}
	err = o.withConnection(ctx, func(ctx context.Context) error {
		if !o.prepared {
			return errors.New("recovery must be prepared before task writes")
		}
		var err error
		tag, err = o.conn.Exec(ctx, sql, args...)
		return err
	})
	return tag, err
}
