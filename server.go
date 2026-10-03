package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
)

// newServer builds the service's root handler: routes plus middleware.
func newServer(logger *slog.Logger, items ItemStore) http.Handler {
	mux := http.NewServeMux()
	addRoutes(mux, logger, items)

	var h http.Handler = mux
	h = recoverPanics(logger, h)
	h = logRequests(logger, h)
	return h
}

// addRoutes is the single place every route is registered.
func addRoutes(mux *http.ServeMux, logger *slog.Logger, items ItemStore) {
	mux.HandleFunc("GET /healthz", handleHealthz)

	mux.Handle("GET /items", handleListItems(logger, items))
	mux.Handle("POST /items", handleCreateItem(logger, items))
	mux.Handle("GET /items/{id}", handleGetItem(logger, items))
	mux.Handle("PUT /items/{id}", handleUpdateItem(logger, items))
	mux.Handle("DELETE /items/{id}", handleDeleteItem(logger, items))
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

// serverError logs err and sends a generic 500 so internals aren't leaked.
func serverError(logger *slog.Logger, w http.ResponseWriter, r *http.Request, err error) {
	logger.ErrorContext(r.Context(), "internal error",
		"method", r.Method,
		"path", r.URL.Path,
		"err", err,
	)
	writeError(w, http.StatusInternalServerError, "internal server error")
}

const maxBodyBytes = 1 << 20 // 1 MiB

// decodeJSON decodes a single JSON object from the request body into v,
// rejecting unknown fields, trailing data, and bodies over maxBodyBytes.
// The returned error is safe to show to the client.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var maxErr *http.MaxBytesError
		switch {
		case errors.Is(err, io.EOF):
			return errors.New("request body is empty")
		case errors.As(err, &maxErr):
			return fmt.Errorf("request body exceeds %d bytes", maxErr.Limit)
		default:
			return fmt.Errorf("invalid JSON: %w", err)
		}
	}
	if dec.Decode(&struct{}{}) != io.EOF {
		return errors.New("request body must contain a single JSON object")
	}
	return nil
}
