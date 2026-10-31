package httpapi

import (
	"time"

	"github.com/google/uuid"
)

type UpdateAcceptedResponse struct {
	ID uuid.UUID `json:"id"`
}

type TaskResponse struct {
	Rate      *float64   `json:"rate,omitempty"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
	Message   string     `json:"message,omitempty"`
	Error     string     `json:"error,omitempty"`
}
