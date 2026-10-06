package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/headerguard"
	"github.com/ajlatour/golang-vibe-starter/internal/httpx"
	"github.com/ajlatour/golang-vibe-starter/internal/item"
	"github.com/ajlatour/golang-vibe-starter/internal/item/httpstore"
	"github.com/ajlatour/golang-vibe-starter/internal/item/itemtest"
	"github.com/ajlatour/golang-vibe-starter/internal/limit"
	"github.com/ajlatour/golang-vibe-starter/internal/nettest"
	"github.com/ajlatour/golang-vibe-starter/internal/validator"
)

// fixedFlags is an item.Flags with one answer for every flag.
type fixedFlags bool

func (f fixedFlags) Bool(context.Context, string) bool { return bool(f) }

// errStore fails every call with a plain error, as a bug in the service
// would, so the handlers answer 500.
type errStore struct{}

var errBoom = errors.New("boom")

func (errStore) List(context.Context, item.Page) (item.Listing, error) {
	return item.Listing{}, errBoom
}

func (errStore) Get(context.Context, item.ID) (item.Item, error) { return item.Item{}, errBoom }

func (errStore) Create(context.Context, item.Draft) (item.Item, error) {
	return item.Item{}, errBoom
}

func (errStore) Update(context.Context, item.ID, item.Draft) (item.Item, error) {
	return item.Item{}, errBoom
}

func (errStore) Delete(context.Context, item.ID) error { return errBoom }

// handlerFor builds the real service handler for an environment:
//
//	ok       an in-memory store holding one item; flags off
//	flagOn   the same, with the items-list-total flag on
//	paged       three items, so a page of two has a next
//	refused     the request-validation service refuses every request (403)
//	unvalidated the request-validation service can't answer, failing closed (503)
//	internal a store that fails with a plain error (a bug here: 500)
//	badgw    an upstream that refuses connections (502)
//	timeout  an upstream that never answers (504)
//
// It returns the handler and the seeded item's ID.
func handlerFor(t *testing.T, env string) (http.Handler, string) {
	t.Helper()
	logger := slog.New(slog.DiscardHandler)
	switch env {
	case "ok", "flagOn":
		store := itemtest.NewMemStore()
		seeded, err := store.Create(t.Context(), itemtest.MustDraft("seeded", "first"))
		if err != nil {
			t.Fatal(err)
		}
		return newHandler(logger, handlerConfig{Guard: headerguard.DefaultPolicy(), Auth: testAuth(t)}, store, item.WithFlags(fixedFlags(env == "flagOn"))), seeded.ID.String()
	case "ratelimited":
		// The test client (its key) has spent its only token, and gets
		// another in about 11 days.
		limits := limit.Policy{Rate: 1e-6, Burst: 1}
		h := newHandler(logger, handlerConfig{Guard: headerguard.DefaultPolicy(), Auth: testAuth(t), Limits: limits}, itemtest.NewMemStore())
		warm := httptest.NewRequest("GET", "/items", nil)
		warm.Header.Set("Authorization", "Bearer "+testWriteKey)
		h.ServeHTTP(httptest.NewRecorder(), warm)
		return h, ""
	case "overloaded":
		// The one request the service takes at once is stuck in the store.
		store := blockingStore{entered: make(chan struct{}), release: make(chan struct{})}
		h := newHandler(logger, handlerConfig{Guard: headerguard.DefaultPolicy(), Auth: testAuth(t), Limits: limit.Policy{MaxInFlight: 1}}, store)
		stuck := httptest.NewRequest("GET", "/items", nil)
		stuck.Header.Set("Authorization", "Bearer "+testWriteKey)
		done := make(chan struct{})
		go func() { h.ServeHTTP(httptest.NewRecorder(), stuck); close(done) }()
		<-store.entered
		t.Cleanup(func() { close(store.release); <-done })
		return h, ""
	case "paged":
		// Three items, so a page of two has a next.
		store := itemtest.NewMemStore()
		for _, name := range []string{"a", "b", "c"} {
			if _, err := store.Create(t.Context(), itemtest.MustDraft(name, "")); err != nil {
				t.Fatal(err)
			}
		}
		return newHandler(logger, handlerConfig{Guard: headerguard.DefaultPolicy(), Auth: testAuth(t)}, store), ""
	case "refused":
		// The request-validation service refuses every request.
		return newHandler(logger, handlerConfig{Guard: headerguard.DefaultPolicy(), Auth: testAuth(t), Validator: fixedDecision{}}, itemtest.NewMemStore()), ""
	case "unvalidated":
		// The request-validation service can't answer, and the service fails
		// closed.
		v := validator.New(nettest.DeadURL(), &http.Client{Timeout: time.Second})
		return newHandler(logger, handlerConfig{Guard: headerguard.DefaultPolicy(), Auth: testAuth(t), Validator: v}, itemtest.NewMemStore()), ""
	case "internal":
		return newHandler(logger, handlerConfig{Guard: headerguard.DefaultPolicy(), Auth: testAuth(t)}, errStore{}), ""
	case "badgw":
		store := httpstore.New(nettest.DeadURL(), &http.Client{Timeout: time.Second})
		readiness := httpx.NewReadiness(logger, time.Second, time.Second, httpx.Check{Name: "upstream", Fn: store.Reachable})
		return newHandler(logger, handlerConfig{Guard: headerguard.DefaultPolicy(), Auth: testAuth(t), Readiness: readiness}, store), ""
	case "timeout":
		upstream := httptest.NewServer(http.HandlerFunc(nettest.Hang))
		t.Cleanup(upstream.Close)
		return newHandler(logger, handlerConfig{Guard: headerguard.DefaultPolicy(), Auth: testAuth(t)}, httpstore.New(upstream.URL, &http.Client{Timeout: 50 * time.Millisecond})), ""
	}
	t.Fatalf("unknown environment %q", env)
	return nil, ""
}

// spyStore notes that it was asked anything, and has no items.
type spyStore struct{ asked *bool }

func (s spyStore) List(context.Context, item.Page) (item.Listing, error) {
	*s.asked = true
	return item.Listing{Items: []item.Item{}}, nil
}

func (s spyStore) Get(context.Context, item.ID) (item.Item, error) {
	*s.asked = true
	return item.Item{}, item.ErrNotFound
}

func (s spyStore) Create(context.Context, item.Draft) (item.Item, error) {
	*s.asked = true
	return item.Item{}, nil
}

func (s spyStore) Update(context.Context, item.ID, item.Draft) (item.Item, error) {
	*s.asked = true
	return item.Item{}, item.ErrNotFound
}

func (s spyStore) Delete(context.Context, item.ID) error { *s.asked = true; return item.ErrNotFound }

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

// fixedDecision is a validator.Checker that answers every request with
// itself: the zero value refuses.
type fixedDecision validator.Decision

func (d fixedDecision) Check(context.Context, validator.Request) (validator.Decision, error) {
	return validator.Decision(d), nil
}
