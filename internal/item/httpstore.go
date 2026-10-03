package item

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
)

// HTTPStore is a Store backed by an upstream service that speaks the same
// /items JSON API that RegisterRoutes serves: WireMock stubs in local
// development, later a real backend. Switching upstreams is a base URL change.
type HTTPStore struct {
	baseURL string
	client  *http.Client
}

var _ Store = (*HTTPStore)(nil)

// maxResponseBytes caps how much of an upstream response is read.
const maxResponseBytes = 4 << 20 // 4 MiB

// NewHTTPStore returns a store that sends requests to baseURL (for example
// "http://mock:8080", optionally with a path prefix) using client. The
// client should have a timeout set.
func NewHTTPStore(baseURL string, client *http.Client) *HTTPStore {
	return &HTTPStore{baseURL: strings.TrimRight(baseURL, "/"), client: client}
}

func (s *HTTPStore) List(ctx context.Context) ([]Item, error) {
	var out struct {
		Items []Item `json:"items"`
	}
	if err := s.do(ctx, http.MethodGet, "/items", nil, http.StatusOK, &out); err != nil {
		return nil, err
	}
	if out.Items == nil {
		out.Items = []Item{}
	}
	return out.Items, nil
}

func (s *HTTPStore) Get(ctx context.Context, id string) (Item, error) {
	var it Item
	err := s.do(ctx, http.MethodGet, itemPath(id), nil, http.StatusOK, &it)
	return it, err
}

func (s *HTTPStore) Create(ctx context.Context, it Item) (Item, error) {
	var out Item
	err := s.do(ctx, http.MethodPost, "/items", input{Name: it.Name, Description: it.Description}, http.StatusCreated, &out)
	return out, err
}

func (s *HTTPStore) Update(ctx context.Context, it Item) (Item, error) {
	var out Item
	err := s.do(ctx, http.MethodPut, itemPath(it.ID), input{Name: it.Name, Description: it.Description}, http.StatusOK, &out)
	return out, err
}

func (s *HTTPStore) Delete(ctx context.Context, id string) error {
	return s.do(ctx, http.MethodDelete, itemPath(id), nil, http.StatusNoContent, nil)
}

func itemPath(id string) string {
	return "/items/" + url.PathEscape(id)
}

// do sends a request with an optional JSON body and decodes a JSON response
// into out (if non-nil). A JSON 404 becomes ErrNotFound; any other status
// than want is an error that includes the upstream's error message.
func (s *HTTPStore) do(ctx context.Context, method, path string, body any, want int, out any) error {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("upstream %s %s: encode request: %w", method, path, err)
		}
		reqBody = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, s.baseURL+path, reqBody)
	if err != nil {
		return fmt.Errorf("upstream %s %s: %w", method, path, err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("upstream %s %s: %w", method, path, err)
	}
	respBody := io.LimitReader(resp.Body, maxResponseBytes)
	defer func() {
		// Drain so the connection can be reused.
		_, _ = io.Copy(io.Discard, respBody)
		resp.Body.Close()
	}()

	if resp.StatusCode != want {
		// Only a JSON 404 means "no such item"; a plain-text 404 means the
		// route itself is missing, i.e. the base URL is misconfigured.
		if resp.StatusCode == http.StatusNotFound && isJSON(resp.Header) {
			return ErrNotFound
		}
		var e struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(respBody).Decode(&e)
		return fmt.Errorf("upstream %s %s: unexpected status %d: %q", method, path, resp.StatusCode, e.Error)
	}

	if out == nil {
		return nil
	}
	if err := json.NewDecoder(respBody).Decode(out); err != nil {
		return fmt.Errorf("upstream %s %s: decode response: %w", method, path, err)
	}
	return nil
}

func isJSON(h http.Header) bool {
	mt, _, _ := mime.ParseMediaType(h.Get("Content-Type"))
	return mt == "application/json"
}
