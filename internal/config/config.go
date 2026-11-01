package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"
)

type Config struct {
	SupportedCurrencies map[string]struct{}
	RateProviderURL     string
	RateProviderTimeout time.Duration
	RateProviderAPIKey  string
	DatabaseURL         string
}

// Load reads and validates application configuration from environment variables.
func Load() (Config, error) {
	value := os.Getenv("SUPPORTED_CURRENCIES")
	if strings.TrimSpace(value) == "" {
		return Config{}, fmt.Errorf("SUPPORTED_CURRENCIES must be set and non-empty")
	}

	supportedCurrencies := make(map[string]struct{})
	for i, entry := range strings.Split(value, ",") {
		code := strings.ToUpper(strings.TrimSpace(entry))
		if !validCurrencyCode(code) {
			return Config{}, fmt.Errorf("SUPPORTED_CURRENCIES: invalid currency code %q at entry %d: must contain exactly three letters A-Z", entry, i+1)
		}
		supportedCurrencies[code] = struct{}{}
	}

	providerURL := os.Getenv("RATE_PROVIDER_URL")
	if strings.TrimSpace(providerURL) == "" {
		return Config{}, fmt.Errorf("RATE_PROVIDER_URL must be set and non-empty")
	}
	parsedURL, err := url.Parse(providerURL)
	if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") || !validHostname(parsedURL.Hostname()) {
		return Config{}, fmt.Errorf("RATE_PROVIDER_URL must be a valid HTTP or HTTPS URL with a valid hostname")
	}

	timeout := 10 * time.Second
	if value, set := os.LookupEnv("RATE_PROVIDER_TIMEOUT"); set {
		timeout, err = time.ParseDuration(value)
		if err != nil || timeout <= 0 {
			return Config{}, fmt.Errorf("RATE_PROVIDER_TIMEOUT must be a valid positive duration")
		}
	}

	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL must be set and non-empty")
	}

	return Config{
		SupportedCurrencies: supportedCurrencies,
		RateProviderURL:     providerURL,
		RateProviderTimeout: timeout,
		RateProviderAPIKey:  os.Getenv("RATE_PROVIDER_API_KEY"),
		DatabaseURL:         databaseURL,
	}, nil
}

// validHostname accepts IP literals and DNS names without resolving them.
func validHostname(host string) bool {
	if net.ParseIP(host) != nil {
		return true
	}
	host = strings.TrimSuffix(host, ".")
	if len(host) == 0 || len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}

func validCurrencyCode(code string) bool {
	if len(code) != 3 {
		return false
	}
	for _, c := range code {
		if c < 'A' || c > 'Z' {
			return false
		}
	}
	return true
}
