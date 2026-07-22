package handlers

import (
	"encoding/json"
	"log"
	"net/http"
)

// writeJSON sets the content type, writes the status code, then encodes payload.
// Mirrors the helper in the target service.
func writeJSON(w http.ResponseWriter, statusCode int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("failed to write JSON response: %v", err)
	}
}

// writeError sends a JSON error body of the shape {"error": "..."} with the
// given status code, so clients get a consistent error format.
func writeError(w http.ResponseWriter, statusCode int, message string) {
	writeJSON(w, statusCode, map[string]string{"error": message})
}
