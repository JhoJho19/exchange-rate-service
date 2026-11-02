package config

import (
	"maps"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	setValidEnvironment(t)
	tests := []struct {
		name    string
		value   string
		want    map[string]struct{}
		wantErr string
	}{
		{name: "uppercase", value: "USD,EUR,MXN", want: map[string]struct{}{"USD": {}, "EUR": {}, "MXN": {}}},
		{name: "lowercase", value: "usd, eur, mxn", want: map[string]struct{}{"USD": {}, "EUR": {}, "MXN": {}}},
		{name: "duplicates", value: "USD,EUR,USD", want: map[string]struct{}{"USD": {}, "EUR": {}}},
		{name: "whitespace", value: "USD , EUR , MXN", want: map[string]struct{}{"USD": {}, "EUR": {}, "MXN": {}}},
		{name: "normalized duplicates", value: " usd, USD ,uSd", want: map[string]struct{}{"USD": {}}},
		{name: "unicode whitespace", value: "\u00a0usd\u2003,\tEuR\n", want: map[string]struct{}{"USD": {}, "EUR": {}}},
		{name: "format only", value: "ZZZ", want: map[string]struct{}{"ZZZ": {}}},
		{name: "empty", value: "", wantErr: "must be set and non-empty"},
		{name: "only whitespace", value: " \t\n", wantErr: "must be set and non-empty"},
		{name: "empty entry", value: "USD,,EUR", wantErr: "invalid currency code \"\" at entry 2"},
		{name: "short", value: "USD,EU", wantErr: "invalid currency code \"EU\" at entry 2"},
		{name: "long", value: "USD,EURO", wantErr: "invalid currency code \"EURO\" at entry 2"},
		{name: "digits", value: "USD,123", wantErr: "invalid currency code \"123\" at entry 2"},
		{name: "Cyrillic", value: "USD,РУБ", wantErr: "invalid currency code \"РУБ\" at entry 2"},
		{name: "trailing comma", value: "USD,EUR,", wantErr: "invalid currency code \"\" at entry 3"},
		{name: "leading comma", value: ",USD", wantErr: "invalid currency code \"\" at entry 1"},
		{name: "blank entry", value: "USD, ,EUR", wantErr: "invalid currency code"},
		{name: "internal space", value: "USD,E R", wantErr: "invalid currency code"},
		{name: "punctuation", value: "USD,EU!", wantErr: "invalid currency code"},
		{name: "non Latin three bytes", value: "USD,日", wantErr: "invalid currency code"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("SUPPORTED_CURRENCIES", tt.value)
			got, err := Load()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), "SUPPORTED_CURRENCIES") || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Load() error = %v, want SUPPORTED_CURRENCIES error containing %q", err, tt.wantErr)
				}
				if got.SupportedCurrencies != nil {
					t.Errorf("Load() returned partial configuration: %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() unexpected error: %v", err)
			}
			if !maps.Equal(got.SupportedCurrencies, tt.want) {
				t.Errorf("Load() currencies = %v, want %v", got.SupportedCurrencies, tt.want)
			}
		})
	}
}

func TestLoadMissingSupportedCurrencies(t *testing.T) {
	setValidEnvironment(t)
	// Setenv registers cleanup to restore the original environment after Unsetenv.
	t.Setenv("SUPPORTED_CURRENCIES", "")
	if err := os.Unsetenv("SUPPORTED_CURRENCIES"); err != nil {
		t.Fatal(err)
	}

	got, err := Load()
	if err == nil || !strings.Contains(err.Error(), "SUPPORTED_CURRENCIES must be set and non-empty") {
		t.Fatalf("Load() error = %v, want missing SUPPORTED_CURRENCIES error", err)
	}
	if got.SupportedCurrencies != nil {
		t.Errorf("Load() returned configuration for missing environment variable: %+v", got)
	}
}

func setValidEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("WORKER_COUNT", "2")
	t.Setenv("QUEUE_SIZE", "100")
	t.Setenv("SUPPORTED_CURRENCIES", "USD,EUR")
	t.Setenv("RATE_PROVIDER_URL", "https://rates.example.com/v1")
	t.Setenv("RATE_PROVIDER_TIMEOUT", "10s")
	t.Setenv("RATE_PROVIDER_API_KEY", "")
	t.Setenv("DATABASE_URL", "postgres://user:password@localhost:5432/exchange_rates?sslmode=disable")
}

func TestLoadDatabaseConfiguration(t *testing.T) {
	const databaseURL = "postgres://user:password@localhost:5432/exchange_rates?sslmode=disable"
	tests := []struct {
		name    string
		value   string
		missing bool
		want    string
		wantErr bool
	}{
		{name: "missing URL", missing: true, wantErr: true},
		{name: "empty URL", wantErr: true},
		{name: "blank URL", value: " \t\n", wantErr: true},
		{name: "PostgreSQL URL", value: databaseURL, want: databaseURL},
		{name: "surrounding whitespace", value: " \t" + databaseURL + "\n", want: databaseURL},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setValidEnvironment(t)
			t.Setenv("DATABASE_URL", tt.value)
			if tt.missing {
				// Setenv above registers restoration even when the variable is unset.
				if err := os.Unsetenv("DATABASE_URL"); err != nil {
					t.Fatal(err)
				}
			}
			got, err := Load()
			if tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), "DATABASE_URL must be set and non-empty") {
					t.Fatalf("Load() error = %v, want DATABASE_URL must be set and non-empty", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() unexpected error: %v", err)
			}
			if got.DatabaseURL != tt.want {
				t.Errorf("DatabaseURL = %q, want %q", got.DatabaseURL, tt.want)
			}
		})
	}
}

func TestLoadProviderConfiguration(t *testing.T) {
	const secret = "test-secret-key"
	tests := []struct {
		name     string
		variable string
		value    string
		missing  bool
		wantErr  bool
	}{
		{name: "HTTP", variable: "RATE_PROVIDER_URL", value: "http://rates.example.com"},
		{name: "HTTPS", variable: "RATE_PROVIDER_URL", value: "https://rates.example.com/v1"},
		{name: "IPv4", variable: "RATE_PROVIDER_URL", value: "http://127.0.0.1:8080"},
		{name: "IPv6", variable: "RATE_PROVIDER_URL", value: "http://[::1]:8080"},
		{name: "missing URL", variable: "RATE_PROVIDER_URL", missing: true, wantErr: true},
		{name: "empty URL", variable: "RATE_PROVIDER_URL", wantErr: true},
		{name: "blank URL", variable: "RATE_PROVIDER_URL", value: " \t", wantErr: true},
		{name: "malformed URL", variable: "RATE_PROVIDER_URL", value: "://broken", wantErr: true},
		{name: "unsupported scheme", variable: "RATE_PROVIDER_URL", value: "ftp://rates.example.com", wantErr: true},
		{name: "relative URL", variable: "RATE_PROVIDER_URL", value: "rates.example.com", wantErr: true},
		{name: "missing hostname", variable: "RATE_PROVIDER_URL", value: "https:///v1", wantErr: true},
		{name: "invalid hostname", variable: "RATE_PROVIDER_URL", value: "https://bad_host.example.com", wantErr: true},
		{name: "empty hostname label", variable: "RATE_PROVIDER_URL", value: "https://rates..example.com", wantErr: true},
		{name: "leading hostname hyphen", variable: "RATE_PROVIDER_URL", value: "https://-rates.example.com", wantErr: true},
		{name: "invalid IP literal", variable: "RATE_PROVIDER_URL", value: "https://[not-an-ip]", wantErr: true},
		{name: "URL parser error with secret", variable: "RATE_PROVIDER_URL", value: "https://rates.example.com/%" + secret, wantErr: true},
		{name: "seconds", variable: "RATE_PROVIDER_TIMEOUT", value: "5s"},
		{name: "milliseconds", variable: "RATE_PROVIDER_TIMEOUT", value: "500ms"},
		{name: "default timeout", variable: "RATE_PROVIDER_TIMEOUT", missing: true},
		{name: "invalid timeout", variable: "RATE_PROVIDER_TIMEOUT", value: "five seconds", wantErr: true},
		{name: "empty timeout", variable: "RATE_PROVIDER_TIMEOUT", wantErr: true},
		{name: "zero timeout", variable: "RATE_PROVIDER_TIMEOUT", value: "0s", wantErr: true},
		{name: "negative timeout", variable: "RATE_PROVIDER_TIMEOUT", value: "-1s", wantErr: true},
		{name: "timeout containing secret", variable: "RATE_PROVIDER_TIMEOUT", value: secret, wantErr: true},
		{name: "API key", variable: "RATE_PROVIDER_API_KEY", value: secret},
		{name: "missing API key", variable: "RATE_PROVIDER_API_KEY", missing: true},
		{name: "empty API key", variable: "RATE_PROVIDER_API_KEY"},
		{name: "invalid currencies with API key", variable: "SUPPORTED_CURRENCIES", value: "USD,12", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setValidEnvironment(t)
			t.Setenv("RATE_PROVIDER_API_KEY", secret)
			t.Setenv(tt.variable, tt.value)
			if tt.missing {
				// Setenv above registers restoration even when the variable is unset.
				if err := os.Unsetenv(tt.variable); err != nil {
					t.Fatal(err)
				}
			}
			got, err := Load()
			if tt.wantErr {
				if err == nil {
					t.Fatal("Load() succeeded, want an error")
				}
				if strings.Contains(err.Error(), secret) {
					t.Fatal("Load() error exposed the API key")
				}
				if !strings.Contains(err.Error(), tt.variable) {
					t.Errorf("Load() error = %v, want parameter %s", err, tt.variable)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() unexpected error: %v", err)
			}
			wantURL := "https://rates.example.com/v1"
			if tt.variable == "RATE_PROVIDER_URL" {
				wantURL = tt.value
			}
			wantTimeout := 10 * time.Second
			if tt.name == "seconds" {
				wantTimeout = 5 * time.Second
			} else if tt.name == "milliseconds" {
				wantTimeout = 500 * time.Millisecond
			}
			wantKey := secret
			if tt.variable == "RATE_PROVIDER_API_KEY" {
				wantKey = tt.value
			}
			if got.RateProviderURL != wantURL {
				t.Errorf("URL = %q, want %q", got.RateProviderURL, wantURL)
			}
			if got.RateProviderTimeout != wantTimeout {
				t.Errorf("timeout = %v, want %v", got.RateProviderTimeout, wantTimeout)
			}
			if got.RateProviderAPIKey != wantKey {
				t.Error("API key does not match expected value")
			}
			if !maps.Equal(got.SupportedCurrencies, map[string]struct{}{"USD": {}, "EUR": {}}) {
				t.Errorf("unexpected currencies: %v", got.SupportedCurrencies)
			}
		})
	}
}

func TestLoadRateLimits(t *testing.T) {
	setValidEnvironment(t)
	for _, name := range []string{"RATE_LIMIT_PER_MINUTE", "RATE_LIMIT_PER_HOUR", "RATE_LIMIT_MAX_RETRIES", "RATE_LIMIT_RETRY_DELAY"} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RateLimitPerMinute != 10 || cfg.RateLimitPerHour != 100 || cfg.RateLimitMaxRetries != 2 || cfg.RateLimitRetryDelay != time.Minute {
		t.Fatal("incorrect rate limit defaults")
	}
	t.Run("configured", func(t *testing.T) {
		t.Setenv("RATE_LIMIT_PER_MINUTE", " 5 ")
		t.Setenv("RATE_LIMIT_PER_HOUR", "50")
		t.Setenv("RATE_LIMIT_MAX_RETRIES", "0")
		t.Setenv("RATE_LIMIT_RETRY_DELAY", "2s")
		cfg, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.RateLimitPerMinute != 5 || cfg.RateLimitPerHour != 50 || cfg.RateLimitMaxRetries != 0 || cfg.RateLimitRetryDelay != 2*time.Second {
			t.Fatal("configuration not applied")
		}
	})
	for _, tc := range []struct{ name, value string }{
		{"RATE_LIMIT_PER_MINUTE", "0"}, {"RATE_LIMIT_PER_HOUR", "-1"},
		{"RATE_LIMIT_PER_MINUTE", ""}, {"RATE_LIMIT_PER_HOUR", "1.5"},
		{"RATE_LIMIT_MAX_RETRIES", "-1"}, {"RATE_LIMIT_MAX_RETRIES", "invalid"},
		{"RATE_LIMIT_RETRY_DELAY", "0s"}, {"RATE_LIMIT_RETRY_DELAY", "-1s"},
		{"RATE_LIMIT_RETRY_DELAY", "invalid"},
	} {
		t.Run(tc.name+"/"+tc.value, func(t *testing.T) {
			t.Setenv(tc.name, tc.value)
			if _, err := Load(); err == nil || !strings.Contains(err.Error(), tc.name) {
				t.Fatalf("expected %s validation error, got %v", tc.name, err)
			}
		})
	}
}

func TestShutdownTimeout(t *testing.T) {
	for _, tc := range []struct {
		value          string
		unset, invalid bool
		want           time.Duration
	}{
		{unset: true, want: 30 * time.Second}, {value: "250ms", want: 250 * time.Millisecond},
		{value: "0s", invalid: true}, {value: "-1s", invalid: true}, {value: "", invalid: true}, {value: "bad", invalid: true},
	} {
		t.Run(tc.value+strconv.FormatBool(tc.unset), func(t *testing.T) {
			setValidEnvironment(t)
			t.Setenv("SHUTDOWN_TIMEOUT", tc.value)
			if tc.unset {
				if err := os.Unsetenv("SHUTDOWN_TIMEOUT"); err != nil {
					t.Fatal(err)
				}
			}
			cfg, err := Load()
			if tc.invalid {
				if err == nil || !strings.Contains(err.Error(), "SHUTDOWN_TIMEOUT") {
					t.Fatalf("validation: %v", err)
				}
			} else if err != nil || cfg.ShutdownTimeout != tc.want {
				t.Fatalf("timeout=%v err=%v", cfg.ShutdownTimeout, err)
			}
		})
	}
}
