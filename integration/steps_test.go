package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cucumber/godog"
)

// world is one scenario's state: the stack it talks to and the last response
// it got. Godog calls initScenario for every scenario, so nothing carries over
// from one to the next.
type world struct {
	stack  stack
	client *http.Client

	status int
	header http.Header
	body   []byte
}

func initScenario(sc *godog.ScenarioContext, s stack) {
	w := &world{stack: s, client: &http.Client{Timeout: 15 * time.Second}}

	// Start every scenario from the mounted WireMock stubs, dropping any
	// fault a previous scenario (or a person) switched on.
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, w.resetUpstream()
	})

	sc.Step(`^I (GET|DELETE) "([^"]*)"$`, w.send)
	sc.Step(`^I (POST|PUT) "([^"]*)" with body:$`, w.sendBody)
	sc.Step(`^I (POST|PUT) "([^"]*)" with a name of (\d+) characters$`, w.sendNameOfLength)

	sc.Step(`^the response status is (\d+)$`, w.statusIs)
	sc.Step(`^the response header "([^"]*)" is "([^"]*)"$`, w.headerIs)
	sc.Step(`^the response body is empty$`, w.bodyIsEmpty)
	sc.Step(`^the JSON field "([^"]*)" is "([^"]*)"$`, w.fieldIsString)
	sc.Step(`^the JSON field "([^"]*)" is (-?\d+)$`, w.fieldIsNumber)
	sc.Step(`^the JSON field "([^"]*)" contains "([^"]*)"$`, w.fieldContains)
	sc.Step(`^the JSON field "([^"]*)" is absent$`, w.fieldIsAbsent)
	sc.Step(`^the JSON array "([^"]*)" has (\d+) elements?$`, w.arrayHasLength)
}

// --- the stack -------------------------------------------------------------

func (w *world) resetUpstream() error {
	resp, err := w.client.Post(w.stack.wiremock+"/__admin/mappings/reset", "application/json", nil)
	if err != nil {
		return fmt.Errorf("reset WireMock: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("reset WireMock: %s", resp.Status)
	}
	return nil
}

// --- requests ----------------------------------------------------------------

func (w *world) do(method, path string, body io.Reader) error {
	req, err := http.NewRequest(method, w.stack.api+path, body)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := w.client.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	w.status, w.header = resp.StatusCode, resp.Header
	w.body, err = io.ReadAll(resp.Body)
	return err
}

func (w *world) send(method, path string) error {
	return w.do(method, path, nil)
}

func (w *world) sendBody(method, path string, doc *godog.DocString) error {
	return w.do(method, path, strings.NewReader(doc.Content))
}

func (w *world) sendNameOfLength(method, path string, n int) error {
	body, _ := json.Marshal(map[string]string{"name": strings.Repeat("a", n)})
	return w.do(method, path, bytes.NewReader(body))
}

// --- assertions --------------------------------------------------------------

func (w *world) statusIs(want int) error {
	if w.status != want {
		return fmt.Errorf("status = %d, want %d (body: %s)", w.status, want, w.bodyForError())
	}
	return nil
}

func (w *world) headerIs(name, want string) error {
	if got := w.header.Get(name); got != want {
		return fmt.Errorf("header %s = %q, want %q", name, got, want)
	}
	return nil
}

func (w *world) bodyIsEmpty() error {
	if len(w.body) != 0 {
		return fmt.Errorf("body = %s, want it empty", w.bodyForError())
	}
	return nil
}

func (w *world) fieldIsString(path, want string) error {
	v, err := w.field(path)
	if err != nil {
		return err
	}
	if s, ok := v.(string); !ok || s != want {
		return fmt.Errorf("%s = %#v, want %q (body: %s)", path, v, want, w.bodyForError())
	}
	return nil
}

func (w *world) fieldIsNumber(path string, want int) error {
	v, err := w.field(path)
	if err != nil {
		return err
	}
	if f, ok := v.(float64); !ok || f != float64(want) {
		return fmt.Errorf("%s = %#v, want %d (body: %s)", path, v, want, w.bodyForError())
	}
	return nil
}

func (w *world) fieldContains(path, want string) error {
	v, err := w.field(path)
	if err != nil {
		return err
	}
	if s, ok := v.(string); !ok || !strings.Contains(s, want) {
		return fmt.Errorf("%s = %#v, want it to contain %q", path, v, want)
	}
	return nil
}

func (w *world) fieldIsAbsent(path string) error {
	if v, err := w.field(path); err == nil {
		return fmt.Errorf("%s = %#v, want it absent (body: %s)", path, v, w.bodyForError())
	}
	return nil
}

func (w *world) arrayHasLength(path string, want int) error {
	v, err := w.field(path)
	if err != nil {
		return err
	}
	arr, ok := v.([]any)
	if !ok {
		return fmt.Errorf("%s is %T, want an array", path, v)
	}
	if len(arr) != want {
		return fmt.Errorf("%s has %d elements, want %d", path, len(arr), want)
	}
	return nil
}

// field returns the value at a dotted path in the JSON body, such as
// "items.0.id" (array elements by index).
func (w *world) field(path string) (any, error) {
	var cur any
	if err := json.Unmarshal(w.body, &cur); err != nil {
		return nil, fmt.Errorf("body is not JSON (%v): %s", err, w.bodyForError())
	}
	for _, part := range strings.Split(path, ".") {
		switch node := cur.(type) {
		case map[string]any:
			v, ok := node[part]
			if !ok {
				return nil, fmt.Errorf("%s: no field %q (body: %s)", path, part, w.bodyForError())
			}
			cur = v
		case []any:
			i, err := strconv.Atoi(part)
			if err != nil || i < 0 || i >= len(node) {
				return nil, fmt.Errorf("%s: no element %q in an array of %d", path, part, len(node))
			}
			cur = node[i]
		default:
			return nil, fmt.Errorf("%s: %q is inside a %T, not an object or array", path, part, cur)
		}
	}
	return cur, nil
}

// bodyForError keeps failure messages readable when a body is large.
func (w *world) bodyForError() string {
	const max = 300
	if len(w.body) > max {
		return string(w.body[:max]) + "…"
	}
	return string(w.body)
}
