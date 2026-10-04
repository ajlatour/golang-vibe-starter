package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
)

// MaxBodyBytes is the largest request body DecodeJSON accepts.
const MaxBodyBytes = 1 << 20 // 1 MiB

// HealthzPath is where services serve Healthz and where Probe looks.
const HealthzPath = "/healthz"

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
// If err (or an error it wraps) is a LogAttrsError, its fields are logged too.
// If err is only because the client went away (the request's context was
// cancelled), that's not a server fault: it is logged at info as
// "client disconnected" and answered with 499 instead.
func ServerError(logger *slog.Logger, w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(r.Context().Err(), context.Canceled) && errors.Is(err, context.Canceled) {
		logger.LogAttrs(r.Context(), slog.LevelInfo, "client disconnected",
			append(requestAttrs(r), errorAttrs(err)...)...,
		)
		WriteError(w, StatusClientClosedRequest, "client closed request")
		return
	}
	logger.LogAttrs(r.Context(), slog.LevelError, "internal error",
		append(requestAttrs(r), errorAttrs(err)...)...,
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
