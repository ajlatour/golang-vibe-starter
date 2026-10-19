package telemetry

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// exporter sends spans over OTLP/HTTP, encoded as JSON, which OTLP allows
// beside protobuf (https://opentelemetry.io/docs/specs/otlp/#otlphttp): the
// SDK's own exporter encodes protobuf, and brings gRPC and protobuf's
// modules with it, doubling the binary even with tracing off. The
// OpenTelemetry Collector, Jaeger and most backends take JSON; one that
// takes only protobuf can sit behind a Collector.
type exporter struct {
	url     string
	headers http.Header
	client  *http.Client
}

var _ sdktrace.SpanExporter = (*exporter)(nil)

// newExporter exports to endpoint, the full URL, with the headers
// OTEL_EXPORTER_OTLP_HEADERS and OTEL_EXPORTER_OTLP_TRACES_HEADERS give,
// each export within OTEL_EXPORTER_OTLP_TIMEOUT (10s by default).
func newExporter(endpoint string, getenv func(string) string) (*exporter, error) {
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("telemetry: endpoint %q isn't an http or https URL", endpoint)
	}
	headers := http.Header{}
	for _, name := range []string{"OTEL_EXPORTER_OTLP_HEADERS", "OTEL_EXPORTER_OTLP_TRACES_HEADERS"} {
		if err := parseHeaders(headers, getenv(name)); err != nil {
			return nil, fmt.Errorf("telemetry: %s: %w", name, err)
		}
	}
	timeout := 10 * time.Second
	for _, name := range []string{"OTEL_EXPORTER_OTLP_TIMEOUT", "OTEL_EXPORTER_OTLP_TRACES_TIMEOUT"} {
		if v := getenv(name); v != "" {
			ms, err := strconv.Atoi(v)
			if err != nil || ms <= 0 {
				return nil, fmt.Errorf("telemetry: %s: %q isn't a positive number of milliseconds", name, v)
			}
			timeout = time.Duration(ms) * time.Millisecond
		}
	}
	// No proxy from the environment, and no redirects followed, as for the
	// service's other calls (ADR-0019).
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &exporter{url: u.String(), headers: headers, client: &http.Client{
		Timeout:       timeout,
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

// parseHeaders adds the headers in s, "name=value,name=value" with the
// values URL-encoded, as OpenTelemetry specifies, to h.
func parseHeaders(h http.Header, s string) error {
	for pair := range strings.SplitSeq(s, ",") {
		if strings.TrimSpace(pair) == "" {
			continue
		}
		name, value, ok := strings.Cut(pair, "=")
		name = strings.TrimSpace(name)
		if !ok || name == "" {
			return errors.New("want name=value pairs, separated by commas")
		}
		v, err := url.QueryUnescape(strings.TrimSpace(value))
		if err != nil {
			return fmt.Errorf("the value of %s: %w", name, err)
		}
		h.Set(name, v)
	}
	return nil
}

func (e *exporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	if len(spans) == 0 {
		return nil
	}
	body, err := json.Marshal(encode(spans))
	if err != nil {
		return fmt.Errorf("telemetry: encode spans: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("telemetry: %w", err)
	}
	maps.Copy(req.Header, e.headers)
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.client.Do(req)
	if err != nil {
		if ue, ok := errors.AsType[*url.Error](err); ok {
			err = ue.Err // without the URL, which may carry credentials
		}
		return fmt.Errorf("telemetry: export %d spans: %w", len(spans), err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10)) // so the connection is reused
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("telemetry: export %d spans: the collector answered %d", len(spans), resp.StatusCode)
	}
	return nil
}

func (e *exporter) Shutdown(context.Context) error {
	e.client.CloseIdleConnections()
	return nil
}

// The OTLP JSON encoding (opentelemetry-proto's ExportTraceServiceRequest):
// IDs in hex, 64-bit integers as strings, enums as numbers.
type (
	otlpRequest struct {
		ResourceSpans []otlpResourceSpans `json:"resourceSpans"`
	}
	otlpResourceSpans struct {
		Resource   otlpResource     `json:"resource"`
		ScopeSpans []otlpScopeSpans `json:"scopeSpans"`
	}
	otlpResource struct {
		Attributes []otlpKeyValue `json:"attributes"`
	}
	otlpScopeSpans struct {
		Scope otlpScope  `json:"scope"`
		Spans []otlpSpan `json:"spans"`
	}
	otlpScope struct {
		Name    string `json:"name"`
		Version string `json:"version,omitempty"`
	}
	otlpSpan struct {
		TraceID           string         `json:"traceId"`
		SpanID            string         `json:"spanId"`
		ParentSpanID      string         `json:"parentSpanId,omitempty"`
		Name              string         `json:"name"`
		Kind              int            `json:"kind"`
		StartTimeUnixNano string         `json:"startTimeUnixNano"`
		EndTimeUnixNano   string         `json:"endTimeUnixNano"`
		Attributes        []otlpKeyValue `json:"attributes,omitempty"`
		Events            []otlpEvent    `json:"events,omitempty"`
		Status            otlpStatus     `json:"status"`
	}
	otlpEvent struct {
		TimeUnixNano string         `json:"timeUnixNano"`
		Name         string         `json:"name"`
		Attributes   []otlpKeyValue `json:"attributes,omitempty"`
	}
	otlpStatus struct {
		Code    int    `json:"code,omitempty"`
		Message string `json:"message,omitempty"`
	}
	otlpKeyValue struct {
		Key   string       `json:"key"`
		Value otlpAnyValue `json:"value"`
	}
	otlpAnyValue struct {
		StringValue *string         `json:"stringValue,omitempty"`
		BoolValue   *bool           `json:"boolValue,omitempty"`
		IntValue    *string         `json:"intValue,omitempty"`
		DoubleValue *float64        `json:"doubleValue,omitempty"`
		ArrayValue  *otlpArrayValue `json:"arrayValue,omitempty"`
	}
	otlpArrayValue struct {
		Values []otlpAnyValue `json:"values"`
	}
)

// encode groups spans by their resource, then by the scope that made them,
// as OTLP sends them.
func encode(spans []sdktrace.ReadOnlySpan) otlpRequest {
	var req otlpRequest
	resources := map[attribute.Distinct]int{}
	scopes := map[[2]int]int{} // resource index, scope key → scope index
	scopeKeys := map[string]int{}
	for _, s := range spans {
		res := s.Resource()
		var key attribute.Distinct
		var attrs []attribute.KeyValue
		if res != nil {
			key, attrs = res.Equivalent(), res.Attributes()
		}
		ri, ok := resources[key]
		if !ok {
			ri = len(req.ResourceSpans)
			resources[key] = ri
			req.ResourceSpans = append(req.ResourceSpans, otlpResourceSpans{Resource: otlpResource{Attributes: keyValues(attrs)}})
		}
		scope := s.InstrumentationScope()
		sk, ok := scopeKeys[scope.Name+"\x00"+scope.Version]
		if !ok {
			sk = len(scopeKeys)
			scopeKeys[scope.Name+"\x00"+scope.Version] = sk
		}
		si, ok := scopes[[2]int{ri, sk}]
		rs := &req.ResourceSpans[ri]
		if !ok {
			si = len(rs.ScopeSpans)
			scopes[[2]int{ri, sk}] = si
			rs.ScopeSpans = append(rs.ScopeSpans, otlpScopeSpans{Scope: otlpScope{Name: scope.Name, Version: scope.Version}})
		}
		rs.ScopeSpans[si].Spans = append(rs.ScopeSpans[si].Spans, encodeSpan(s))
	}
	return req
}

func encodeSpan(s sdktrace.ReadOnlySpan) otlpSpan {
	sc := s.SpanContext()
	traceID, spanID := sc.TraceID(), sc.SpanID()
	out := otlpSpan{
		TraceID:           hex.EncodeToString(traceID[:]),
		SpanID:            hex.EncodeToString(spanID[:]),
		Name:              s.Name(),
		Kind:              int(s.SpanKind()), // the SDK's numbering is OTLP's
		StartTimeUnixNano: strconv.FormatInt(s.StartTime().UnixNano(), 10),
		EndTimeUnixNano:   strconv.FormatInt(s.EndTime().UnixNano(), 10),
		Attributes:        keyValues(s.Attributes()),
	}
	if parent := s.Parent().SpanID(); parent.IsValid() {
		out.ParentSpanID = hex.EncodeToString(parent[:])
	}
	for _, e := range s.Events() {
		out.Events = append(out.Events, otlpEvent{
			TimeUnixNano: strconv.FormatInt(e.Time.UnixNano(), 10),
			Name:         e.Name,
			Attributes:   keyValues(e.Attributes),
		})
	}
	// OTLP's codes: unset 0, ok 1, error 2; the SDK's: unset 0, error 1, ok 2.
	switch st := s.Status(); st.Code {
	case codes.Error:
		out.Status = otlpStatus{Code: 2, Message: st.Description}
	case codes.Ok:
		out.Status = otlpStatus{Code: 1}
	}
	return out
}

func keyValues(attrs []attribute.KeyValue) []otlpKeyValue {
	out := make([]otlpKeyValue, 0, len(attrs))
	for _, kv := range attrs {
		out = append(out, otlpKeyValue{Key: string(kv.Key), Value: anyValue(kv.Value)})
	}
	return out
}

func anyValue(v attribute.Value) otlpAnyValue {
	switch v.Type() {
	case attribute.BOOL:
		b := v.AsBool()
		return otlpAnyValue{BoolValue: &b}
	case attribute.INT64:
		s := strconv.FormatInt(v.AsInt64(), 10)
		return otlpAnyValue{IntValue: &s}
	case attribute.FLOAT64:
		f := v.AsFloat64()
		return otlpAnyValue{DoubleValue: &f}
	case attribute.BOOLSLICE:
		return array(v.AsBoolSlice(), attribute.BoolValue)
	case attribute.INT64SLICE:
		return array(v.AsInt64Slice(), attribute.Int64Value)
	case attribute.FLOAT64SLICE:
		return array(v.AsFloat64Slice(), attribute.Float64Value)
	case attribute.STRINGSLICE:
		return array(v.AsStringSlice(), attribute.StringValue)
	}
	s := v.String()
	return otlpAnyValue{StringValue: &s}
}

func array[T any](values []T, of func(T) attribute.Value) otlpAnyValue {
	a := &otlpArrayValue{Values: make([]otlpAnyValue, 0, len(values))}
	for _, x := range values {
		a.Values = append(a.Values, anyValue(of(x)))
	}
	return otlpAnyValue{ArrayValue: a}
}

// environ is the process's environment, for the exporter's settings.
var environ = os.Getenv
