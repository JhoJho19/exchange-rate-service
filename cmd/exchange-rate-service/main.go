package main

import (
	"log"

	"github.com/JhoJho19/exchange-rate-service/internal/config"
)

func main() {
	config, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	log.Println("Configuration loaded successfully")
	_ = config
}
