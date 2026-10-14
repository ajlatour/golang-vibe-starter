package httpx_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/httpx"
)

func TestParsePage(t *testing.T) {
	for _, tc := range []struct {
		query string
		want  httpx.PageRequest
		bad   bool
	}{
		{"", httpx.PageRequest{Limit: 50}, false},
		{"limit=1", httpx.PageRequest{Limit: 1}, false},
		{"limit=200&cursor=abc_-9", httpx.PageRequest{Limit: 200, Cursor: "abc_-9"}, false},
		{"other=x", httpx.PageRequest{Limit: 50}, false},
		{"limit=0", httpx.PageRequest{}, true},
		{"limit=201", httpx.PageRequest{}, true},
		{"limit=ten", httpx.PageRequest{}, true},
		{"cursor=a%2Fb", httpx.PageRequest{}, true},
		{"cursor=" + strings.Repeat("a", httpx.MaxCursorLen+1), httpx.PageRequest{}, true},
	} {
		got, err := httpx.ParsePage(httptest.NewRequest("GET", "/things?"+tc.query, nil))
		if tc.bad {
			var e *httpx.Error
			if !errors.As(err, &e) || e.Status != http.StatusBadRequest || e.Code != httpx.CodeInvalidParameter {
				t.Errorf("?%s: err = %v, want a 400 invalid_parameter", tc.query, err)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("?%s = %+v, %v; want %+v", tc.query, got, err, tc.want)
		}
	}
}

func TestListJSON(t *testing.T) {
	n := 2
	for _, tc := range []struct {
		list httpx.List[string]
		want string
	}{
		{httpx.List[string]{Items: []string{}}, `{"items":[]}`},
		{httpx.List[string]{Items: []string{"a", "b"}, NextCursor: "c", Total: &n}, `{"items":["a","b"],"next_cursor":"c","total":2}`},
	} {
		b, _ := json.Marshal(tc.list)
		if string(b) != tc.want {
			t.Errorf("got %s, want %s", b, tc.want)
		}
	}
}

func TestETag(t *testing.T) {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// The tag the item handlers sent before ETag moved here: clients' cached
	// tags must stay valid.
	if got := httpx.ETag(at, "Widget", "A standard widget."); got != `"3grxah94rbikl"` {
		t.Errorf("ETag = %s, want the tag items had before", got)
	}
	base := httpx.ETag(at, "ab", "c")
	for name, other := range map[string]string{
		"fields split differently": httpx.ETag(at, "a", "bc"),
		"a field changed":          httpx.ETag(at, "ab", "d"),
		"a nanosecond later":       httpx.ETag(at.Add(time.Nanosecond), "ab", "c"),
	} {
		if other == base {
			t.Errorf("%s: same tag %s", name, base)
		}
	}
	if httpx.ETag(at, "ab", "c") != base {
		t.Error("the same resource got a different tag")
	}
	if n := testing.AllocsPerRun(100, func() { _ = httpx.ETag(at, "ab", "c") }); n > 1 {
		t.Errorf("ETag allocates %v times, want at most 1 (its string)", n)
	}
}

func TestConditionalRequests(t *testing.T) {
	tag := `"abc"`
	for _, tc := range []struct {
		header       string
		weak, strong bool // matches as If-None-Match, as If-Match
	}{
		{`"abc"`, true, true},
		{`W/"abc"`, true, false},
		{`"x", "abc"`, true, true},
		{`*`, true, true},
		{`"x"`, false, false},
	} {
		if got := httpx.ETagMatches(tc.header, tag, true); got != tc.weak {
			t.Errorf("weak %s = %v", tc.header, got)
		}
		if got := httpx.ETagMatches(tc.header, tag, false); got != tc.strong {
			t.Errorf("strong %s = %v", tc.header, got)
		}
	}

	r := httptest.NewRequest("GET", "/", nil)
	if httpx.NotModified(r, tag) {
		t.Error("NotModified without If-None-Match")
	}
	r.Header.Set("If-None-Match", `W/"abc"`)
	if !httpx.NotModified(r, tag) {
		t.Error("NotModified with a weakly matching If-None-Match = false")
	}

	w := httptest.NewRecorder()
	httpx.SetETag(w, tag)
	if w.Header().Get("ETag") != tag {
		t.Errorf("SetETag: header %q", w.Header().Get("ETag"))
	}

	if httpx.IfMatch(httptest.NewRequest("PUT", "/", nil)) != "" {
		t.Error("IfMatch without the header isn't empty")
	}
	if err := httpx.Precondition(`"abc"`, tag, true); err != nil {
		t.Errorf("Precondition on a match = %v", err)
	}
	for name, err := range map[string]error{
		"a stale tag":          httpx.Precondition(`"old"`, tag, true),
		"a weak tag":           httpx.Precondition(`W/"abc"`, tag, true),
		"no such resource":     httpx.Precondition(`"abc"`, "", false),
		"* with none to match": httpx.Precondition(`*`, "", false),
	} {
		if err != httpx.ErrPreconditionFailed {
			t.Errorf("%s: %v, want ErrPreconditionFailed", name, err)
		}
	}
}

func TestDecodeValid(t *testing.T) {
	type in struct{ Name string }
	ruleBroken := httpx.NewError(http.StatusUnprocessableEntity, httpx.CodeInvalidField, "name is required")
	valid := func(v in) (string, error) {
		switch v.Name {
		case "":
			return "", ruleBroken // made once, returned as it is
		case "long":
			return "", errors.New("name is too long") // wrapped as a 422
		}
		return strings.ToUpper(v.Name), nil
	}
	for _, tc := range []struct {
		body   string
		want   string
		status int
	}{
		{`{"Name":"x"}`, "X", 0},
		{`{"Name":`, "", http.StatusBadRequest},
		{`{"Name":""}`, "", http.StatusUnprocessableEntity},
		{`{"Name":"long"}`, "", http.StatusUnprocessableEntity},
	} {
		r := httptest.NewRequest("POST", "/", strings.NewReader(tc.body))
		r.Header.Set("Content-Type", "application/json")
		got, err := httpx.DecodeValid(httptest.NewRecorder(), r, valid)
		if tc.status == 0 {
			if err != nil || got != tc.want {
				t.Errorf("%s = %q, %v; want %q", tc.body, got, err, tc.want)
			}
			continue
		}
		var e *httpx.Error
		if !errors.As(err, &e) || e.Status != tc.status {
			t.Errorf("%s: err = %v, want status %d", tc.body, err, tc.status)
		}
		if tc.body == `{"Name":""}` && err != ruleBroken {
			t.Errorf("an *Error the rules returned was replaced: %v", err)
		}
	}
}

func TestWithStatus(t *testing.T) {
	cause := errors.New("upstream reset")
	err := httpx.WithStatus(cause, http.StatusBadGateway)
	se, ok := errors.AsType[httpx.StatusError](err)
	if !ok || se.HTTPStatus() != http.StatusBadGateway || !errors.Is(err, cause) {
		t.Errorf("WithStatus = %v: status %v, keeps its cause %v", err, se, errors.Is(err, cause))
	}
}
