package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"

	"github.com/JhoJho19/exchange-rate-service/internal/apperrors"
	"github.com/JhoJho19/exchange-rate-service/internal/models"
	"github.com/google/uuid"
)

const maxCreateUpdateBodyBytes = 4 * 1024

type updateCreator interface {
	CreateUpdate(context.Context, string, string) (uuid.UUID, error)
	GetUpdate(ctx context.Context, id uuid.UUID) (models.UpdateTask, error)
}

type Handler struct {
	applicationService updateCreator
}

func NewHandler(applicationService updateCreator) *Handler {
	return &Handler{
		applicationService: applicationService,
	}
}

type CreateUpdateRequest struct {
	Base  string `json:"base"`
	Quote string `json:"quote"`
}

func (h *Handler) CreateUpdate(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxCreateUpdateBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	var request *CreateUpdateRequest
	if err := decoder.Decode(&request); err != nil || request == nil {
		writeJSONError(w, http.StatusBadRequest, "Invalid JSON request")
		return
	}
	// Require EOF so that trailing values, malformed data, and oversized
	// trailing whitespace are rejected before creating a task.
	if err := decoder.Decode(new(any)); err != io.EOF {
		writeJSONError(w, http.StatusBadRequest, "Invalid JSON request")
		return
	}

	id, err := h.applicationService.CreateUpdate(
		r.Context(),
		request.Base,
		request.Quote,
	)
	if err != nil {
		switch {
		case errors.Is(err, apperrors.ErrInvalidCurrencyPair):
			writeJSONError(w, http.StatusBadRequest, "Invalid currency pair")
		case errors.Is(err, apperrors.ErrUnsupportedCurrency):
			writeJSONError(w, http.StatusBadRequest, "Unsupported currency")
		case errors.Is(err, apperrors.ErrServiceOverloaded):
			writeJSONError(w, http.StatusServiceUnavailable, "Service temporarily unavailable")
		default:
			log.Printf("Failed to create update: %v", err)
			writeJSONError(w, http.StatusInternalServerError, "Internal server error")
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	if err := json.NewEncoder(w).Encode(UpdateAcceptedResponse{ID: id}); err != nil {
		log.Printf("Failed to write update response: %v", err)
	}
}

func (h *Handler) GetUpdate(w http.ResponseWriter, r *http.Request) {
	idString := r.PathValue("id")

	id, err := uuid.Parse(idString)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "Invalid update ID")
		return
	}

	task, err := h.applicationService.GetUpdate(r.Context(), id)
	if err != nil {
		switch {
		case errors.Is(err, apperrors.ErrTaskNotFound):
			writeJSONError(w, http.StatusNotFound, "Update not found")

		default:
			log.Printf("Failed to get update %s: %v", id, err)
			writeJSONError(w, http.StatusInternalServerError, "Internal server error")
		}

		return
	}

	status, response := responseForTask(task)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("Failed to write update response: %v", err)
	}
}
