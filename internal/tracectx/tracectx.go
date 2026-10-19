// Package tracectx carries W3C Trace Context (https://www.w3.org/TR/trace-context/)
// through the service without a tracing SDK: it reads the traceparent header
// a request arrives with, or starts a trace when there isn't one, and gives
// every call the service makes onward a traceparent of its own, in the same
// trace. So the service's calls join a caller's trace today, and when an
// OpenTelemetry SDK is added, its spans join the traces already flowing.
//
// It records nothing itself: no spans, no timings. The trace ID goes into the
// request's log lines (trace_id), which ties them to the trace. Spans is how
// something that does record them is told of each span: the request's own,
// and each call it makes. The service runs without one, recording none; a
// tracing SDK's adapter is one.
package tracectx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
)

// Header is the W3C header that carries a trace context.
const Header = "traceparent"

// Parent is a trace context: the trace a request belongs to, the span that
// sent it (the parent of any span this service makes; none, all zeros, for a
// trace this service started), and the trace flags.
type Parent struct {
	TraceID [16]byte
	SpanID  [8]byte
	Flags   byte
}

// Parse reads a traceparent header, version 00:
// "00-<32 hex trace ID>-<16 hex span ID>-<2 hex flags>". It reports false
// for anything else, an all-zero trace or span ID included, as the spec
// says to treat them: such a request starts a trace of its own.
func Parse(h string) (Parent, bool) {
	var p Parent
	if len(h) != 55 || h[:3] != "00-" || h[35] != '-' || h[52] != '-' {
		return Parent{}, false
	}
	var flags [1]byte
	if !decode(p.TraceID[:], h[3:35]) || !decode(p.SpanID[:], h[36:52]) || !decode(flags[:], h[53:55]) {
		return Parent{}, false
	}
	p.Flags = flags[0]
	if p.TraceID == ([16]byte{}) || p.SpanID == ([8]byte{}) {
		return Parent{}, false
	}
	return p, true
}

// decode decodes lower-case hex only, as the spec requires.
func decode(dst []byte, s string) bool {
	for _, c := range []byte(s) {
		if ('0' > c || c > '9') && ('a' > c || c > 'f') {
			return false
		}
	}
	_, err := hex.Decode(dst, []byte(s))
	return err == nil
}

// New starts a trace: a random trace ID, and no parent span, since the
// trace starts here. It isn't sampled: nothing here records it. Its calls
// onward still each get a span ID of their own (Child).
func New() Parent {
	var p Parent
	_, _ = rand.Read(p.TraceID[:])
	return p
}

// IsRoot reports whether p is a trace this service started: it has no
// parent span.
func (p Parent) IsRoot() bool { return p.SpanID == [8]byte{} }

// Child returns the context for a call this service makes within p's trace:
// the same trace and flags, with a new span ID for the call.
func (p Parent) Child() Parent {
	c := p
	_, _ = rand.Read(c.SpanID[:])
	return c
}

// String is p as a traceparent header.
func (p Parent) String() string {
	var b [55]byte
	copy(b[:], "00-")
	hex.Encode(b[3:35], p.TraceID[:])
	b[35] = '-'
	hex.Encode(b[36:52], p.SpanID[:])
	b[52] = '-'
	hex.Encode(b[53:55], []byte{p.Flags})
	return string(b[:])
}

// TraceIDString is p's trace ID in hex, as logged.
func (p Parent) TraceIDString() string { return hex.EncodeToString(p.TraceID[:]) }

// Spans records the spans of a trace: the request's own, as the service
// serves it, and each call it makes onward. Start starts one and returns its
// context, which is what its children and the calls it makes carry: the same
// trace, the span's own ID, and whether it's sampled. end ends it.
//
// A Spans must keep the trace ID it's given, a root span's included, so the
// trace_id logged is the trace's. And it must be cheap: Start is on every
// request's path.
type Spans interface {
	Start(ctx context.Context, s Span) (Parent, func(End))
}

// Kind says which side of a call a span is.
type Kind uint8

const (
	// Server is a request this service serves.
	Server Kind = iota + 1
	// Client is a call this service makes.
	Client
)

// Span is a span as it starts.
type Span struct {
	Kind Kind
	// Name says what it is: for a server span, the method, the route being
	// known only at its end; for a client span, the call's label, such as
	// "upstream GET /items/widget", which is safe to log.
	Name   string
	Method string
	// Parent is its parent's context: the caller's span, or for a root span
	// (Parent.IsRoot) only the trace's ID.
	Parent Parent
}

// End is how a span ended.
type End struct {
	// Status is the response's: the one the service sent, or got (0 if none
	// arrived).
	Status int
	// Route is a server span's route pattern, such as "GET /items/{id}" (""
	// if none matched).
	Route string
	// Err is why a client span's call failed, if it did.
	Err error
}
