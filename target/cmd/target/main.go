package main

import (
	"encoding/json"
	"log"
	"math/rand"
	"net/http"
	"time"
)

func main() {
	mux := http.NewServeMux()

	mux.HandleFunc("/health", healthHandler)
	mux.HandleFunc("/fast", fastHandler)
	mux.HandleFunc("/slow", slowHandler)
	mux.HandleFunc("/error", errorHandler)
	mux.HandleFunc("/random", randomHandler)

	server := &http.Server{
		Addr:         ":8081",
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
		IdleTimeout:  30 * time.Second,
	}

	log.Println("target service listening on :8081")

	if err := server.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"service": "target",
		"status":  "ok",
	})
}

func fastHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"message": "fast response",
	})
}

func slowHandler(w http.ResponseWriter, r *http.Request) {
	time.Sleep(500 * time.Millisecond)

	writeJSON(w, http.StatusOK, map[string]string{
		"message": "slow response",
	})
}

func errorHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusInternalServerError, map[string]string{
		"error": "intentional error response",
	})
}

func randomHandler(w http.ResponseWriter, r *http.Request) {
	switch rand.Intn(3) {
	case 0:
		fastHandler(w, r)
	case 1:
		slowHandler(w, r)
	default:
		errorHandler(w, r)
	}
}

func writeJSON(w http.ResponseWriter, statusCode int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("failed to write JSON response: %v", err)
	}
}
