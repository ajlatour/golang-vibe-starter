package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
)

// MaxBodyBytes is the largest request body DecodeJSON accepts.
const MaxBodyBytes = 1 << 20 // 1 MiB

// Healthz is a liveness handler: it reports ok whenever the process can
// serve HTTP.
func Healthz(w http.ResponseWriter, r *http.Request) {
	WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// WriteJSON sends v as a JSON response with the given status.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// The status is already sent, so an encode error can't be reported to
	// the client; it will show up as a short body.
	_ = json.NewEncoder(w).Encode(v)
}

// WriteError sends {"error": msg} with the given status.
func WriteError(w http.ResponseWriter, status int, msg string) {
	WriteJSON(w, status, map[string]string{"error": msg})
}

// ServerError logs err and sends a generic 500 so internals aren't leaked.
func ServerError(logger *slog.Logger, w http.ResponseWriter, r *http.Request, err error) {
	logger.ErrorContext(r.Context(), "internal error",
		"method", r.Method,
		"path", r.URL.Path,
		"err", err,
	)
	WriteError(w, http.StatusInternalServerError, "internal server error")
}

// DecodeJSON decodes a single JSON object from the request body into v,
// rejecting unknown fields, trailing data, and bodies over MaxBodyBytes.
// The returned error is safe to show to the client.
func DecodeJSON(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
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
