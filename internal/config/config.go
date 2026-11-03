package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	HTTPAddress         string
	ShutdownTimeout     time.Duration
	SupportedCurrencies map[string]struct{}
	RateProviderURL     string
	RateProviderTimeout time.Duration
	RateProviderAPIKey  string
	DatabaseURL         string
	WorkerCount         int
	QueueSize           int
	RateLimitPerMinute  int
	RateLimitPerHour    int
	RateLimitMaxRetries int
	RateLimitRetryDelay time.Duration
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

	shutdownTimeout := 30 * time.Second
	if value, set := os.LookupEnv("SHUTDOWN_TIMEOUT"); set {
		shutdownTimeout, err = time.ParseDuration(strings.TrimSpace(value))
		if err != nil || shutdownTimeout <= 0 {
			return Config{}, fmt.Errorf("SHUTDOWN_TIMEOUT must be a valid positive duration")
		}
	}

	httpAddress := ":8080"
	if value, set := os.LookupEnv("HTTP_ADDRESS"); set {
		httpAddress = strings.TrimSpace(value)
	}
	host, port, addressErr := net.SplitHostPort(httpAddress)
	portNumber, portErr := strconv.Atoi(port)
	if addressErr != nil || portErr != nil || portNumber < 0 || portNumber > 65535 ||
		(host != "" && !validHostname(host)) ||
		(strings.HasPrefix(httpAddress, "[") && net.ParseIP(host) == nil) ||
		strings.IndexFunc(port, func(c rune) bool { return c < '0' || c > '9' }) >= 0 {
		return Config{}, fmt.Errorf("HTTP_ADDRESS must be a valid host:port address with a numeric port from 0 to 65535")
	}

	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL must be set and non-empty")
	}

	workerCountStr := os.Getenv("WORKER_COUNT")
	workerCount, err := strconv.Atoi(strings.TrimSpace(workerCountStr))
	if err != nil || workerCount <= 0 {
		return Config{}, fmt.Errorf("WORKER_COUNT must be a valid positive integer")
	}

	queueSizeStr := os.Getenv("QUEUE_SIZE")
	queueSize, err := strconv.Atoi(strings.TrimSpace(queueSizeStr))
	if err != nil || queueSize <= 0 {
		return Config{}, fmt.Errorf("QUEUE_SIZE must be a valid positive integer")
	}

	perMinute, err := optionalInt("RATE_LIMIT_PER_MINUTE", 10, 1)
	if err != nil {
		return Config{}, err
	}
	perHour, err := optionalInt("RATE_LIMIT_PER_HOUR", 100, 1)
	if err != nil {
		return Config{}, err
	}
	maxRetries, err := optionalInt("RATE_LIMIT_MAX_RETRIES", 2, 0)
	if err != nil {
		return Config{}, err
	}
	retryDelay := time.Minute
	if value, set := os.LookupEnv("RATE_LIMIT_RETRY_DELAY"); set {
		retryDelay, err = time.ParseDuration(strings.TrimSpace(value))
		if err != nil || retryDelay <= 0 {
			return Config{}, fmt.Errorf("RATE_LIMIT_RETRY_DELAY must be a valid positive duration")
		}
	}

	return Config{
		HTTPAddress:         httpAddress,
		ShutdownTimeout:     shutdownTimeout,
		SupportedCurrencies: supportedCurrencies,
		RateProviderURL:     providerURL,
		RateProviderTimeout: timeout,
		RateProviderAPIKey:  os.Getenv("RATE_PROVIDER_API_KEY"),
		DatabaseURL:         databaseURL,
		WorkerCount:         workerCount,
		QueueSize:           queueSize,
		RateLimitPerMinute:  perMinute,
		RateLimitPerHour:    perHour,
		RateLimitMaxRetries: maxRetries,
		RateLimitRetryDelay: retryDelay,
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

func optionalInt(name string, fallback, minimum int) (int, error) {
	value, set := os.LookupEnv(name)
	if !set {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || parsed < minimum {
		return 0, fmt.Errorf("%s must be an integer >= %d", name, minimum)
	}
	return parsed, nil
}
