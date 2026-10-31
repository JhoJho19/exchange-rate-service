package models

import "testing"

func TestNewCurrencyPair(t *testing.T) {
	supportedCurrencies := map[string]struct{}{
		"USD": {},
		"EUR": {},
		"MXN": {},
	}

	tests := []struct {
		name    string
		base    string
		quote   string
		want    CurrencyPair
		wantErr bool
	}{
		{name: "uppercase", base: "EUR", quote: "MXN", want: CurrencyPair{"EUR", "MXN"}},
		{name: "lowercase", base: "eur", quote: "mxn", want: CurrencyPair{"EUR", "MXN"}},
		{name: "whitespace", base: " \tEUR\n", quote: "\rMXN ", want: CurrencyPair{"EUR", "MXN"}},
		{name: "mixed case and whitespace", base: " eUr\t", quote: "\nmXn ", want: CurrencyPair{"EUR", "MXN"}},
		{name: "unicode whitespace", base: "\u00a0EUR\u2003", quote: "\u2003MXN\u00a0", want: CurrencyPair{"EUR", "MXN"}},
		{name: "USD EUR", base: "USD", quote: "EUR", want: CurrencyPair{"USD", "EUR"}},
		{name: "MXN USD", base: "MXN", quote: "USD", want: CurrencyPair{"MXN", "USD"}},
		{name: "empty base", quote: "MXN", wantErr: true},
		{name: "empty quote", base: "EUR", wantErr: true},
		{name: "both empty", wantErr: true},
		{name: "whitespace base", base: " \t\n", quote: "MXN", wantErr: true},
		{name: "whitespace quote", base: "EUR", quote: " \t\n", wantErr: true},
		{name: "both whitespace", base: " \t", quote: "\n ", wantErr: true},
		{name: "short base", base: "EU", quote: "MXN", wantErr: true},
		{name: "short quote", base: "EUR", quote: "MX", wantErr: true},
		{name: "long base", base: "EURO", quote: "MXN", wantErr: true},
		{name: "long quote", base: "EUR", quote: "MXNN", wantErr: true},
		{name: "digit base", base: "EU1", quote: "MXN", wantErr: true},
		{name: "digit quote", base: "EUR", quote: "MX1", wantErr: true},
		{name: "special base", base: "EU!", quote: "MXN", wantErr: true},
		{name: "special quote", base: "EUR", quote: "MX!", wantErr: true},
		{name: "internal space base", base: "E R", quote: "MXN", wantErr: true},
		{name: "internal space quote", base: "EUR", quote: "M N", wantErr: true},
		{name: "Cyrillic base", base: "ЕUR", quote: "MXN", wantErr: true},
		{name: "Cyrillic quote", base: "EUR", quote: "МXN", wantErr: true},
		{name: "non Latin base", base: "日", quote: "MXN", wantErr: true},
		{name: "non Latin quote", base: "EUR", quote: "日", wantErr: true},
		{name: "identical", base: "EUR", quote: "EUR", wantErr: true},
		{name: "identical different case", base: "eur", quote: "EUR", wantErr: true},
		{name: "identical after trimming", base: " eUr ", quote: "EUR\t", wantErr: true},
		{name: "unsupported base", base: "GBP", quote: "MXN", wantErr: true},
		{name: "unsupported quote", base: "EUR", quote: "JPY", wantErr: true},
		{name: "both unsupported", base: "GBP", quote: "JPY", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewCurrencyPair(tt.base, tt.quote, supportedCurrencies)
			if (err != nil) != tt.wantErr {
				t.Fatalf("NewCurrencyPair(%q, %q) error = %v, wantErr %v", tt.base, tt.quote, err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
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
		name    string
		code    string
		wantErr bool
	}{
		{name: "valid", code: "EUR"},
		{name: "another valid currency", code: "USD"},
		{name: "empty", code: "", wantErr: true},
		{name: "short", code: "EU", wantErr: true},
		{name: "long", code: "EURO", wantErr: true},
		{name: "digit", code: "EU1", wantErr: true},
		{name: "special character", code: "EU!", wantErr: true},
		{name: "Cyrillic", code: "ЕUR", wantErr: true},
		{name: "non Latin three bytes", code: "日", wantErr: true},
		{name: "lowercase", code: "eur", wantErr: true},
		{name: "mixed case", code: "eUr", wantErr: true},
		{name: "surrounding whitespace", code: " EUR ", wantErr: true},
		{name: "only whitespace", code: "   ", wantErr: true},
		{name: "identical", code: "MXN", wantErr: true},
		{name: "unsupported", code: "GBP", wantErr: true},
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
				if (err != nil) != tt.wantErr {
					t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
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
	supportedCurrencies := map[string]struct{}{"EUR": {}, "MXN": {}}
	tests := []struct {
		name      string
		supported map[string]struct{}
		change    func()
		wantErr   bool
	}{
		{name: "supported", supported: supportedCurrencies},
		{name: "remove base", supported: supportedCurrencies, change: func() { delete(supportedCurrencies, "EUR") }, wantErr: true},
		{name: "restore base", supported: supportedCurrencies, change: func() { supportedCurrencies["EUR"] = struct{}{} }},
		{name: "remove quote", supported: supportedCurrencies, change: func() { delete(supportedCurrencies, "MXN") }, wantErr: true},
		{name: "restore quote", supported: supportedCurrencies, change: func() { supportedCurrencies["MXN"] = struct{}{} }},
		{name: "both unsupported", supported: map[string]struct{}{"USD": {}}, wantErr: true},
		{name: "empty set", supported: map[string]struct{}{}, wantErr: true},
		{name: "nil set", supported: nil, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.change != nil {
				tt.change()
			}
			if err := pair.Validate(tt.supported); (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			got, err := NewCurrencyPair(pair.BaseCurrency, pair.QuoteCurrency, tt.supported)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewCurrencyPair() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && got != pair {
				t.Errorf("NewCurrencyPair() = %+v, want %+v", got, pair)
			}
		})
	}
}
