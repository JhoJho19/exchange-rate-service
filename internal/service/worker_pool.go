package service

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/JhoJho19/exchange-rate-service/internal/models"
)

type WorkerPool struct {
	jobs           chan models.UpdateTask
	workerCount    int
	waitGroup      sync.WaitGroup
	taskRepository TaskRepository
	rateProvider   RateProvider
	timeout        time.Duration
}

func NewWorkerPool(workerCount, queueSize int, taskRepository TaskRepository, rateProvider RateProvider, timeout time.Duration) *WorkerPool {
	return &WorkerPool{
		jobs:           make(chan models.UpdateTask, queueSize),
		workerCount:    workerCount,
		waitGroup:      sync.WaitGroup{},
		taskRepository: taskRepository,
		rateProvider:   rateProvider,
		timeout:        timeout,
	}
}

func (workerPool *WorkerPool) Start() {
	i := workerPool.workerCount
	for i > 0 {
		workerPool.waitGroup.Add(1)
		go workerPool.worker()
		i--
	}
}

func (workerPool *WorkerPool) worker() {
	defer workerPool.waitGroup.Done()

	for task := range workerPool.jobs {
		claimed, err := workerPool.taskRepository.TryStartProcessing(
			context.Background(),
			task.ID,
		)

		if err != nil {
			log.Printf("Failed to claim task %s: %v", task.ID, err)
			continue
		}

		if !claimed {
			log.Printf("Task %s cannot be claimed", task.ID)
			continue
		}

		log.Printf("Started processing task %s", task.ID)

		ctx, cancel := context.WithTimeout(
			context.Background(),
			workerPool.timeout,
		)

		rate, err := workerPool.rateProvider.GetRate(ctx, task.Pair)

		cancel()

		if err != nil {
			log.Printf("Failed to get rate for task %s: %v", task.ID, err)
			continue
		}

		log.Printf("Received rate for task %s: %f", task.ID, rate)
	}
}

var ErrQueueFull = errors.New("worker queue is full")

func (workerPool *WorkerPool) Enqueue(task models.UpdateTask) error {
	select {
	case workerPool.jobs <- task:
		return nil
	default:
		return ErrQueueFull
	}
}

func (workerPool *WorkerPool) Stop() {
	close(workerPool.jobs)
	workerPool.waitGroup.Wait()
}
