package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
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

	reqHeader     http.Header // sent with every request in the scenario
	noContentType bool        // send a body without a Content-Type
	sentID        string      // the correlation ID the scenario sent, if any

	status     int
	header     http.Header
	prevHeader http.Header // the response before the last one
	body       []byte
	elapsed    time.Duration

	flagsChanged bool // a step edited the flags, so restore them afterwards
}

func initScenario(sc *godog.ScenarioContext, s stack) {
	w := &world{stack: s, client: &http.Client{Timeout: 15 * time.Second}, reqHeader: http.Header{}}

	// Start every scenario from the mounted WireMock stubs, dropping any
	// fault a previous scenario (or a person) switched on.
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, w.resetUpstream()
	})
	// Put back any flag a scenario switched, so the next one starts from the
	// shipped defaults.
	// Godog already reports a failed step; returning its error from here
	// would report it again as a hook failure, so only a failed reset is
	// returned.
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		if !w.flagsChanged {
			return ctx, nil
		}
		return ctx, resetFlags(w.stack)
	})

	sc.Step(`^the upstream fault "([^"]+)" is active$`, w.activateFault)
	sc.Step(`^the feature flag "([^"]+)" is (on|off)$`, w.setFlag)

	sc.Step(`^I send the correlation ID "([^"]*)"$`, w.sendCorrelationID)
	sc.Step(`^I send a correlation ID of (\d+) characters$`, w.sendCorrelationIDOfLength)
	// A quote can't go inside the quoted argument of the step above.
	sc.Step(`^I send a correlation ID with a quote in it$`, func() error { return w.sendCorrelationID(`quote"inside`) })
	sc.Step(`^I (GET|DELETE) "([^"]*)"$`, w.send)
	sc.Step(`^I (POST|PUT) "([^"]*)" with body:$`, w.sendBody)
	sc.Step(`^I (POST|PUT) "([^"]*)" with a name of (\d+) characters$`, w.sendNameOfLength)
	sc.Step(`^I (POST|PUT) "([^"]*)" as "([^"]+)" with body:$`, w.sendBodyAs)
	sc.Step(`^I (POST|PUT) "([^"]*)" without a content type, with body:$`, w.sendBodyWithoutContentType)
	sc.Step(`^I send the header "([^"]+)" with value "([^"]*)"$`, w.addHeader)
	sc.Step(`^I send a header "([^"]+)" of (\d+) bytes$`, w.addHeaderOfSize)
	sc.Step(`^I send (\d+) extra headers$`, w.addExtraHeaders)

	sc.Step(`^the response status is (\d+)$`, w.statusIs)
	sc.Step(`^the response header "([^"]*)" is "([^"]*)"$`, w.headerIs)
	sc.Step(`^the response body is empty$`, w.bodyIsEmpty)
	sc.Step(`^the JSON field "([^"]*)" is "([^"]*)"$`, w.fieldIsString)
	sc.Step(`^the JSON field "([^"]*)" is (-?\d+)$`, w.fieldIsNumber)
	sc.Step(`^the JSON field "([^"]*)" contains "([^"]*)"$`, w.fieldContains)
	sc.Step(`^the JSON field "([^"]*)" is absent$`, w.fieldIsAbsent)
	sc.Step(`^the JSON array "([^"]*)" has (\d+) elements?$`, w.arrayHasLength)
	sc.Step(`^the correlation ID I sent is echoed back$`, w.correlationIDEchoed)
	sc.Step(`^the response has a generated correlation ID$`, w.correlationIDGenerated)
	sc.Step(`^the last two responses have different correlation IDs$`, w.correlationIDsDiffer)
	sc.Step(`^the response took less than (\d+(?:\.\d+)?) seconds?$`, w.tookLessThan)
	sc.Step(`^the response took at least (\d+(?:\.\d+)?) seconds?$`, w.tookAtLeast)
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

// activateFault loads one of WireMock's fault stubs (wiremock/faults/<name>.json)
// through its admin API. Each has a fixed ID and the top priority, so it
// overrides the matching normal stub until the next scenario's reset.
func (w *world) activateFault(name string) error {
	stub, err := os.ReadFile(filepath.Join("..", "wiremock", "faults", name+".json"))
	if err != nil {
		return fmt.Errorf("no fault stub named %q: %w", name, err)
	}
	resp, err := w.client.Post(w.stack.wiremock+"/__admin/mappings", "application/json", bytes.NewReader(stub))
	if err != nil {
		return fmt.Errorf("load fault %q: %w", name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("load fault %q: %s: %s", name, resp.Status, body)
	}
	return nil
}

func (w *world) setFlag(key, state string) error {
	w.flagsChanged = true
	return setFlag(w.stack, key, state == "on")
}

// --- requests ----------------------------------------------------------------

func (w *world) do(method, path string, body io.Reader) error {
	req, err := http.NewRequest(method, w.stack.api+path, body)
	if err != nil {
		return err
	}
	// A body is sent as JSON unless the scenario chose another Content-Type
	// (or none), which the headers below then set.
	if body != nil && !w.noContentType {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range w.reqHeader {
		req.Header[k] = v
	}
	start := time.Now()
	resp, err := w.client.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	w.prevHeader = w.header
	w.status, w.header = resp.StatusCode, resp.Header
	w.body, err = io.ReadAll(resp.Body)
	w.elapsed = time.Since(start)
	return err
}

func (w *world) send(method, path string) error {
	return w.do(method, path, nil)
}

func (w *world) sendBody(method, path string, doc *godog.DocString) error {
	return w.do(method, path, strings.NewReader(doc.Content))
}

func (w *world) sendBodyAs(method, path, contentType string, doc *godog.DocString) error {
	w.reqHeader.Set("Content-Type", contentType)
	defer w.reqHeader.Del("Content-Type")
	return w.do(method, path, strings.NewReader(doc.Content))
}

func (w *world) sendBodyWithoutContentType(method, path string, doc *godog.DocString) error {
	w.noContentType = true
	defer func() { w.noContentType = false }()
	return w.do(method, path, strings.NewReader(doc.Content))
}

// addHeader adds a header to every request in the scenario; adding the same
// name twice sends it twice.
func (w *world) addHeader(name, value string) error {
	w.reqHeader.Add(name, value)
	return nil
}

func (w *world) addHeaderOfSize(name string, n int) error {
	w.reqHeader.Set(name, strings.Repeat("a", n))
	return nil
}

func (w *world) addExtraHeaders(n int) error {
	for i := range n {
		w.reqHeader.Set(fmt.Sprintf("X-Extra-%d", i), "v")
	}
	return nil
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

func (w *world) tookLessThan(seconds float64) error {
	if limit := time.Duration(seconds * float64(time.Second)); w.elapsed >= limit {
		return fmt.Errorf("response took %s, want less than %s", w.elapsed.Round(time.Millisecond), limit)
	}
	return nil
}

func (w *world) tookAtLeast(seconds float64) error {
	if limit := time.Duration(seconds * float64(time.Second)); w.elapsed < limit {
		return fmt.Errorf("response took %s, want at least %s", w.elapsed.Round(time.Millisecond), limit)
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
