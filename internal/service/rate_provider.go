package service

import (
	"context"

	"github.com/JhoJho19/exchange-rate-service/internal/models"
)

type RateProvider interface {
	GetRate(ctx context.Context, pair models.CurrencyPair) (float64, error)
}
