package main

import (
	"context"
	"log"
	"time"

	"github.com/JhoJho19/exchange-rate-service/internal/config"
	"github.com/JhoJho19/exchange-rate-service/internal/postgres"
	"github.com/JhoJho19/exchange-rate-service/internal/provider/exchangeratedev"
	"github.com/JhoJho19/exchange-rate-service/internal/service"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	log.Println("Configuration loaded successfully")

	var rateProvider service.RateProvider = exchangeratedev.NewClient(
		cfg.RateProviderAPIKey,
		cfg.RateProviderURL,
		cfg.RateProviderTimeout,
	)

	_ = rateProvider

	pool, err := pgxpool.New(context.Background(), cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("Failed to create database pool: %v", err)
	}
	defer pool.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := pool.Ping(ctx); err != nil {
		log.Fatalf("Failed to ping database: %v", err)
	}

	var taskRepo = postgres.NewTaskRepository(pool)
	_ = taskRepo

	log.Println("Database connection established successfully")

	var applicationService = service.NewApplicationService(cfg.SupportedCurrencies, taskRepo)
	_ = applicationService

	log.Println("Application service initialized successfully")

	var workerPool = service.NewWorkerPool(cfg.WorkerCount, cfg.QueueSize, taskRepo, rateProvider, cfg.RateProviderTimeout)

	workerPool.Start()
	defer workerPool.Stop()

	log.Println("Worker pool started successfuly")
}
