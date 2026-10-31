package models

import (
	"fmt"
	"strings"
)

type CurrencyPair struct {
	BaseCurrency  string
	QuoteCurrency string
}

func NewCurrencyPair(base, quote string, supportedCurrencies map[string]struct{}) (CurrencyPair, error) {
	pair := CurrencyPair{
		BaseCurrency:  normalize(base),
		QuoteCurrency: normalize(quote),
	}

	if err := pair.Validate(supportedCurrencies); err != nil {
		return CurrencyPair{}, err
	}

	return pair, nil
}

func (p CurrencyPair) Validate(supportedCurrencies map[string]struct{}) error {
	if p.BaseCurrency == "" || p.QuoteCurrency == "" {
		return fmt.Errorf("both base and quote currencies must be provided")
	}

	if len(p.BaseCurrency) != 3 || len(p.QuoteCurrency) != 3 {
		return fmt.Errorf("currency codes must be 3 characters long")
	}

	if !containsOnlyLatinLetters(p.BaseCurrency) || !containsOnlyLatinLetters(p.QuoteCurrency) {
		return fmt.Errorf("currency codes must contain only uppercase Latin letters")
	}

	if p.BaseCurrency == p.QuoteCurrency {
		return fmt.Errorf("base and quote currencies must be different")
	}

	if _, ok := supportedCurrencies[p.BaseCurrency]; !ok {
		return fmt.Errorf("unsupported currency: %s", p.BaseCurrency)
	}

	if _, ok := supportedCurrencies[p.QuoteCurrency]; !ok {
		return fmt.Errorf("unsupported currency: %s", p.QuoteCurrency)
	}

	return nil
}

func normalize(code string) string {
	return strings.ToUpper(strings.TrimSpace(code))
}

func containsOnlyLatinLetters(code string) bool {
	for _, c := range code {
		if c < 'A' || c > 'Z' {
			return false
		}
	}

	return true
}
