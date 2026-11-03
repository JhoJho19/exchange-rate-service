package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/JhoJho19/exchange-rate-service/internal/service"
)

type Handler struct {
	applicationService *service.ApplicationService
}

func NewHandler(applicationService *service.ApplicationService) *Handler {
	return &Handler{
		applicationService: applicationService,
	}
}

type CreateUpdateRequest struct {
	Base  string `json:"base"`
	Quote string `json:"quote"`
}

func (h *Handler) CreateUpdate(w http.ResponseWriter, r *http.Request) {
	var request CreateUpdateRequest

	err := json.NewDecoder(r.Body).Decode(&request)
	if err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	id, err := h.applicationService.CreateUpdate(
		r.Context(),
		request.Base,
		request.Quote,
	)

	if err != nil {
		http.Error(w, "Failed to create update", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)

	json.NewEncoder(w).Encode(map[string]string{
		"id": id.String(),
	})
}
