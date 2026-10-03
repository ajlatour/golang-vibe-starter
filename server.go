package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// newServer builds the service's root handler: routes plus middleware.
func newServer(logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	addRoutes(mux)

	var h http.Handler = mux
	h = recoverPanics(logger, h)
	h = logRequests(logger, h)
	return h
}

// addRoutes is the single place every route is registered.
func addRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", handleHealthz)
}

func handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// The status is already sent, so an encode error can't be reported to
	// the client; it will show up as a short body.
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
