package httpapi

import (
	"net/http"

	"github.com/JhoJho19/exchange-rate-service/internal/models"
)

func responseForTask(task models.UpdateTask) (int, TaskResponse) {
	switch task.Status {
	case models.StatusPending, models.StatusProcessing:
		return http.StatusAccepted, TaskResponse{
			Message: "Exchange rate update is still in progress",
		}

	case models.StatusCompleted:
		if task.Quote == nil {
			return http.StatusInternalServerError, TaskResponse{
				Error: "Internal server error",
			}
		}

		return http.StatusOK, TaskResponse{
			Rate:      &task.Quote.Rate,
			UpdatedAt: &task.Quote.UpdatedAt,
		}

	case models.StatusFailed:
		return http.StatusBadGateway, TaskResponse{
			Error: "Failed to fetch exchange rate from external provider",
		}

	default:
		return http.StatusInternalServerError, TaskResponse{
			Error: "Internal server error",
		}
	}
}
