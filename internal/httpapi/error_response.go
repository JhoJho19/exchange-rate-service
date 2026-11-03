package httpapi

import (
	"encoding/json"
	"log"
	"net/http"
)

// writeJSONError writes a client-safe message supplied by the caller.
// Once headers are committed, write failures can only be logged.
func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(struct {
		Error string `json:"error"`
	}{Error: message}); err != nil {
		log.Printf("Failed to write JSON error response: %v", err)
	}
}
