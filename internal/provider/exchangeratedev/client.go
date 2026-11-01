package exchangeratedev

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/JhoJho19/exchange-rate-service/internal/apperrors"
	"github.com/JhoJho19/exchange-rate-service/internal/models"
)

const maxResponseSize = 1 << 20 // 1 MiB

type Client struct {
	apiKey     string
	url        string
	httpClient *http.Client
}

func NewClient(apiKey, url string, timeout time.Duration) *Client {
	return &Client{
		apiKey:     apiKey,
		url:        url,
		httpClient: &http.Client{Timeout: timeout},
	}
}

func (c *Client) GetRate(ctx context.Context, pair models.CurrencyPair) (float64, error) {
	endpoint, err := url.Parse(c.url)
	if err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Host == "" {
		return 0, apperrors.ErrProviderInvalidResponse
	}

	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/latest/" + pair.BaseCurrency
	endpoint.RawPath = ""

	query := endpoint.Query()
	query.Set("symbols", pair.QuoteCurrency)
	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return 0, apperrors.ErrProviderInvalidResponse
	}

	req.Header.Set("Accept", "application/json")

	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, mapTransportError(err)
	}
	defer resp.Body.Close()

	if mapped := mapHTTPStatus(resp.StatusCode); mapped != nil {
		return 0, mapped
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize+1))
	if err != nil {
		return 0, mapTransportError(err)
	}

	if len(body) > maxResponseSize {
		return 0, apperrors.ErrProviderInvalidResponse
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, mapProviderError(resp.StatusCode, body)
	}

	var payload struct {
		Result string             `json:"result"`
		Base   string             `json:"base"`
		Rates  map[string]float64 `json:"rates"`
	}

	if err := json.Unmarshal(body, &payload); err != nil {
		return 0, apperrors.ErrProviderInvalidResponse
	}

	if payload.Result == "error" {
		return 0, mapProviderError(resp.StatusCode, body)
	}

	if payload.Result != "success" || payload.Base != pair.BaseCurrency {
		return 0, apperrors.ErrProviderInvalidResponse
	}

	rate, ok := payload.Rates[pair.QuoteCurrency]

	if !ok || rate <= 0 || math.IsNaN(rate) || math.IsInf(rate, 0) {
		return 0, apperrors.ErrProviderInvalidResponse
	}

	return rate, nil
}
