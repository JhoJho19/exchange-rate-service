package service

import (
	"context"
	"errors"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/JhoJho19/exchange-rate-service/internal/models"
	"github.com/google/uuid"
)

type recoveryRepository struct {
	fakeTaskRepository
	mu          sync.Mutex
	tasks       map[uuid.UUID]models.UpdateTask
	queryErrors int
	claimErrors int
	queries     int
	maxLimit    int
	completed   chan uuid.UUID
	query       func(context.Context, int, []uuid.UUID) ([]models.UpdateTask, error)
}

func (r *recoveryRepository) Create(_ context.Context, task models.UpdateTask) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tasks[task.ID] = task
	return nil
}
func (r *recoveryRepository) ResetProcessing(context.Context) error { return nil }
func (r *recoveryRepository) PendingTasks(ctx context.Context, limit int, excluded []uuid.UUID) ([]models.UpdateTask, error) {
	if r.query != nil {
		return r.query(ctx, limit, excluded)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.queries++
	if limit > r.maxLimit {
		r.maxLimit = limit
	}
	if r.queryErrors > 0 {
		r.queryErrors--
		return nil, errors.New("temporary query failure")
	}
	skip := make(map[uuid.UUID]bool)
	for _, id := range excluded {
		skip[id] = true
	}
	var result []models.UpdateTask
	for _, task := range r.tasks {
		if task.Status == models.StatusPending && !skip[task.ID] {
			result = append(result, task)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].CreatedAt.Equal(result[j].CreatedAt) {
			return result[i].ID.String() < result[j].ID.String()
		}
		return result[i].CreatedAt.Before(result[j].CreatedAt)
	})
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}
func (r *recoveryRepository) TryStartProcessing(_ context.Context, id uuid.UUID) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.claimErrors > 0 {
		r.claimErrors--
		return false, errors.New("temporary claim failure")
	}
	task, ok := r.tasks[id]
	if !ok || task.Status != models.StatusPending {
		return false, nil
	}
	task.Status = models.StatusProcessing
	r.tasks[id] = task
	return true, nil
}
func (r *recoveryRepository) CompleteTask(_ context.Context, id uuid.UUID, quote models.Quote, at time.Time) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	task := r.tasks[id]
	if task.Status != models.StatusProcessing {
		return false, nil
	}
	task.Status = models.StatusCompleted
	task.Quote = &quote
	task.CompletedAt = &at
	r.tasks[id] = task
	if r.completed != nil {
		r.completed <- id
	}
	return true, nil
}

func TestRecoveryBacklogPreservesIdentityAndRetriesErrors(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		repo := &recoveryRepository{tasks: make(map[uuid.UUID]models.UpdateTask), completed: make(chan uuid.UUID, 30), queryErrors: 2, claimErrors: 1}
		originals := make(map[uuid.UUID]models.UpdateTask)
		for i := range 25 {
			task := models.UpdateTask{ID: uuid.New(), Pair: models.CurrencyPair{BaseCurrency: "EUR", QuoteCurrency: "MXN"}, Status: models.StatusPending, CreatedAt: time.Now().Add(time.Duration(i) * time.Second)}
			if i == 23 {
				task.Status = models.StatusCompleted
			}
			if i == 24 {
				task.Status = models.StatusFailed
			}
			repo.tasks[task.ID] = task
			originals[task.ID] = task
		}
		var calls atomic.Int32
		p := lifecyclePool(repo, queueRateProvider(func(context.Context, models.CurrencyPair) (float64, error) { calls.Add(1); return 20, nil }), 2, 3)
		if err := p.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := p.StartRecovery(repo); err != nil {
			t.Fatal(err)
		}
		if err := p.StartRecovery(repo); err == nil {
			t.Fatal("duplicate recovery started")
		}
		for range 23 {
			<-repo.completed
		}
		// Wait through another poll to verify idempotency, using virtual time.
		time.Sleep(200 * time.Millisecond)
		if err := p.Stop(context.Background()); err != nil {
			t.Fatal(err)
		}
		if calls.Load() != 23 || repo.maxLimit > 3 || repo.queries < 9 || len(repo.tasks) != 25 {
			t.Fatalf("calls=%d maxLimit=%d queries=%d tasks=%d", calls.Load(), repo.maxLimit, repo.queries, len(repo.tasks))
		}
		for id, before := range originals {
			after := repo.tasks[id]
			if after.ID != before.ID || after.Pair != before.Pair || !after.CreatedAt.Equal(before.CreatedAt) {
				t.Fatal("recovery changed identity")
			}
			if before.Status == models.StatusPending {
				if after.Status != models.StatusCompleted {
					t.Fatal("unfinished task")
				}
			} else if after != before {
				t.Fatal("terminal task changed")
			}
		}
		select {
		case <-p.recoveryDone:
		default:
			t.Fatal("recovery not joined")
		}
	})
}

func TestRecoveryDoesNotDuplicateConcurrentCreation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		queryStarted, releaseQuery := make(chan struct{}), make(chan struct{})
		repo := &recoveryRepository{tasks: make(map[uuid.UUID]models.UpdateTask), completed: make(chan uuid.UUID, 1)}
		var snapshot models.UpdateTask
		repo.query = func(ctx context.Context, _ int, _ []uuid.UUID) ([]models.UpdateTask, error) {
			close(queryStarted)
			<-releaseQuery
			return []models.UpdateTask{snapshot}, nil
		}
		var claims atomic.Int32
		// Count claims as well as provider calls: a stale row must not even enqueue.
		wrapped := &claimCountingRepository{recoveryRepository: repo, claims: &claims}
		p := lifecyclePool(wrapped, queueRateProvider(func(context.Context, models.CurrencyPair) (float64, error) { return 20, nil }), 1, 2)
		if err := p.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		recovered := make(chan struct{})
		go func() { p.recoverBatch(context.Background(), repo); close(recovered) }()
		<-queryStarted
		id, err := queueService(repo, p).CreateUpdate(context.Background(), "EUR", "MXN")
		if err != nil {
			t.Fatal(err)
		}
		<-repo.completed
		synctest.Wait() // Worker has forgotten the ID before the stale query returns.
		repo.mu.Lock()
		snapshot = repo.tasks[id]
		snapshot.Status = models.StatusPending
		repo.mu.Unlock()
		close(releaseQuery)
		<-recovered
		_ = p.Stop(context.Background())
		if claims.Load() != 1 {
			t.Fatalf("duplicate enqueue produced %d claims", claims.Load())
		}
	})
}

type claimCountingRepository struct {
	*recoveryRepository
	claims *atomic.Int32
}

func (r *claimCountingRepository) TryStartProcessing(ctx context.Context, id uuid.UUID) (bool, error) {
	r.claims.Add(1)
	return r.recoveryRepository.TryStartProcessing(ctx, id)
}

func TestRecoveryCancellationJoinsQueryBeforeClosingQueue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		entered, finished := make(chan struct{}), make(chan struct{})
		repo := &recoveryRepository{query: func(ctx context.Context, _ int, _ []uuid.UUID) ([]models.UpdateTask, error) {
			close(entered)
			<-ctx.Done()
			defer close(finished)
			return nil, ctx.Err()
		}}
		p := lifecyclePool(repo, nil, 1, 1)
		_ = p.Start(context.Background())
		_ = p.StartRecovery(repo)
		<-entered
		if err := p.Stop(context.Background()); err != nil {
			t.Fatal(err)
		}
		select {
		case <-finished:
		default:
			t.Fatal("query still running after Stop")
		}
		select {
		case <-p.recoveryDone:
		default:
			t.Fatal("recovery still running")
		}
	})
}

func TestDuplicateTaskOnlyCallsProviderOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		task := models.UpdateTask{ID: uuid.New(), Status: models.StatusPending}
		repo := &recoveryRepository{tasks: map[uuid.UUID]models.UpdateTask{task.ID: task}}
		entered, release := make(chan struct{}), make(chan struct{})
		var calls atomic.Int32
		p := lifecyclePool(repo, queueRateProvider(func(context.Context, models.CurrencyPair) (float64, error) {
			calls.Add(1)
			close(entered)
			<-release
			return 20, nil
		}), 2, 2)
		_ = p.Enqueue(task)
		_ = p.Start(context.Background())
		<-entered
		var wg sync.WaitGroup
		for range 20 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := p.Enqueue(task); err != nil {
					t.Error(err)
				}
			}()
		}
		wg.Wait()
		close(release)
		_ = p.Stop(context.Background())
		if calls.Load() != 1 {
			t.Fatalf("calls=%d", calls.Load())
		}
	})
}

func TestRecoveryCreationAndShutdownRace(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		repo := &recoveryRepository{tasks: make(map[uuid.UUID]models.UpdateTask)}
		p := lifecyclePool(repo, queueRateProvider(func(context.Context, models.CurrencyPair) (float64, error) { return 20, nil }), 2, 4)
		_ = p.Start(context.Background())
		_ = p.StartRecovery(repo)
		var wg sync.WaitGroup
		for range 50 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := queueService(repo, p).CreateUpdate(context.Background(), "EUR", "MXN")
				if err != nil && !errors.Is(err, ErrQueueFull) {
					t.Error(err)
				}
			}()
		}
		wg.Add(1)
		go func() { defer wg.Done(); _ = p.Stop(context.Background()) }()
		wg.Wait()
		for _, task := range repo.tasks {
			if task.Status != models.StatusCompleted {
				t.Fatal("accepted task was not drained")
			}
		}
	})
}

type finalizationErrorRepository struct{ *recoveryRepository }

func (r finalizationErrorRepository) CompleteTask(context.Context, uuid.UUID, models.Quote, time.Time) (bool, error) {
	return false, errors.New("finalization unavailable")
}

func TestFinalizationErrorDoesNotRepeatProviderRequest(t *testing.T) {
	repo := finalizationErrorRepository{&recoveryRepository{tasks: make(map[uuid.UUID]models.UpdateTask)}}
	task := models.UpdateTask{ID: uuid.New(), Status: models.StatusPending}
	repo.tasks[task.ID] = task
	var calls atomic.Int32
	p := lifecyclePool(repo, queueRateProvider(func(context.Context, models.CurrencyPair) (float64, error) { calls.Add(1); return 20, nil }), 1, 1)
	_ = p.Enqueue(task)
	_ = p.Start(context.Background())
	_ = p.Stop(context.Background())
	if calls.Load() != 1 || repo.tasks[task.ID].Status != models.StatusProcessing {
		t.Fatal("finalization failure repeated or discarded work")
	}
}
