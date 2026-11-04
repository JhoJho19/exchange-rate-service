package postgres

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/JhoJho19/exchange-rate-service/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type boolRow struct{ acquired bool }

func (r boolRow) Scan(dest ...any) error { *dest[0].(*bool) = r.acquired; return nil }

type ownerConnectionFake struct {
	locked  *atomic.Bool
	holds   bool
	closed  atomic.Bool
	writes  atomic.Int32
	active  atomic.Int32
	overlap atomic.Bool
	exec    func(context.Context) error
}

func (c *ownerConnectionFake) QueryRow(_ context.Context, _ string, args ...any) pgx.Row {
	if args[0] != workerOwnershipLock {
		panic("unstable ownership key")
	}
	c.holds = c.locked.CompareAndSwap(false, true)
	return boolRow{c.holds}
}
func (c *ownerConnectionFake) Exec(ctx context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
	if c.active.Add(1) != 1 {
		c.overlap.Store(true)
	}
	defer c.active.Add(-1)
	if c.closed.Load() {
		return pgconn.CommandTag{}, errors.New("session lost")
	}
	if c.exec != nil {
		if err := c.exec(ctx); err != nil {
			return pgconn.CommandTag{}, err
		}
	}
	c.writes.Add(1)
	return pgconn.NewCommandTag("UPDATE 1"), nil
}
func (c *ownerConnectionFake) Query(context.Context, string, ...any) (pgx.Rows, error) {
	panic("unexpected Query")
}
func (c *ownerConnectionFake) Ping(context.Context) error {
	if c.closed.Load() {
		return errors.New("connection lost")
	}
	return nil
}
func (c *ownerConnectionFake) Close(context.Context) error {
	c.closed.Store(true)
	if c.holds {
		c.locked.Store(false)
	}
	return nil
}
func (c *ownerConnectionFake) IsClosed() bool { return c.closed.Load() }

func newOwnedTestRepo(t *testing.T, conn *ownerConnectionFake) *TaskRepository {
	t.Helper()
	o, err := acquireOwnership(context.Background(), conn)
	if err != nil {
		t.Fatal(err)
	}
	r := &TaskRepository{owner: o}
	t.Cleanup(r.CloseOwnership)
	return r
}

func TestOwnershipExcludesSecondInstanceAndReleasesAfterClose(t *testing.T) {
	var locked atomic.Bool
	first := newOwnedTestRepo(t, &ownerConnectionFake{locked: &locked})
	second := &ownerConnectionFake{locked: &locked}
	if _, err := acquireOwnership(context.Background(), second); !errors.Is(err, ErrOwnershipUnavailable) {
		t.Fatalf("second owner: %v", err)
	}
	first.CloseOwnership()
	first.CloseOwnership()
	replacement := newOwnedTestRepo(t, &ownerConnectionFake{locked: &locked})
	if err := replacement.ResetProcessing(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestRecoveryResetRequiresOwnershipAndCannotResetActiveSession(t *testing.T) {
	if err := (&TaskRepository{}).ResetProcessing(context.Background()); !errors.Is(err, ErrOwnershipUnavailable) {
		t.Fatal(err)
	}
	var locked atomic.Bool
	conn := &ownerConnectionFake{locked: &locked}
	repo := newOwnedTestRepo(t, conn)
	if _, err := repo.TryStartProcessing(context.Background(), uuid.New()); err == nil {
		t.Fatal("claim before reset succeeded")
	}
	if err := repo.ResetProcessing(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.TryStartProcessing(context.Background(), uuid.New()); err != nil {
		t.Fatal(err)
	}
	if err := repo.ResetProcessing(context.Background()); err != nil {
		t.Fatal(err)
	}
	if conn.writes.Load() != 2 {
		t.Fatal("repeated reset touched active work")
	}
}

func TestRecoveryResetFailureCanBeRetried(t *testing.T) {
	var locked atomic.Bool
	resetErr := errors.New("temporary reset failure")
	conn := &ownerConnectionFake{locked: &locked, exec: func(context.Context) error { return resetErr }}
	repo := newOwnedTestRepo(t, conn)
	if err := repo.ResetProcessing(context.Background()); !errors.Is(err, resetErr) {
		t.Fatalf("ResetProcessing error = %v, want %v", err, resetErr)
	}
	// A failed reset must not enable workers or mark recovery as prepared.
	if claimed, err := repo.TryStartProcessing(context.Background(), uuid.New()); err == nil || claimed {
		t.Fatalf("claim after failed reset = %t, %v", claimed, err)
	}
	if conn.writes.Load() != 0 {
		t.Fatal("failed reset allowed a write")
	}
	conn.exec = nil
	if err := repo.ResetProcessing(context.Background()); err != nil {
		t.Fatalf("retry reset: %v", err)
	}
	if claimed, err := repo.TryStartProcessing(context.Background(), uuid.New()); err != nil || !claimed {
		t.Fatalf("claim after successful reset = %t, %v", claimed, err)
	}
	if conn.writes.Load() != 2 {
		t.Fatalf("successful writes = %d, want reset and claim", conn.writes.Load())
	}
}

func TestOwnershipLossFencesAllTaskMutations(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var locked atomic.Bool
		conn := &ownerConnectionFake{locked: &locked}
		// Cleanup must run inside the synctest bubble owning the monitor goroutine.
		owner, err := acquireOwnership(context.Background(), conn)
		if err != nil {
			t.Fatal(err)
		}
		repo := &TaskRepository{owner: owner}
		defer repo.CloseOwnership()
		if err := repo.ResetProcessing(context.Background()); err != nil {
			t.Fatal(err)
		}
		conn.closed.Store(true)
		<-repo.OwnershipDone() // virtual heartbeat detects the loss
		id := uuid.New()
		mutations := []func() error{
			func() error {
				return repo.Create(context.Background(), models.UpdateTask{ID: id, Status: models.StatusPending})
			},
			func() error { _, e := repo.TryStartProcessing(context.Background(), id); return e },
			func() error {
				_, e := repo.CompleteTask(context.Background(), id, models.Quote{}, time.Now())
				return e
			},
			func() error { _, e := repo.FailTask(context.Background(), id, "failure", time.Now()); return e },
		}
		for _, mutation := range mutations {
			if err := mutation(); err == nil {
				t.Fatal("stale mutation succeeded")
			}
		}
		if conn.writes.Load() != 1 {
			t.Fatal("stale owner wrote after loss")
		}
	})
}

func TestOwnedSessionSerializesWritesAndCancelsWaiters(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var locked atomic.Bool
		conn := &ownerConnectionFake{locked: &locked}
		o, err := acquireOwnership(context.Background(), conn)
		if err != nil {
			t.Fatal(err)
		}
		repo := &TaskRepository{owner: o}
		defer repo.CloseOwnership()
		_ = repo.ResetProcessing(context.Background())
		entered := make(chan struct{})
		conn.exec = func(ctx context.Context) error { close(entered); <-ctx.Done(); return ctx.Err() }
		done := make(chan error, 1)
		go func() { _, err := repo.TryStartProcessing(context.Background(), uuid.New()); done <- err }()
		<-entered
		ctx, cancel := context.WithCancel(context.Background())
		waiting := make(chan error, 1)
		go func() { _, err := repo.TryStartProcessing(ctx, uuid.New()); waiting <- err }()
		synctest.Wait()
		cancel()
		if err := <-waiting; !errors.Is(err, context.Canceled) {
			t.Fatalf("waiter: %v", err)
		}
		o.cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("active operation: %v", err)
		}
		repo.CloseOwnership()
		if conn.active.Load() != 0 || conn.overlap.Load() {
			t.Fatal("unsafe connection close or concurrent use")
		}
	})
}

func TestOwnedSessionSerializesConcurrentFinalizations(t *testing.T) {
	var locked atomic.Bool
	conn := &ownerConnectionFake{locked: &locked}
	repo := newOwnedTestRepo(t, conn)
	_ = repo.ResetProcessing(context.Background())
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := repo.CompleteTask(context.Background(), uuid.New(), models.Quote{}, time.Now())
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if conn.overlap.Load() {
		t.Fatal("concurrent access to pgx session")
	}
}
