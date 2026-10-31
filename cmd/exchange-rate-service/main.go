package main

import (
	"log"

	"github.com/JhoJho19/exchange-rate-service/internal/config"
	"github.com/JhoJho19/exchange-rate-service/internal/provider/exchangeratedev"
	"github.com/JhoJho19/exchange-rate-service/internal/service"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	log.Println("Configuration loaded successfully")

	var rateProvider service.RateProvider = exchangeratedev.NewClient(
		cfg.RateProviderAPIKey,
		cfg.RateProviderURL,
		cfg.RateProviderTimeout,
	)

	_ = rateProvider
}
