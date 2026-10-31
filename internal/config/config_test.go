package config

import (
	"maps"
	"os"
	"strings"
	"testing"
)

func TestLoad(t *testing.T) {
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
