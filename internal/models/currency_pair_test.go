package models

import (
	"errors"
	"testing"

	"github.com/JhoJho19/exchange-rate-service/internal/apperrors"
)

func TestNewCurrencyPair(t *testing.T) {
	supportedCurrencies := map[string]struct{}{
		"USD": {},
		"EUR": {},
		"MXN": {},
	}

	tests := []struct {
		name        string
		base        string
		quote       string
		want        CurrencyPair
		expectedErr error
	}{
		{name: "uppercase", base: "EUR", quote: "MXN", want: CurrencyPair{"EUR", "MXN"}},
		{name: "lowercase", base: "eur", quote: "mxn", want: CurrencyPair{"EUR", "MXN"}},
		{name: "whitespace", base: " \tEUR\n", quote: "\rMXN ", want: CurrencyPair{"EUR", "MXN"}},
		{name: "mixed case and whitespace", base: " eUr\t", quote: "\nmXn ", want: CurrencyPair{"EUR", "MXN"}},
		{name: "unicode whitespace", base: "\u00a0EUR\u2003", quote: "\u2003MXN\u00a0", want: CurrencyPair{"EUR", "MXN"}},
		{name: "USD EUR", base: "USD", quote: "EUR", want: CurrencyPair{"USD", "EUR"}},
		{name: "MXN USD", base: "MXN", quote: "USD", want: CurrencyPair{"MXN", "USD"}},
		{name: "unsupported base", base: "GBP", quote: "MXN", expectedErr: apperrors.ErrUnsupportedCurrency},
		{name: "unsupported quote", base: "EUR", quote: "JPY", expectedErr: apperrors.ErrUnsupportedCurrency},
		{name: "both unsupported", base: "GBP", quote: "JPY", expectedErr: apperrors.ErrUnsupportedCurrency},
		{name: "whitespace base", base: " \t\n", quote: "MXN", expectedErr: apperrors.ErrInvalidCurrencyPair},
		{name: "whitespace quote", base: "EUR", quote: " \t\n", expectedErr: apperrors.ErrInvalidCurrencyPair},
		{name: "both whitespace", base: " \t", quote: "\n ", expectedErr: apperrors.ErrInvalidCurrencyPair},
		{name: "short base", base: "EU", quote: "MXN", expectedErr: apperrors.ErrInvalidCurrencyPair},
		{name: "short quote", base: "EUR", quote: "MX", expectedErr: apperrors.ErrInvalidCurrencyPair},
		{name: "long base", base: "EURO", quote: "MXN", expectedErr: apperrors.ErrInvalidCurrencyPair},
		{name: "long quote", base: "EUR", quote: "MXNN", expectedErr: apperrors.ErrInvalidCurrencyPair},
		{name: "digit base", base: "EU1", quote: "MXN", expectedErr: apperrors.ErrInvalidCurrencyPair},
		{name: "digit quote", base: "EUR", quote: "MX1", expectedErr: apperrors.ErrInvalidCurrencyPair},
		{name: "special base", base: "EU!", quote: "MXN", expectedErr: apperrors.ErrInvalidCurrencyPair},
		{name: "special quote", base: "EUR", quote: "MX!", expectedErr: apperrors.ErrInvalidCurrencyPair},
		{name: "internal space base", base: "E R", quote: "MXN", expectedErr: apperrors.ErrInvalidCurrencyPair},
		{name: "internal space quote", base: "EUR", quote: "M N", expectedErr: apperrors.ErrInvalidCurrencyPair},
		{name: "Cyrillic base", base: "ЕUR", quote: "MXN", expectedErr: apperrors.ErrInvalidCurrencyPair},
		{name: "Cyrillic quote", base: "EUR", quote: "МXN", expectedErr: apperrors.ErrInvalidCurrencyPair},
		{name: "non Latin base", base: "日", quote: "MXN", expectedErr: apperrors.ErrInvalidCurrencyPair},
		{name: "non Latin quote", base: "EUR", quote: "日", expectedErr: apperrors.ErrInvalidCurrencyPair},
		{name: "identical", base: "EUR", quote: "EUR", expectedErr: apperrors.ErrInvalidCurrencyPair},
		{name: "identical different case", base: "eur", quote: "EUR", expectedErr: apperrors.ErrInvalidCurrencyPair},
		{name: "identical after trimming", base: " eUr ", quote: "EUR\t", expectedErr: apperrors.ErrInvalidCurrencyPair},
		{name: "unsupported base", base: "GBP", quote: "MXN", expectedErr: apperrors.ErrInvalidCurrencyPair},
		{name: "unsupported quote", base: "EUR", quote: "JPY", expectedErr: apperrors.ErrInvalidCurrencyPair},
		{name: "both unsupported", base: "GBP", quote: "JPY", expectedErr: apperrors.ErrInvalidCurrencyPair},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewCurrencyPair(tt.base, tt.quote, supportedCurrencies)

			if tt.expectedErr != nil {
				if !errors.Is(err, tt.expectedErr) {
					t.Fatalf(
						"NewCurrencyPair(%q, %q) error = %v, expected %v",
						tt.base, tt.quote, err, tt.expectedErr,
					)
				}
				return
			}

			if err != nil {
				t.Fatalf(
					"NewCurrencyPair(%q, %q) unexpected error: %v",
					tt.base, tt.quote, err,
				)
			}

			if got != tt.want {
				t.Errorf("NewCurrencyPair() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestCurrencyPairValidate(t *testing.T) {
	// Include malformed codes so membership cannot hide missing format checks.
	supportedCurrencies := map[string]struct{}{
		"USD": {}, "EUR": {}, "MXN": {},
		"": {}, "EU": {}, "EURO": {}, "EU1": {}, "EU!": {},
		"ЕUR": {}, "日": {}, "eur": {}, "eUr": {}, " EUR ": {}, "   ": {},
	}

	tests := []struct {
		name        string
		code        string
		expectedErr error
	}{
		{name: "valid", code: "EUR"},
		{name: "another valid currency", code: "USD"},
		{name: "empty", code: "", expectedErr: apperrors.ErrInvalidCurrencyPair},
		{name: "short", code: "EU", expectedErr: apperrors.ErrInvalidCurrencyPair},
		{name: "long", code: "EURO", expectedErr: apperrors.ErrInvalidCurrencyPair},
		{name: "digit", code: "EU1", expectedErr: apperrors.ErrInvalidCurrencyPair},
		{name: "special character", code: "EU!", expectedErr: apperrors.ErrInvalidCurrencyPair},
		{name: "Cyrillic", code: "ЕUR", expectedErr: apperrors.ErrInvalidCurrencyPair},
		{name: "non Latin three bytes", code: "日", expectedErr: apperrors.ErrInvalidCurrencyPair},
		{name: "lowercase", code: "eur", expectedErr: apperrors.ErrInvalidCurrencyPair},
		{name: "mixed case", code: "eUr", expectedErr: apperrors.ErrInvalidCurrencyPair},
		{name: "surrounding whitespace", code: " EUR ", expectedErr: apperrors.ErrInvalidCurrencyPair},
		{name: "only whitespace", code: "   ", expectedErr: apperrors.ErrInvalidCurrencyPair},
		{name: "identical", code: "MXN", expectedErr: apperrors.ErrInvalidCurrencyPair},
		{name: "unsupported", code: "GBP", expectedErr: apperrors.ErrUnsupportedCurrency},
	}

	for _, tt := range tests {
		for _, field := range []string{"base", "quote"} {
			t.Run(tt.name+"/"+field, func(t *testing.T) {
				pair := CurrencyPair{BaseCurrency: tt.code, QuoteCurrency: "MXN"}

				if field == "quote" {
					pair = CurrencyPair{BaseCurrency: "MXN", QuoteCurrency: tt.code}
				}

				original := pair
				err := pair.Validate(supportedCurrencies)

				if tt.expectedErr != nil {
					if !errors.Is(err, tt.expectedErr) {
						t.Errorf("Validate() error = %v, expected %v", err, tt.expectedErr)
					}
				} else if err != nil {
					t.Errorf("Validate() unexpected error = %v", err)
				}

				if pair != original {
					t.Errorf("Validate() modified pair: got %+v, want %+v", pair, original)
				}
			})
		}
	}
}

func TestCurrencyPairSupportedCurrencies(t *testing.T) {
	pair := CurrencyPair{BaseCurrency: "EUR", QuoteCurrency: "MXN"}

	supportedCurrencies := map[string]struct{}{
		"EUR": {},
		"MXN": {},
	}

	tests := []struct {
		name        string
		supported   map[string]struct{}
		change      func()
		expectedErr error
	}{
		{name: "supported", supported: supportedCurrencies},
		{name: "remove base", supported: supportedCurrencies, change: func() { delete(supportedCurrencies, "EUR") }, expectedErr: apperrors.ErrUnsupportedCurrency},
		{name: "restore base", supported: supportedCurrencies, change: func() { supportedCurrencies["EUR"] = struct{}{} }},
		{name: "remove quote", supported: supportedCurrencies, change: func() { delete(supportedCurrencies, "MXN") }, expectedErr: apperrors.ErrUnsupportedCurrency},
		{name: "restore quote", supported: supportedCurrencies, change: func() { supportedCurrencies["MXN"] = struct{}{} }},
		{name: "both unsupported", supported: map[string]struct{}{"USD": {}}, expectedErr: apperrors.ErrUnsupportedCurrency},
		{name: "empty set", supported: map[string]struct{}{}, expectedErr: apperrors.ErrUnsupportedCurrency},
		{name: "nil set", supported: nil, expectedErr: apperrors.ErrUnsupportedCurrency},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.change != nil {
				tt.change()
			}

			err := pair.Validate(tt.supported)

			if tt.expectedErr != nil {
				if !errors.Is(err, tt.expectedErr) {
					t.Errorf("Validate() error = %v, expected %v", err, tt.expectedErr)
				}
			} else if err != nil {
				t.Errorf("Validate() unexpected error = %v", err)
			}

			got, err := NewCurrencyPair(
				pair.BaseCurrency,
				pair.QuoteCurrency,
				tt.supported,
			)

			if tt.expectedErr != nil {
				if !errors.Is(err, tt.expectedErr) {
					t.Errorf("NewCurrencyPair() error = %v, expected %v", err, tt.expectedErr)
				}
			} else {
				if err != nil {
					t.Fatalf("NewCurrencyPair() unexpected error = %v", err)
				}

				if got != pair {
					t.Errorf("NewCurrencyPair() = %+v, want %+v", got, pair)
				}
			}
		})
	}
}
