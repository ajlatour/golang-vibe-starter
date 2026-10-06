package httpstore_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/item"
	"github.com/ajlatour/golang-vibe-starter/internal/item/httpstore"
	"github.com/ajlatour/golang-vibe-starter/internal/item/itemtest"
	"github.com/ajlatour/golang-vibe-starter/internal/logtest"
	"github.com/ajlatour/golang-vibe-starter/internal/nettest"
)

// failing answers its first failures calls with status, then a list.
func failing(t *testing.T, failures int32, status int) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= failures {
			w.WriteHeader(status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func TestRetriesAGetThatFailedFast(t *testing.T) {
	srv, calls := failing(t, 1, http.StatusServiceUnavailable)
	var logs logtest.Buffer
	store := httpstore.New(srv.URL, srv.Client(), httpstore.WithRetries(1), httpstore.WithLogger(logs.Logger(slog.LevelInfo)))
	if _, err := store.List(t.Context()); err != nil {
		t.Fatalf("List = %v, want the retry's answer", err)
	}
	if n := calls.Load(); n != 2 {
		t.Errorf("the upstream was called %d times, want 2", n)
	}
	got := logtest.LevelMsgs(logs.Records(t))
	want := "INFO upstream request sent,INFO upstream response received,INFO upstream request retried,INFO upstream request sent,INFO upstream response received"
	if strings.Join(got, ",") != want {
		t.Errorf("logs = %q", got)
	}
	if !strings.Contains(logs.String(), `"attempt":2`) {
		t.Errorf("the retry line doesn't say it's the second attempt:\n%s", logs.String())
	}
}

// TestNeverRetries checks what isn't retried: a write, a timeout, a call
// whose client has gone, a 4xx (the upstream answered), and any GET when
// retries are off.
func TestNeverRetries(t *testing.T) {
	t.Run("a write", func(t *testing.T) {
		srv, calls := failing(t, 5, http.StatusServiceUnavailable)
		store := httpstore.New(srv.URL, srv.Client(), httpstore.WithRetries(3))
		if _, err := store.Create(t.Context(), itemtest.MustDraft("x", "")); err == nil {
			t.Fatal("Create succeeded")
		}
		if n := calls.Load(); n != 1 {
			t.Errorf("a POST was sent %d times, want once", n)
		}
	})
	t.Run("a 4xx", func(t *testing.T) {
		srv, calls := failing(t, 5, http.StatusBadRequest)
		store := httpstore.New(srv.URL, srv.Client(), httpstore.WithRetries(3))
		_, _ = store.List(t.Context())
		if n := calls.Load(); n != 1 {
			t.Errorf("a 400 was retried: %d calls", n)
		}
	})
	t.Run("retries off", func(t *testing.T) {
		srv, calls := failing(t, 5, http.StatusServiceUnavailable)
		_, _ = httpstore.New(srv.URL, srv.Client()).List(t.Context())
		if n := calls.Load(); n != 1 {
			t.Errorf("retried with retries off: %d calls", n)
		}
	})
	t.Run("a timeout", func(t *testing.T) {
		var calls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			nettest.Hang(w, r)
		}))
		defer srv.Close()
		store := httpstore.New(srv.URL, &http.Client{Timeout: 50 * time.Millisecond}, httpstore.WithRetries(3))
		_, err := store.List(t.Context())
		if !errors.Is(err, item.ErrStoreTimeout) || calls.Load() != 1 {
			t.Errorf("a timeout: %v after %d calls; want ErrStoreTimeout after one", err, calls.Load())
		}
	})
	t.Run("the client left", func(t *testing.T) {
		srv, calls := failing(t, 5, http.StatusServiceUnavailable)
		ctx, cancel := context.WithCancel(t.Context())
		store := httpstore.New(srv.URL, srv.Client(), httpstore.WithRetries(3))
		cancel()
		_, _ = store.List(ctx)
		if n := calls.Load(); n > 1 {
			t.Errorf("retried for a client that had gone: %d calls", n)
		}
	})
}

// TestRetryBudget checks that while the upstream keeps failing, retries
// stop at the budget: what was saved up, then about one per ten requests.
func TestRetryBudget(t *testing.T) {
	srv, calls := failing(t, 1<<30, http.StatusServiceUnavailable)
	store := httpstore.New(srv.URL, srv.Client(), httpstore.WithRetries(1))
	const requests = 100
	for range requests {
		_, _ = store.List(t.Context())
	}
	retries := calls.Load() - requests
	// The 10 saved up, and a tenth of a retry per request: at most 20.
	if retries < 10 || retries > 20 {
		t.Errorf("%d retries for %d failing requests, want the budget's 10 to 20", retries, requests)
	}
}
