package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	// feature:validation
	"sync/atomic"
	// end feature:validation
	"testing"

	"github.com/ajlatour/golang-vibe-starter/internal/apikey"
	"github.com/ajlatour/golang-vibe-starter/internal/headerguard"
	"github.com/ajlatour/golang-vibe-starter/internal/item"
	"github.com/ajlatour/golang-vibe-starter/internal/item/itemtest"
	"github.com/ajlatour/golang-vibe-starter/internal/limit"
	// feature:validation
	"github.com/ajlatour/golang-vibe-starter/internal/validator"
	// end feature:validation
)

// The order of newHandler's middleware is a security property: each step
// refuses what the ones inside it shouldn't see. These tests hold each pair
// that matters by what a request gets when two steps would both refuse it:
// the outer one must answer. So swapping two steps fails here, though every
// step still works on its own.

// orderRequest is a request to /items with the test write key, unless the
// caller changes it.
func orderRequest(method, target string) *http.Request {
	r := httptest.NewRequest(method, target, nil)
	r.Header.Set("Authorization", "Bearer "+testWriteKey)
	return r
}

// code sends r to h and returns the status and the error code answered.
func code(t *testing.T, h http.Handler, r *http.Request) (int, string) {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var body struct{ Code string }
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return w.Code, body.Code
}

func TestMiddlewareOrder(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	guard := headerguard.DefaultPolicy()

	t.Run("the header guard before authentication", func(t *testing.T) {
		// Two Authorization headers, neither a valid key: the guard refuses
		// the doubling before either copy is read.
		h := newHandler(logger, handlerConfig{Guard: guard, Auth: testAuth(t)}, itemRoutes(itemtest.NewMemStore()))
		r := httptest.NewRequest("GET", "/items", nil)
		r.Header.Add("Authorization", "Bearer one")
		r.Header.Add("Authorization", "Bearer two")
		if status, c := code(t, h, r); c != "duplicate_header" {
			t.Errorf("got %d %s, want 400 duplicate_header", status, c)
		}
	})

	t.Run("the API version before authentication", func(t *testing.T) {
		h := newHandler(logger, handlerConfig{Guard: guard, Auth: testAuth(t)}, itemRoutes(itemtest.NewMemStore()))
		r := httptest.NewRequest("GET", "/items", nil)
		r.Header.Set("API-Version", "9")
		if status, c := code(t, h, r); c != "unsupported_api_version" {
			t.Errorf("got %d %s, want 400 unsupported_api_version", status, c)
		}
	})

	t.Run("the in-flight cap before the header guard, and a bad target before both", func(t *testing.T) {
		// The one request the service takes at once is stuck in the store.
		store := blockingStore{entered: make(chan struct{}), release: make(chan struct{})}
		h := newHandler(logger, handlerConfig{Guard: guard, Auth: testAuth(t), Limits: limit.Policy{MaxInFlight: 1}}, itemRoutes(store))
		done := make(chan struct{})
		go func() { h.ServeHTTP(httptest.NewRecorder(), orderRequest("GET", "/items")); close(done) }()
		<-store.entered
		defer func() { close(store.release); <-done }()

		doubled := orderRequest("GET", "/items")
		doubled.Header.Add("Client-Correlation-Id", "a")
		doubled.Header.Add("Client-Correlation-Id", "b")
		if status, c := code(t, h, doubled); c != "busy" {
			t.Errorf("a doubled header while full: got %d %s, want 503 busy", status, c)
		}
		star := orderRequest("OPTIONS", "/")
		star.RequestURI, star.URL.Path = "*", "*"
		if status, c := code(t, h, star); c != "invalid_target" {
			t.Errorf("OPTIONS * while full: got %d %s, want 400 invalid_target", status, c)
		}
	})

	t.Run("authentication before the rate limit", func(t *testing.T) {
		// One request per address, ever. A request with a key that isn't
		// valid is refused by authentication each time; were the limit
		// first, the second would be refused as over it.
		h := newHandler(logger, handlerConfig{Guard: guard, Auth: testAuth(t), Limits: limit.Policy{Rate: 1e-6, Burst: 1}}, itemRoutes(itemtest.NewMemStore()))
		for i := range 2 {
			r := httptest.NewRequest("GET", "/items", nil)
			r.Header.Set("Authorization", "Bearer gvs_not-a-key")
			if status, c := code(t, h, r); c != "api_key_invalid" {
				t.Errorf("request %d with an invalid key: got %d %s, want 401 api_key_invalid", i+1, status, c)
			}
		}
		// And a client is its key: a second key has its own limit.
		keys, err := apikey.ParseKeys("one:read:" + apikey.HashHex(testWriteKey) + ":1/1,two:read:" + apikey.HashHex(testReadKey) + ":1/1")
		if err != nil {
			t.Fatal(err)
		}
		h = newHandler(logger, handlerConfig{Guard: guard, Auth: apikey.Policy{Keys: keys}}, itemRoutes(itemtest.NewMemStore()))
		code(t, h, orderRequest("GET", "/items"))
		if status, c := code(t, h, orderRequest("GET", "/items")); c != "rate_limited" {
			t.Errorf("the first key's second request: got %d %s, want 429 rate_limited", status, c)
		}
		other := httptest.NewRequest("GET", "/items", nil)
		other.Header.Set("Authorization", "Bearer "+testReadKey)
		if status, _ := code(t, h, other); status != http.StatusOK {
			t.Errorf("the second key, with its own limit: got %d, want 200", status)
		}
	})

	// feature:validation
	t.Run("the rate limit before request validation", func(t *testing.T) {
		// A client over its limit never costs the validation service a call.
		checker := &countingChecker{}
		keys, err := apikey.ParseKeys("capped:read:" + apikey.HashHex(testWriteKey) + ":1/1")
		if err != nil {
			t.Fatal(err)
		}
		h := newHandler(logger, handlerConfig{Guard: guard, Auth: apikey.Policy{Keys: keys}, Validator: checker}, itemRoutes(itemtest.NewMemStore()))
		code(t, h, orderRequest("GET", "/items"))
		if status, c := code(t, h, orderRequest("GET", "/items")); c != "rate_limited" {
			t.Errorf("over the limit: got %d %s, want 429 rate_limited", status, c)
		}
		if n := checker.calls.Load(); n != 1 {
			t.Errorf("the validation service was asked %d times, want once: the limited request reached it", n)
		}
	})
	// end feature:validation
}

// feature:validation
// countingChecker is a validator.Checker that allows every request, and
// counts them.
type countingChecker struct{ calls atomic.Int32 }

func (c *countingChecker) Check(context.Context, validator.Request) (validator.Decision, error) {
	c.calls.Add(1)
	return validator.Decision{Allow: true}, nil
}

// end feature:validation

// blockingStore holds every call until release is closed, after saying on
// entered that one has arrived.
type blockingStore struct{ entered, release chan struct{} }

func (s blockingStore) wait() { s.entered <- struct{}{}; <-s.release }

func (s blockingStore) List(context.Context, item.Page) (item.Listing, error) {
	s.wait()
	return item.Listing{Items: []item.Item{}}, nil
}

func (s blockingStore) Get(context.Context, item.ID) (item.Item, error) {
	s.wait()
	return item.Item{}, item.ErrNotFound
}

func (s blockingStore) Create(context.Context, item.Draft) (item.Item, error) {
	s.wait()
	return item.Item{}, nil
}

func (s blockingStore) Update(context.Context, item.ID, item.Draft) (item.Item, error) {
	s.wait()
	return item.Item{}, nil
}

func (s blockingStore) Delete(context.Context, item.ID) error { s.wait(); return nil }
