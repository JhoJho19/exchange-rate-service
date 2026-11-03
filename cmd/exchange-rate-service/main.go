package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/JhoJho19/exchange-rate-service/internal/config"
	"github.com/JhoJho19/exchange-rate-service/internal/httpapi"
	"github.com/JhoJho19/exchange-rate-service/internal/postgres"
	"github.com/JhoJho19/exchange-rate-service/internal/provider/exchangeratedev"
	"github.com/JhoJho19/exchange-rate-service/internal/service"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	httpReadHeaderTimeout = 5 * time.Second
	httpReadTimeout       = 10 * time.Second
	httpWriteTimeout      = 15 * time.Second
	httpIdleTimeout       = 60 * time.Second
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

	log.Println("Configuration loaded successfully")

	signals, stopSignals := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
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

	log.Println("Database connection established successfully")

	var taskRepo = postgres.NewTaskRepository(pool)

	if err := taskRepo.AcquireOwnership(startup); err != nil {
		return err
	}
	defer taskRepo.CloseOwnership()

	var rateProvider service.RateProvider = exchangeratedev.NewClient(
		cfg.RateProviderAPIKey,
		cfg.RateProviderURL,
		cfg.RateProviderTimeout,
	)

	var rateLimiter = service.NewRateLimiter(
		cfg.RateLimitPerMinute,
		cfg.RateLimitPerHour,
	)

	var workerPool = service.NewWorkerPool(
		cfg.WorkerCount,
		cfg.QueueSize,
		taskRepo,
		rateProvider,
		cfg.RateProviderTimeout,
		rateLimiter,
		service.RetryPolicy{
			MaxRetries: cfg.RateLimitMaxRetries,
			Delay:      cfg.RateLimitRetryDelay,
		},
	)

	if err := taskRepo.ResetProcessing(startup); err != nil {
		return err
	}

	workerCtx, cancelWorkers := context.WithCancel(context.Background())
	defer cancelWorkers()

	if err := workerPool.Start(workerCtx); err != nil {
		return err
	}
	defer workerPool.Stop(context.Background())

	log.Println("Worker pool started successfully")

	if err := workerPool.StartRecovery(taskRepo); err != nil {
		cancelWorkers()
		return err
	}

	log.Println("Task recovery started successfully")

	var applicationService = service.NewApplicationService(
		cfg.SupportedCurrencies,
		taskRepo,
		workerPool,
	)

	var handler *httpapi.Handler = httpapi.NewHandler(applicationService)

	log.Println("Application service initialized successfully")

	var mux = httpapi.NewRouter(handler)

	var httpServer = &http.Server{
		Addr:              cfg.HTTPAddress,
		Handler:           mux,
		ReadHeaderTimeout: httpReadHeaderTimeout,
		ReadTimeout:       httpReadTimeout,
		WriteTimeout:      httpWriteTimeout,
		IdleTimeout:       httpIdleTimeout,
	}

	httpErrors := make(chan error, 1)

	go func() {
		err := httpServer.ListenAndServe()

		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("HTTP server error: %v", err)
			httpErrors <- err
		}
	}()

	log.Printf("HTTP server listening on %s", cfg.HTTPAddress)

	var serverErr error

	select {
	case <-signals.Done():
		log.Println("Termination signal received")

	case <-taskRepo.OwnershipDone():
		cancelWorkers()
		serverErr = postgres.ErrOwnershipUnavailable

	case err := <-httpErrors:
		serverErr = fmt.Errorf("HTTP server failed: %w", err)
	}

	shutdown, cancelShutdown := context.WithTimeout(
		context.Background(),
		cfg.ShutdownTimeout,
	)
	defer cancelShutdown()

	watchDone := make(chan struct{})
	watchExited := make(chan struct{})

	defer func() {
		close(watchDone)
		<-watchExited
	}()

	go func() {
		defer close(watchExited)

		select {
		case <-taskRepo.OwnershipDone():
			cancelWorkers()

		case <-watchDone:
		}
	}()

	if err := shutdownHTTPServer(shutdown, httpServer); err != nil {
		log.Printf("HTTP server shutdown error: %v", err)
		serverErr = errors.Join(serverErr, err)
	}

	if err := workerPool.Stop(shutdown); err != nil {
		return errors.Join(serverErr, fmt.Errorf("forced worker shutdown: %w", err))
	}

	select {
	case <-taskRepo.OwnershipDone():
		return errors.Join(serverErr, postgres.ErrOwnershipUnavailable)

	default:
	}

	if serverErr != nil {
		return serverErr
	}

	return nil
}

// Shutdown ends admission and waits for active requests before workers drain.
func shutdownHTTPServer(ctx context.Context, server *http.Server) error {
	if err := server.Shutdown(ctx); err != nil {
		return errors.Join(fmt.Errorf("HTTP shutdown: %w", err), server.Close())
	}
	return nil
}
