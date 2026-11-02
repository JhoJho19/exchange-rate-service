package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/JhoJho19/exchange-rate-service/internal/config"
	"github.com/JhoJho19/exchange-rate-service/internal/postgres"
	"github.com/JhoJho19/exchange-rate-service/internal/provider/exchangeratedev"
	"github.com/JhoJho19/exchange-rate-service/internal/service"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	signals, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	startup, cancelStartup := context.WithTimeout(signals, 10*time.Second)
	defer cancelStartup()
	pool, err := pgxpool.New(startup, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("create database pool: %w", err)
	}
	defer pool.Close()
	if err := pool.Ping(startup); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}
	repo := postgres.NewTaskRepository(pool)
	if err := repo.AcquireOwnership(startup); err != nil {
		return err
	}
	defer repo.CloseOwnership()
	provider := exchangeratedev.NewClient(cfg.RateProviderAPIKey, cfg.RateProviderURL, cfg.RateProviderTimeout)
	limiter := service.NewRateLimiter(cfg.RateLimitPerMinute, cfg.RateLimitPerHour)
	workers := service.NewWorkerPool(cfg.WorkerCount, cfg.QueueSize, repo, provider,
		cfg.RateProviderTimeout, limiter, service.RetryPolicy{MaxRetries: cfg.RateLimitMaxRetries, Delay: cfg.RateLimitRetryDelay})
	if err := repo.ResetProcessing(startup); err != nil {
		return err
	}
	// Signal cancellation starts draining; it is deliberately not the pool context.
	workerCtx, cancelWorkers := context.WithCancel(context.Background())
	defer cancelWorkers()
	if err := workers.Start(workerCtx); err != nil {
		return err
	}
	// Join before releasing ownership or closing the DB, also on startup errors.
	defer workers.Stop(context.Background())
	if err := workers.StartRecovery(repo); err != nil {
		cancelWorkers()
		return err
	}
	application := service.NewApplicationService(cfg.SupportedCurrencies, repo, workers)
	_ = application // Stage 7 will attach the HTTP interface.
	log.Println("Worker pool and recovery ready")
	select {
	case <-signals.Done():
		log.Println("Termination signal received; draining workers")
	case <-repo.OwnershipDone():
		cancelWorkers()
		_ = workers.Stop(context.Background())
		return postgres.ErrOwnershipUnavailable
	}
	shutdown, cancelShutdown := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancelShutdown()
	// Ownership may also be lost during graceful draining.
	watchDone := make(chan struct{})
	watchExited := make(chan struct{})
	defer func() { close(watchDone); <-watchExited }()
	go func() {
		defer close(watchExited)
		select {
		case <-repo.OwnershipDone():
			cancelWorkers()
		case <-watchDone:
		}
	}()
	if err := workers.Stop(shutdown); err != nil {
		return fmt.Errorf("forced worker shutdown: %w", err)
	}
	select {
	case <-repo.OwnershipDone():
		return postgres.ErrOwnershipUnavailable
	default:
	}
	return nil
}
