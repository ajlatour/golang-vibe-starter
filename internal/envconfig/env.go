package envconfig

import (
	"errors"
	"fmt"
)

// Env reads configuration from environment variables and collects every
// problem it finds, so a service reports all bad settings in one failed start
// instead of one per attempt. Read the settings with Optional, Required and
// String, then check Err once.
//
// An empty variable counts as unset.
type Env struct {
	getenv func(string) string
	errs   []error
}

// NewEnv returns an Env that reads variables with getenv, normally os.Getenv.
// Tests pass a fake, so they never touch the real environment.
func NewEnv(getenv func(string) string) *Env {
	return &Env{getenv: getenv}
}

// String returns the variable's value, or "" if it is unset.
func (e *Env) String(name string) string {
	return e.getenv(name)
}

// Err returns every problem recorded so far, each prefixed with its variable's
// name, or nil.
func (e *Env) Err() error {
	return errors.Join(e.errs...)
}

func (e *Env) fail(name string, err error) {
	e.errs = append(e.errs, fmt.Errorf("%s: %w", name, err))
}

// Optional returns the variable parsed by parse, or def if it is unset. A
// value that doesn't parse is recorded as an error on e, and def is returned.
func Optional[T any](e *Env, name string, def T, parse func(string) (T, error)) T {
	v := e.getenv(name)
	if v == "" {
		return def
	}
	t, err := parse(v)
	if err != nil {
		e.fail(name, err)
		return def
	}
	return t
}

// Required is Optional for a variable with no sensible default: unset is an
// error recorded on e, and the zero value is returned.
func Required[T any](e *Env, name string, parse func(string) (T, error)) T {
	var zero T
	if e.getenv(name) == "" {
		e.fail(name, errors.New("required"))
		return zero
	}
	return Optional(e, name, zero, parse)
}
