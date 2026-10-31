package models

import (
	"time"

	"github.com/google/uuid"
)

type UpdateStatus string

const (
	StatusPending    UpdateStatus = "PENDING"
	StatusProcessing UpdateStatus = "PROCESSING"
	StatusCompleted  UpdateStatus = "COMPLETED"
	StatusFailed     UpdateStatus = "FAILED"
)

type UpdateTask struct {
	ID           uuid.UUID
	Pair         CurrencyPair
	Status       UpdateStatus
	Quote        *Quote
	ErrorMessage string
	CreatedAt    time.Time
	CompletedAt  *time.Time
}
