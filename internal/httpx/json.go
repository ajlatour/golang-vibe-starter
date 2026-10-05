package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"strings"

	"github.com/ajlatour/golang-vibe-starter/internal/logx"
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

// StatusError is implemented by errors that map to a specific 5xx status for
// the client, such as a failed upstream call (502 or 504). ServerError sends
// that status with the standard reason phrase as the message, so nothing
// internal is exposed.
type StatusError interface {
	error
	HTTPStatus() int
}

// ServerError logs err and sends a 5xx so internals aren't leaked: a generic
// 500, or the status of the first StatusError in err's chain.
// If err (or an error it wraps) is a logx.AttrsError, its fields are logged too.
// If err is only because the client went away (the request's context was
// cancelled), that's not a server fault: it is logged at info as
// "client disconnected" and answered with 499 instead.
func ServerError(logger *slog.Logger, w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(r.Context().Err(), context.Canceled) && errors.Is(err, context.Canceled) {
		logger.LogAttrs(r.Context(), slog.LevelInfo, "client disconnected",
			append(requestAttrs(r), logx.ErrorAttrs(err)...)...,
		)
		WriteError(w, StatusClientClosedRequest, "client closed request")
		return
	}
	logger.LogAttrs(r.Context(), slog.LevelError, "internal error",
		append(requestAttrs(r), logx.ErrorAttrs(err)...)...,
	)
	status, msg := http.StatusInternalServerError, "internal server error"
	var se StatusError
	if errors.As(err, &se) {
		status = se.HTTPStatus()
		msg = strings.ToLower(http.StatusText(status))
	}
	WriteError(w, status, msg)
}

// jsonKind describes the JSON value a Go kind is decoded from.
func jsonKind(k reflect.Kind) string {
	switch k {
	case reflect.String:
		return "a string"
	case reflect.Bool:
		return "true or false"
	case reflect.Slice, reflect.Array:
		return "an array"
	case reflect.Struct, reflect.Map:
		return "an object"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return "a number"
	default:
		return "a different type"
	}
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
		var typeErr *json.UnmarshalTypeError
		switch {
		case errors.Is(err, io.EOF):
			return errors.New("request body is empty")
		case errors.As(err, &maxErr):
			return fmt.Errorf("request body exceeds %d bytes", maxErr.Limit)
		case errors.As(err, &typeErr):
			// encoding/json's own message names the Go type it was
			// decoding into ("Go struct field itemInput.name"); say it in
			// the client's terms instead.
			if typeErr.Field == "" {
				return fmt.Errorf("invalid JSON: the body must be %s", jsonKind(reflect.Struct))
			}
			return fmt.Errorf("invalid JSON: field %q must be %s", typeErr.Field, jsonKind(typeErr.Type.Kind()))
		default:
			// Syntax errors and unknown fields, which name only the input.
			return fmt.Errorf("invalid JSON: %s", strings.TrimPrefix(err.Error(), "json: "))
		}
	}
	if dec.Decode(&struct{}{}) != io.EOF {
		return errors.New("request body must contain a single JSON object")
	}
	return nil
}
