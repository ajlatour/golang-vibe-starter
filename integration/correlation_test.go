package integration

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
)

// The correlation ID's header name and format come from the API's OpenAPI
// spec, not from a copy here, so these scenarios, the spec and the service
// all have to agree.
var correlation = sync.OnceValues(func() (correlationRules, error) {
	b, err := os.ReadFile("../docs/openapi.json")
	if err != nil {
		return correlationRules{}, err
	}
	var doc struct {
		Components struct {
			Parameters map[string]struct {
				Name   string `json:"name"`
				Schema struct {
					Pattern string `json:"pattern"`
				} `json:"schema"`
			} `json:"parameters"`
		} `json:"components"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return correlationRules{}, fmt.Errorf("docs/openapi.json: %w", err)
	}
	p, ok := doc.Components.Parameters["CorrelationID"]
	if !ok || p.Name == "" || p.Schema.Pattern == "" {
		return correlationRules{}, fmt.Errorf("docs/openapi.json has no CorrelationID parameter with a name and pattern")
	}
	re, err := regexp.Compile(p.Schema.Pattern)
	if err != nil {
		return correlationRules{}, fmt.Errorf("CorrelationID pattern: %w", err)
	}
	return correlationRules{header: p.Name, valid: re}, nil
})

type correlationRules struct {
	header string         // Client-Correlation-Id
	valid  *regexp.Regexp // what the service accepts, and what it generates
}

func (w *world) sendCorrelationID(id string) error {
	rules, err := correlation()
	if err != nil {
		return err
	}
	w.sentID = id
	w.reqHeader.Set(rules.header, id)
	return nil
}

func (w *world) sendCorrelationIDOfLength(n int) error {
	return w.sendCorrelationID(strings.Repeat("a", n))
}

// correlationIDEchoed checks the response carries exactly the ID sent.
func (w *world) correlationIDEchoed() error {
	rules, err := correlation()
	if err != nil {
		return err
	}
	if got := w.header.Get(rules.header); got != w.sentID {
		return fmt.Errorf("%s = %q, want the one sent, %q", rules.header, got, w.sentID)
	}
	return nil
}

// correlationIDGenerated checks the response carries an ID the service made
// up: present, in the documented format, and not the one sent (if any).
func (w *world) correlationIDGenerated() error {
	rules, err := correlation()
	if err != nil {
		return err
	}
	got := w.header.Get(rules.header)
	switch {
	case got == "":
		return fmt.Errorf("the response has no %s header", rules.header)
	case w.sentID != "" && got == w.sentID:
		return fmt.Errorf("%s = %q: the invalid ID sent was echoed back instead of replaced", rules.header, got)
	case !rules.valid.MatchString(got):
		return fmt.Errorf("%s = %q doesn't match the documented format %s", rules.header, got, rules.valid)
	}
	return nil
}

// correlationIDsDiffer checks the last two responses carried different IDs,
// so generated IDs aren't reused across requests.
func (w *world) correlationIDsDiffer() error {
	rules, err := correlation()
	if err != nil {
		return err
	}
	prev, last := w.prevHeader.Get(rules.header), w.header.Get(rules.header)
	if prev == "" || last == "" {
		return fmt.Errorf("need two responses with %s, got %q and %q", rules.header, prev, last)
	}
	if prev == last {
		return fmt.Errorf("both responses had %s %q, want a fresh one each time", rules.header, last)
	}
	return nil
}
