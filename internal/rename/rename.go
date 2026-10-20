// Package rename renames a resource in text: its names, singular and plural,
// in every case they're written in, as make resource does to copy the
// example resource and make init does to rename it. It's tooling, for
// cmd/newresource and cmd/initservice, never imported by the service.
package rename

import (
	"regexp"
	"strconv"
	"strings"
)

// The names every resource shares, which aren't the resource's own: a
// list's key in its JSON, and the field holding it (httpx.List).
const (
	ListKey   = "items"
	ListField = "Items"
)

// Renamer renames From (FromPlural) to To (ToPlural), keeping their case:
// item to order, Items to Orders, ITEM to ORDER, and the article before one
// ("an item" to "a category"). It leaves the names that aren't the
// resource's: a part of a longer lower-case word (omitempty has "item" in
// it), ListKey where it's quoted, and ListField where it's named as a field.
type Renamer struct {
	From, FromPlural, To, ToPlural string
	// Keep is text left as it is wherever it appears, such as the module's
	// path: a service called orders has a resource called order, and its
	// imports name both.
	Keep []string
}

// Text is s with the resource renamed.
func (r Renamer) Text(s string) string {
	if len(r.Keep) == 0 {
		return r.rename(s)
	}
	// Each kept text out of the way while the rest is renamed, as a marker
	// no name matches.
	pairs := make([]string, 0, 4*len(r.Keep))
	back := make([]string, 0, 2*len(r.Keep))
	for i, k := range r.Keep {
		mark := "\x00" + strconv.Itoa(i) + "\x00"
		pairs = append(pairs, k, mark)
		back = append(back, mark, k)
	}
	return strings.NewReplacer(back...).Replace(r.rename(strings.NewReplacer(pairs...).Replace(s)))
}

func (r Renamer) rename(s string) string {
	words := regexp.MustCompile(`(?i)(` + regexp.QuoteMeta(r.FromPlural) + `|` + regexp.QuoteMeta(r.From) + `)`)
	var b strings.Builder
	last := 0
	for _, m := range words.FindAllStringIndex(s, -1) {
		i, j := m[0], m[1]
		word := s[i:j]
		plural := strings.EqualFold(word, r.FromPlural)
		var prev, next byte
		if i > 0 {
			prev = s[i-1]
		}
		if j < len(s) {
			next = s[j]
		}
		if keep(s, word, prev, next, i, j) {
			continue
		}
		b.WriteString(s[last:i])
		repl := r.To
		if plural {
			repl = r.ToPlural
		}
		switch {
		case word == strings.ToUpper(word):
			repl = strings.ToUpper(repl)
		case word[0] >= 'A' && word[0] <= 'Z':
			repl = strings.ToUpper(repl[:1]) + repl[1:]
		}
		article(&b, repl)
		b.WriteString(repl)
		last = j
	}
	b.WriteString(s[last:])
	return b.String()
}

// keep reports whether word, s[i:j], isn't the resource's name.
func keep(s, word string, prev, next byte, i, j int) bool {
	switch {
	case word[0] >= 'a' && word[0] <= 'z' && isLetter(prev):
		return true // inside a word: omitempty
	case word == ListKey && listKey(s, i, j):
		return true
	case word == ListField && !isIdent(prev) && !isIdent(next):
		// The list's field, where it's named as one: x.Items, Items: ...,
		// Items []T.
		rest := strings.TrimLeft(s[j:], " \t")
		return prev == '.' || strings.HasPrefix(rest, ":") || strings.HasPrefix(rest, "[")
	}
	return false
}

// listKey reports whether s[i:j], ListKey, is the list's JSON key, as
// opposed to a resource's plural that happens to be quoted ("items" as a
// constant): a key ("items": or \"items\":), a path into it ("items.0.id",
// body.items[]), a struct tag (json:"items"), the start of a body
// ({"items"), a schema's required list (["items"]), or the key a test step
// names (array "items").
func listKey(s string, i, j int) bool {
	before, after := s[:i], s[j:]
	switch {
	case strings.HasSuffix(before, ".") && strings.HasPrefix(after, "["):
		return true
	case !strings.HasSuffix(before, `"`):
		return false
	case strings.HasPrefix(after, `":`), strings.HasPrefix(after, `\":`), strings.HasPrefix(after, "."):
		return true
	}
	b := strings.TrimSuffix(before, `"`)
	return strings.HasSuffix(b, "json:") || strings.HasSuffix(b, `json:\`) || strings.HasSuffix(b, "{") ||
		strings.HasSuffix(b, `{\`) || strings.HasSuffix(b, "[") || strings.HasSuffix(b, "array ")
}

// article makes the "a" or "an" b ends with, if any, the one word takes.
func article(b *strings.Builder, word string) {
	s := b.String()
	an := strings.ContainsRune("aeiouAEIOU", rune(word[0]))
	for _, a := range []string{"an ", "a ", "An ", "A "} {
		if !strings.HasSuffix(s, a) || (len(s) > len(a) && isLetter(s[len(s)-len(a)-1])) {
			continue
		}
		want := a[:1]
		if an {
			want += "n"
		}
		s = s[:len(s)-len(a)] + want + " "
		b.Reset()
		b.WriteString(s)
		return
	}
}

func isLetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func isIdent(c byte) bool  { return isLetter(c) || c >= '0' && c <= '9' || c == '_' }

// Module is the module path go.mod, in dir, declares: what a Renamer keeps.
func Module(gomod string) string {
	m := regexp.MustCompile(`(?m)^module\s+(\S+)`).FindStringSubmatch(gomod)
	if m == nil {
		return ""
	}
	return m[1]
}
