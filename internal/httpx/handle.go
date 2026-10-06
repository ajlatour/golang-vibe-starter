package httpx

import (
	"errors"
	"log/slog"
	"net/http"
)

// HandlerFunc is a handler that returns its error instead of writing it:
// Handle turns the error into the response, so every handler answers its
// errors the same way, and none repeats "write it, then return".
type HandlerFunc func(w http.ResponseWriter, r *http.Request) error

// Error is an error that says what response it means: its status, its code
// and a message safe to show the client. The client's mistakes (a body that
// won't decode, an unknown ID) are Errors; a failure of the service or its
// dependencies isn't, and Handle hides its details.
type Error struct {
	Status  int
	Code    Code
	Message string
	// Err is the cause, if any, for errors.Is and errors.As; it's never
	// sent to the client.
	Err error
}

// NewError returns an Error answered with status, code and msg. An Error
// with a fixed message can be made once, as a package variable, and returned
// as often as it's needed: Handle only reads it.
func NewError(status int, code Code, msg string) *Error {
	return &Error{Status: status, Code: code, Message: msg}
}

func (e *Error) Error() string {
	if e.Err != nil {
		return e.Message + ": " + e.Err.Error()
	}
	return e.Message
}

func (e *Error) Unwrap() error { return e.Err }

// Handle adapts h to an http.Handler, answering the error it returns:
//
//   - nil: h wrote its response.
//   - an *Error anywhere in the chain: that status, code and message
//     (WriteError), the client's mistake, so not logged as a failure.
//   - anything else: ServerError, which logs it and answers 500, or the 502
//     or 504 a StatusError in the chain asks for, or 499 when the client
//     left.
func Handle(logger *slog.Logger, h HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := h(w, r)
		if err == nil {
			return
		}
		if e, ok := asError(err); ok {
			WriteError(w, e.Status, e.Code, e.Message)
			return
		}
		ServerError(logger, w, r, err)
	})
}

// asError finds the *Error in err's chain. One returned as it is, the usual
// case, is found without errors.As, which allocates; that's in a function
// of its own (wrappedError) because the variable it takes the address of
// would otherwise be allocated on every call, found here or not.
func asError(err error) (*Error, bool) {
	if e, ok := err.(*Error); ok {
		return e, true
	}
	return wrappedError(err)
}

func wrappedError(err error) (*Error, bool) {
	var e *Error
	ok := errors.As(err, &e)
	return e, ok
}
