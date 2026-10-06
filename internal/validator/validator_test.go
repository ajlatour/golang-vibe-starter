package validator_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/logtest"
	"github.com/ajlatour/golang-vibe-starter/internal/nettest"
	"github.com/ajlatour/golang-vibe-starter/internal/validator"
)

// TestClient checks the client speaks the placeholder contract: POST
// /validate with the request, a decision back, and an error, not a refusal,
// for an answer it can't use.
func TestClient(t *testing.T) {
	var got validator.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/validate" {
			t.Errorf("request = %s %s, want POST /validate", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"allow":false,"reason":"over quota"}`)
	}))
	defer srv.Close()

	c := validator.New(srv.URL+"/", srv.Client())
	d, err := c.Check(t.Context(), validator.Request{Client: "ci", Method: "GET", Path: "/items"})
	if err != nil || d.Allow || d.Reason != "over quota" {
		t.Errorf("Check = %+v, %v; want a refusal, over quota", d, err)
	}
	if got != (validator.Request{Client: "ci", Method: "GET", Path: "/items"}) {
		t.Errorf("the service was asked about %+v", got)
	}

	for name, h := range map[string]http.HandlerFunc{
		"an error status":          func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusInternalServerError) },
		"a body that won't decode": func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, `{"allow":`) },
	} {
		srv := httptest.NewServer(h)
		if _, err := validator.New(srv.URL, srv.Client()).Check(t.Context(), validator.Request{}); err == nil {
			t.Errorf("%s: no error, want one", name)
		}
		srv.Close()
	}
	if _, err := validator.New(nettest.DeadURL(), &http.Client{Timeout: time.Second}).Check(t.Context(), validator.Request{}); err == nil {
		t.Error("an unreachable service: no error, want one")
	}
}

// fakeChecker answers with a decision, or fails, and counts its calls.
type fakeChecker struct {
	allow  atomic.Bool
	fail   atomic.Bool
	calls  atomic.Int32
	reason string
}

func (f *fakeChecker) Check(_ context.Context, req validator.Request) (validator.Decision, error) {
	f.calls.Add(1)
	if f.fail.Load() {
		return validator.Decision{}, errors.New("validator POST /validate: connection refused")
	}
	return validator.Decision{Allow: f.allow.Load(), Reason: f.reason}, nil
}

func ok(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }

func call(h http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func msgs(t *testing.T, logs *logtest.Buffer) []string {
	return logtest.LevelMsgs(logs.Records(t))
}

func TestMiddlewareDecisions(t *testing.T) {
	f := &fakeChecker{reason: "blocked by rule 7"}
	var logs logtest.Buffer
	var asked validator.Request
	p := validator.Policy{Exempt: []string{"/healthz"}, Client: func(*http.Request) string { return "ci" }}
	h := validator.Middleware(logs.Logger(slog.LevelInfo), checkerFunc(func(ctx context.Context, r validator.Request) (validator.Decision, error) {
		asked = r
		return f.Check(ctx, r)
	}), p, http.HandlerFunc(ok))

	f.allow.Store(true)
	if rec := call(h, "/items"); rec.Code != http.StatusNoContent {
		t.Errorf("allowed: %d, want it through", rec.Code)
	}
	if asked != (validator.Request{Client: "ci", Method: "GET", Path: "/items"}) {
		t.Errorf("asked about %+v", asked)
	}

	f.allow.Store(false)
	rec := call(h, "/items")
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "the request was refused") || strings.Contains(rec.Body.String(), "rule 7") {
		t.Errorf("refused: %d %s; want 403, without the service's reason", rec.Code, rec.Body)
	}
	if got := msgs(t, &logs); !slices.Equal(got, []string{"INFO request refused by validation"}) || !strings.Contains(logs.String(), "blocked by rule 7") {
		t.Errorf("logs = %q, want one refusal line with the reason", got)
	}

	before := f.calls.Load()
	if rec := call(h, "/healthz"); rec.Code != http.StatusNoContent || f.calls.Load() != before {
		t.Error("an exempt path was checked")
	}
}

type checkerFunc func(context.Context, validator.Request) (validator.Decision, error)

func (f checkerFunc) Check(ctx context.Context, r validator.Request) (validator.Decision, error) {
	return f(ctx, r)
}

// TestMiddlewareFailClosed checks an outage, failing closed: 503 with
// Retry-After, the service asked at most once per Retry, one warning when it
// starts and one line with what it cost when it ends.
func TestMiddlewareFailClosed(t *testing.T) {
	now := time.Unix(0, 0)
	f := &fakeChecker{}
	f.allow.Store(true)
	f.fail.Store(true)
	var logs logtest.Buffer
	h := validator.MiddlewareAt(logs.Logger(slog.LevelInfo), f, validator.Policy{Retry: time.Second}, func() time.Time { return now }, http.HandlerFunc(ok))

	for i := range 5 {
		rec := call(h, "/items")
		if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "1" {
			t.Fatalf("request %d during the outage: %d, Retry-After %q; want 503, 1", i+1, rec.Code, rec.Header().Get("Retry-After"))
		}
	}
	if n := f.calls.Load(); n != 1 {
		t.Errorf("the service was asked %d times within a second of the outage, want 1", n)
	}

	f.fail.Store(false)
	now = now.Add(time.Second)
	if rec := call(h, "/items"); rec.Code != http.StatusNoContent {
		t.Errorf("after recovery: %d, want it through", rec.Code)
	}
	got := msgs(t, &logs)
	want := []string{"WARN request validation unavailable", "INFO request validation recovered"}
	if !slices.Equal(got, want) {
		t.Fatalf("logs = %q, want %q", got, want)
	}
	if last := logs.Records(t)[1]; last["failures"] != float64(5) || last["duration_ms"] != float64(1000) {
		t.Errorf("recovered line = %v, want 5 failures over 1000ms", last)
	}
}

// TestMiddlewareFailOpen checks an outage, failing open: every request goes
// through, and the outage is still logged.
func TestMiddlewareFailOpen(t *testing.T) {
	f := &fakeChecker{}
	f.fail.Store(true)
	var logs logtest.Buffer
	h := validator.Middleware(logs.Logger(slog.LevelInfo), f, validator.Policy{FailOpen: true, Retry: time.Minute}, http.HandlerFunc(ok))
	for range 3 {
		if rec := call(h, "/items"); rec.Code != http.StatusNoContent {
			t.Fatalf("failing open: %d, want it through", rec.Code)
		}
	}
	if got := msgs(t, &logs); !slices.Equal(got, []string{"WARN request validation unavailable"}) || !strings.Contains(logs.String(), `"fail_open":true`) {
		t.Errorf("logs = %q, want one warning saying it fails open", got)
	}
}

func TestReachable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler()) // any answer counts
	defer srv.Close()
	if err := validator.New(srv.URL, srv.Client()).Reachable(t.Context()); err != nil {
		t.Errorf("Reachable = %v, want nil for a 404", err)
	}
	if err := validator.New(nettest.DeadURL(), &http.Client{Timeout: time.Second}).Reachable(t.Context()); err == nil {
		t.Error("Reachable = nil for an unreachable service, want an error")
	}
}
