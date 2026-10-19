package jsoncall_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/jsoncall"
	"github.com/ajlatour/golang-vibe-starter/internal/logx"
	"github.com/ajlatour/golang-vibe-starter/internal/nettest"
	"github.com/ajlatour/golang-vibe-starter/internal/tracectx"
)

func newCall(url string) jsoncall.Call {
	return jsoncall.Call{
		Client: &http.Client{Timeout: 2 * time.Second},
		Method: http.MethodGet,
		URL:    url,
		Label:  "svc GET /x",
		Want:   http.StatusOK,
	}
}

func TestDoSuccess(t *testing.T) {
	var gotMethod, gotAccept, gotType string
	var gotBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotAccept, gotType = r.Method, r.Header.Get("Accept"), r.Header.Get("Content-Type")
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":"7"}`))
	}))
	defer srv.Close()

	var out struct{ ID string }
	c := newCall(srv.URL)
	c.Method, c.Want = http.MethodPost, http.StatusCreated
	c.In, c.Out = map[string]string{"name": "n"}, &out
	status, err := c.Do(t.Context())
	if err != nil || status != http.StatusCreated {
		t.Fatalf("Do() = %d, %v, want 201, nil", status, err)
	}
	if out.ID != "7" {
		t.Errorf("decoded %+v, want ID 7", out)
	}
	if gotMethod != "POST" || gotAccept != "application/json" || gotType != "application/json" || gotBody["name"] != "n" {
		t.Errorf("server saw %s accept=%q type=%q body=%v", gotMethod, gotAccept, gotType, gotBody)
	}
}

func TestDoNoBodyNoContentType(t *testing.T) {
	var gotType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotType = r.Header.Get("Content-Type")
	}))
	defer srv.Close()
	if _, err := newCall(srv.URL).Do(t.Context()); err != nil {
		t.Fatal(err)
	}
	if gotType != "" {
		t.Errorf("Content-Type = %q on a request with no body, want none", gotType)
	}
}

func TestDoFailureKinds(t *testing.T) {
	tests := []struct {
		name       string
		call       func() jsoncall.Call
		wantKind   jsoncall.Kind
		wantStatus int
		wantNoAns  bool
		wantMsg    string
	}{
		{
			name:     "connection refused",
			call:     func() jsoncall.Call { return newCall(nettest.DeadURL()) },
			wantKind: jsoncall.KindTransport, wantNoAns: true, wantMsg: "svc GET /x: ",
		},
		{
			name: "dropped",
			call: func() jsoncall.Call {
				srv := httptest.NewServer(http.HandlerFunc(nettest.Drop))
				t.Cleanup(srv.Close)
				return newCall(srv.URL)
			},
			wantKind: jsoncall.KindTransport, wantNoAns: true, wantMsg: "EOF",
		},
		{
			name: "4xx is not an outage",
			call: func() jsoncall.Call {
				srv := httptest.NewServer(nettest.Status(http.StatusNotFound, ""))
				t.Cleanup(srv.Close)
				return newCall(srv.URL)
			},
			wantKind: jsoncall.KindStatus, wantStatus: 404, wantMsg: "svc GET /x: unexpected status 404",
		},
		{
			name: "5xx counts as no answer",
			call: func() jsoncall.Call {
				srv := httptest.NewServer(nettest.Status(http.StatusServiceUnavailable, ""))
				t.Cleanup(srv.Close)
				return newCall(srv.URL)
			},
			wantKind: jsoncall.KindStatus, wantStatus: 503, wantNoAns: true, wantMsg: "unexpected status 503",
		},
		{
			name: "undecodable body",
			call: func() jsoncall.Call {
				srv := httptest.NewServer(nettest.Status(http.StatusOK, `{"a":`))
				t.Cleanup(srv.Close)
				c := newCall(srv.URL)
				c.Out = &struct{}{}
				return c
			},
			wantKind: jsoncall.KindDecode, wantStatus: 200, wantMsg: "svc GET /x: decode response: ",
		},
		{
			name: "unbuildable request",
			call: func() jsoncall.Call {
				c := newCall("http://[::1")
				return c
			},
			wantKind: jsoncall.KindRequest, wantMsg: "svc GET /x: ",
		},
		{
			name: "unencodable body",
			call: func() jsoncall.Call {
				c := newCall("http://example.invalid")
				c.In = make(chan int)
				return c
			},
			wantKind: jsoncall.KindRequest, wantMsg: "encode request",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, err := tt.call().Do(t.Context())
			var je *jsoncall.Error
			if !errors.As(err, &je) {
				t.Fatalf("Do() error = %v (%T), want *jsoncall.Error", err, err)
			}
			if je.Kind != tt.wantKind || je.Status != tt.wantStatus || status != tt.wantStatus {
				t.Errorf("Kind=%v Status=%d returned=%d, want Kind=%v Status=%d", je.Kind, je.Status, status, tt.wantKind, tt.wantStatus)
			}
			if je.NoAnswer() != tt.wantNoAns {
				t.Errorf("NoAnswer() = %v, want %v", je.NoAnswer(), tt.wantNoAns)
			}
			if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("message %q, want it to contain %q", err, tt.wantMsg)
			}
		})
	}
}

func TestDoTimeoutUnwrapsToNetError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(nettest.Hang))
	defer srv.Close()
	c := newCall(srv.URL)
	c.Client = &http.Client{Timeout: 50 * time.Millisecond}
	_, err := c.Do(t.Context())

	var ne net.Error
	if !errors.As(err, &ne) || !ne.Timeout() {
		t.Errorf("error %v: want errors.As to find a net.Error that reports a timeout", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error %v: want errors.Is(DeadlineExceeded)", err)
	}
}

func TestDoRedactsCredentials(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(nettest.Drop))
	defer srv.Close()
	c := newCall(strings.Replace(srv.URL, "http://", "http://alice:s3cret@", 1))
	_, err := c.Do(t.Context())
	if err == nil || strings.Contains(err.Error(), "s3cret") || strings.Contains(err.Error(), "***") || !strings.Contains(err.Error(), "xxxxx") {
		t.Errorf("error %v: want the password shown as xxxxx", err)
	}
}

var errSentinel = errors.New("sentinel")

func TestDoOnStatus(t *testing.T) {
	srv := httptest.NewServer(nettest.Status(http.StatusNotFound, ""))
	defer srv.Close()

	t.Run("its error is returned as is", func(t *testing.T) {
		c := newCall(srv.URL)
		c.OnStatus = func(resp *http.Response) error { return errSentinel }
		status, err := c.Do(t.Context())
		if err != errSentinel || status != 404 {
			t.Errorf("Do() = %d, %v, want 404 and exactly the sentinel", status, err)
		}
	})
	t.Run("nil falls through to the generic error", func(t *testing.T) {
		c := newCall(srv.URL)
		c.OnStatus = func(resp *http.Response) error { return nil }
		_, err := c.Do(t.Context())
		var je *jsoncall.Error
		if !errors.As(err, &je) || je.Kind != jsoncall.KindStatus {
			t.Errorf("Do() error = %v, want a KindStatus error", err)
		}
	})
	t.Run("not called for the wanted status", func(t *testing.T) {
		ok := httptest.NewServer(nettest.Status(http.StatusOK, ""))
		defer ok.Close()
		c := newCall(ok.URL)
		c.OnStatus = func(resp *http.Response) error { t.Error("OnStatus called for the success status"); return nil }
		if _, err := c.Do(t.Context()); err != nil {
			t.Fatal(err)
		}
	})
}

func TestDoErrorDetail(t *testing.T) {
	srv := httptest.NewServer(nettest.Status(http.StatusBadRequest, `{"why":"nope"}`))
	defer srv.Close()
	c := newCall(srv.URL)
	c.ErrorDetail = func(body io.Reader) string {
		var e struct{ Why string }
		json.NewDecoder(body).Decode(&e)
		return "why=" + e.Why
	}
	_, err := c.Do(t.Context())
	if err == nil || err.Error() != "svc GET /x: unexpected status 400: why=nope" {
		t.Errorf("error = %v, want the detail appended", err)
	}
}

// TestDoLabelWithPercent guards against building the message by putting the
// label into a format string: an escaped path such as /items/a%2Fb contains a
// "%" that would be read as a formatting verb.
func TestDoLabelWithPercent(t *testing.T) {
	srv := httptest.NewServer(nettest.Status(http.StatusTeapot, ""))
	defer srv.Close()
	c := newCall(srv.URL)
	c.Label = "upstream GET /items/a%2Fb%d%s"
	_, err := c.Do(t.Context())
	if err == nil || err.Error() != "upstream GET /items/a%2Fb%d%s: unexpected status 418" {
		t.Errorf("error = %q, want the label verbatim", err)
	}
}

func TestDoCapsResponse(t *testing.T) {
	big := `{"s":"` + strings.Repeat("a", 1000) + `"}`
	srv := httptest.NewServer(nettest.Status(http.StatusOK, big))
	defer srv.Close()

	c := newCall(srv.URL)
	c.Out = &struct{ S string }{}
	c.MaxResponseBytes = 100
	_, err := c.Do(t.Context())
	var je *jsoncall.Error
	if !errors.As(err, &je) || je.Kind != jsoncall.KindDecode {
		t.Errorf("Do() error = %v, want a decode error from the capped body", err)
	}

	c.MaxResponseBytes = 0 // the default is plenty
	if _, err := c.Do(t.Context()); err != nil {
		t.Errorf("Do() with the default cap: %v", err)
	}
}

func TestDoIgnoresBodyWhenOutNil(t *testing.T) {
	srv := httptest.NewServer(nettest.Status(http.StatusOK, `not json at all`))
	defer srv.Close()
	if _, err := newCall(srv.URL).Do(t.Context()); err != nil {
		t.Errorf("Do() error = %v, want nil when Out is nil", err)
	}
}

// TestDoRefusesRedirects checks a redirect is never followed, even by a
// client configured to follow them: the 3xx comes back as an unexpected
// status, and the address it names is never called, with or without the
// request body.
func TestDoRefusesRedirects(t *testing.T) {
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the redirect was followed: %s %s", r.Method, r.URL)
	}))
	defer elsewhere.Close()

	for _, code := range []int{301, 302, 303, 307, 308} {
		for _, in := range []any{nil, map[string]string{"secret": "body"}} {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, elsewhere.URL+"/internal", code)
			}))
			follows := &http.Client{Timeout: time.Second} // the default policy follows up to 10
			status, err := jsoncall.Call{Client: follows, Method: "POST", URL: srv.URL + "/x", Label: "svc POST /x", In: in, Want: 200}.Do(t.Context())
			srv.Close()

			var jerr *jsoncall.Error
			if !errors.As(err, &jerr) || jerr.Kind != jsoncall.KindStatus || status != code {
				t.Errorf("redirect %d (body %v): status %d, error %v; want the %d as an unexpected status", code, in != nil, status, err, code)
			}
			if follows.CheckRedirect != nil {
				t.Error("Do changed the caller's client")
			}
		}
	}
}

// TestCallJoinsTheTrace checks a call made within a request carries its
// trace on: the same trace and flags, with a span of the call's own; and
// that a call outside any request sends no traceparent.
func TestCallJoinsTheTrace(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("traceparent")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	call := jsoncall.Call{Client: srv.Client(), Method: http.MethodGet, URL: srv.URL, Label: "test", Want: http.StatusNoContent}

	parent, _ := tracectx.Parse("00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	ctx := logx.WithRequestID(t.Context(), "req-1")
	logx.SetTrace(ctx, parent)
	if _, err := call.Do(ctx); err != nil {
		t.Fatal(err)
	}
	sent, ok := tracectx.Parse(got)
	if !ok || sent.TraceID != parent.TraceID || sent.Flags != parent.Flags || sent.SpanID == parent.SpanID {
		t.Errorf("traceparent %q: want the request's trace and flags, with a new span", got)
	}

	got = "unset"
	if _, err := call.Do(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("a call outside any request sent traceparent %q", got)
	}
}

// recordedSpans is a tracectx.Spans that keeps what it's told, giving each
// span a fixed ID and sampling it.
type recordedSpans struct {
	starts []tracectx.Span
	ends   []tracectx.End
}

var spanID = [8]byte{1, 2, 3, 4, 5, 6, 7, 8}

func (s *recordedSpans) Start(_ context.Context, sp tracectx.Span) (tracectx.Parent, func(tracectx.End)) {
	s.starts = append(s.starts, sp)
	return tracectx.Parent{TraceID: sp.Parent.TraceID, SpanID: spanID, Flags: 1}, func(e tracectx.End) { s.ends = append(s.ends, e) }
}

// TestCallRecordsItsSpan checks a call made within a request whose spans are
// recorded records its own, as a client span of the request's, named by its
// label, and sends that span's context on; and that it's ended with the
// status, or the error.
func TestCallRecordsItsSpan(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("traceparent")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	parent, _ := tracectx.Parse("00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-00")
	ctx := logx.WithRequestID(t.Context(), "req-1")
	logx.SetTrace(ctx, parent)
	spans := &recordedSpans{}
	logx.SetSpans(ctx, spans)

	call := jsoncall.Call{Client: srv.Client(), Method: http.MethodGet, URL: srv.URL, Label: "upstream GET /x", Want: http.StatusNoContent}
	if _, err := call.Do(ctx); err != nil {
		t.Fatal(err)
	}
	if want := (tracectx.Parent{TraceID: parent.TraceID, SpanID: spanID, Flags: 1}).String(); got != want {
		t.Errorf("traceparent %q, want the call's span's, %q", got, want)
	}
	if len(spans.starts) != 1 || spans.starts[0] != (tracectx.Span{Kind: tracectx.Client, Name: "upstream GET /x", Method: http.MethodGet, Parent: parent}) {
		t.Errorf("started %+v, want one client span of the request's", spans.starts)
	}
	if len(spans.ends) != 1 || spans.ends[0].Status != http.StatusNoContent || spans.ends[0].Err != nil {
		t.Errorf("ended %+v, want 204 and no error", spans.ends)
	}

	// A call that gets no answer ends its span with why.
	dead := jsoncall.Call{Client: srv.Client(), Method: http.MethodGet, URL: nettest.DeadURL(), Label: "dead", Want: http.StatusOK}
	if _, err := dead.Do(ctx); err == nil {
		t.Fatal("a call to a dead address succeeded")
	}
	if len(spans.ends) != 2 || spans.ends[1].Status != 0 || spans.ends[1].Err == nil {
		t.Errorf("ended %+v, want the second with status 0 and its error", spans.ends)
	}
}
