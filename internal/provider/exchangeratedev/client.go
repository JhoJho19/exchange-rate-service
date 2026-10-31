package exchangeratedev

import (
	"context"
	"errors"
	"time"

	"github.com/JhoJho19/exchange-rate-service/internal/models"
)

type Client struct {
	apiKey  string
	url     string
	timeout time.Duration
}

func NewClient(apiKey, url string, timeout time.Duration) *Client {
	return &Client{
		apiKey:  apiKey,
		url:     url,
		timeout: timeout,
	}
}

func (c *Client) GetRate(ctx context.Context, pair models.CurrencyPair) (float64, error) {
	return 0, errors.New("not implemented")
}
