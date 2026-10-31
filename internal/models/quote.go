package models

import "time"

type Quote struct {
	Pair      CurrencyPair
	Rate      float64
	UpdatedAt time.Time
}
