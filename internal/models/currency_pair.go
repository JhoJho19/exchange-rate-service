package models

import (
	"fmt"
	"strings"

	"github.com/JhoJho19/exchange-rate-service/internal/apperrors"
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
		return fmt.Errorf("%w: both base and quote currencies must be provided", apperrors.ErrInvalidCurrencyPair)
	}

	if len(p.BaseCurrency) != 3 || len(p.QuoteCurrency) != 3 {
		return fmt.Errorf("%w: currency codes must be 3 characters long", apperrors.ErrInvalidCurrencyPair)
	}

	if !containsOnlyLatinLetters(p.BaseCurrency) || !containsOnlyLatinLetters(p.QuoteCurrency) {
		return fmt.Errorf("%w: currency codes must contain only uppercase Latin letters", apperrors.ErrInvalidCurrencyPair)
	}

	if p.BaseCurrency == p.QuoteCurrency {
		return fmt.Errorf("%w: base and quote currencies must be different", apperrors.ErrInvalidCurrencyPair)
	}

	if _, ok := supportedCurrencies[p.BaseCurrency]; !ok {
		return fmt.Errorf("%w %s", apperrors.ErrUnsupportedCurrency, p.BaseCurrency)
	}

	if _, ok := supportedCurrencies[p.QuoteCurrency]; !ok {
		return fmt.Errorf("%w %s", apperrors.ErrUnsupportedCurrency, p.QuoteCurrency)
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
