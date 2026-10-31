package config

import (
	"fmt"
	"os"
	"strings"
)

type Config struct {
	SupportedCurrencies map[string]struct{}
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

	return Config{SupportedCurrencies: supportedCurrencies}, nil
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
