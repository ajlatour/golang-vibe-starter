// Package apikey authenticates requests by API key: a bearer token in the
// Authorization header, checked against a list of keys, each allowed to read
// or to read and write.
//
// Only a key's SHA-256 hash is configured, never the key itself, so the
// configuration can't be used to call the API. A fast hash is enough because
// keys are 256-bit random values (see Generate), not passwords: there is
// nothing to guess. Every configured hash is compared, in constant time, so
// how long a check takes says nothing about which key came close.
package apikey

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/httpx"
	"github.com/ajlatour/golang-vibe-starter/internal/logx"
)

// Scope is what a key may do.
type Scope int

const (
	// Read allows safe requests only: GET, HEAD and OPTIONS.
	Read Scope = iota + 1
	// Write allows every request.
	Write
)

func (s Scope) String() string {
	switch s {
	case Read:
		return "read"
	case Write:
		return "write"
	default:
		return "none"
	}
}

// Key is one configured key: a name for the logs, what it may do, the SHA-256
// hash of the key itself, and optionally a rate limit of its own.
type Key struct {
	Name  string
	Scope Scope
	Hash  [sha256.Size]byte
	// Rate and Burst, when Rate isn't zero, replace the per-client rate
	// limit (internal/limit) for requests made with this key.
	Rate  float64
	Burst int
}

// String describes a key for the logs: its name and scope, and its rate
// limit if it has one. Never the key.
func (k Key) String() string {
	if k.Rate == 0 {
		return fmt.Sprintf("%s (%s)", k.Name, k.Scope)
	}
	return fmt.Sprintf("%s (%s, %s/s in bursts of %d)", k.Name, k.Scope, strconv.FormatFloat(k.Rate, 'f', -1, 64), k.Burst)
}

// Keyring holds the keys in force. Replacing them is atomic, so a reload
// (WatchFile) never pauses or half-applies to a request.
type Keyring struct{ keys atomic.Pointer[[]Key] }

// NewKeyring returns a Keyring holding keys.
func NewKeyring(keys []Key) *Keyring {
	r := &Keyring{}
	r.Set(keys)
	return r
}

// Keys returns the keys in force.
func (r *Keyring) Keys() []Key { return *r.keys.Load() }

// Set replaces the keys in force.
func (r *Keyring) Set(keys []Key) { r.keys.Store(&keys) }

// Policy is the authentication configuration.
type Policy struct {
	// Off lets every request through without a key. Only for local
	// development and tests; the service logs a warning when it's set.
	Off bool
	// OpenReads lets safe requests (GET, HEAD, OPTIONS) through without a
	// key. A request that sends one anyway must send a valid one.
	OpenReads bool
	// Keys are the keys, unless Ring is set: then the keys in force are
	// Ring's, which can change while the service runs.
	Keys []Key
	Ring *Keyring
	// Exempt lists paths that never need a key, such as the healthcheck.
	Exempt []string
}

// keys returns the keys in force.
func (p Policy) keys() []Key {
	if p.Ring != nil {
		return p.Ring.Keys()
	}
	return p.Keys
}

// Validate reports a policy that would refuse every request: on, with no
// keys.
func (p Policy) Validate() error {
	if !p.Off && len(p.keys()) == 0 {
		return errors.New("no API keys: set API_KEYS or API_KEYS_FILE (make api-key generates one), or API_AUTH=off for local development only")
	}
	return nil
}

var (
	nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	hashRE = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// ParseKeys reads API_KEYS, or an API_KEYS_FILE: name:scope:hash entries,
// separated by commas or new lines, where scope is read or write and hash is
// the key's SHA-256 in lower-case hex. An entry may add a fourth field, its
// own rate limit: requests a second, and optionally a burst after a slash
// (ci:write:<hash>:500/1000); without a burst it's twice the rate. A line
// starting with # is a comment.
func ParseKeys(s string) ([]Key, error) {
	var keys []Key
	names := map[string]bool{}
	var entries []string
	for line := range strings.SplitSeq(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		entries = append(entries, strings.Split(line, ",")...)
	}
	n := 0
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		n++
		i := n - 1
		parts := strings.Split(entry, ":")
		if len(parts) != 3 && len(parts) != 4 {
			return nil, fmt.Errorf("entry %d: want name:scope:sha256, with an optional :rate[/burst], got %d fields", i+1, len(parts))
		}
		name, scope, hash := parts[0], parts[1], parts[2]
		if !nameRE.MatchString(name) {
			return nil, fmt.Errorf("entry %d: name %q: use up to 64 lower-case letters, digits, - and _", i+1, name)
		}
		if names[name] {
			return nil, fmt.Errorf("entry %d: name %q is used twice", i+1, name)
		}
		names[name] = true
		k := Key{Name: name}
		switch scope {
		case "read":
			k.Scope = Read
		case "write":
			k.Scope = Write
		default:
			return nil, fmt.Errorf("entry %d (%s): scope %q: want read or write", i+1, name, scope)
		}
		if !hashRE.MatchString(hash) {
			return nil, fmt.Errorf("entry %d (%s): want the key's SHA-256 as 64 lower-case hex digits, not the key itself", i+1, name)
		}
		if _, err := hex.Decode(k.Hash[:], []byte(hash)); err != nil {
			return nil, fmt.Errorf("entry %d (%s): %w", i+1, name, err)
		}
		if len(parts) == 4 {
			rate, burst, err := parseLimit(parts[3])
			if err != nil {
				return nil, fmt.Errorf("entry %d (%s): rate limit %q: %w", i+1, name, parts[3], err)
			}
			k.Rate, k.Burst = rate, burst
		}
		keys = append(keys, k)
	}
	return keys, nil
}

// parseLimit reads a key's rate limit: requests a second, optionally followed
// by /burst; the burst defaults to twice the rate, and at least 1.
func parseLimit(s string) (float64, int, error) {
	r, b, hasBurst := strings.Cut(s, "/")
	rate, err := strconv.ParseFloat(r, 64)
	if err != nil || rate <= 0 || math.IsInf(rate, 0) {
		return 0, 0, errors.New("want a positive number of requests a second")
	}
	burst := max(1, int(math.Ceil(2*rate)))
	if hasBurst {
		if burst, err = strconv.Atoi(b); err != nil || burst < 1 {
			return 0, 0, errors.New("want a burst of at least 1")
		}
	}
	return rate, burst, nil
}

// LoadFile reads the keys in an API_KEYS_FILE.
func LoadFile(path string) ([]Key, error) {
	b, err := os.ReadFile(path) // #nosec G304 -- API_KEYS_FILE, the operator's own configuration
	if err != nil {
		return nil, err
	}
	return ParseKeys(string(b))
}

// WatchFile reloads the keys in path into ring, checking every interval
// until ctx is done, and logs each change once. A change is acted on only
// once the file has read the same on two checks in a row, so a file caught
// half-written (a truncated list would still parse, and revoke keys) is never
// used. A file that can't be read or parsed, or holds no keys, changes
// nothing: the keys in force stay, so a bad edit can't lock every client out.
// It polls rather than waiting for events, which the standard library can't
// do, and which a mounted secret's in-place swap can escape.
func WatchFile(ctx context.Context, logger *slog.Logger, path string, ring *Keyring, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	w := fileWatcher{logger: logger, ring: ring}
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		b, err := os.ReadFile(path) // #nosec G304 -- API_KEYS_FILE, the operator's own configuration
		w.check(b, err)
	}
}

// fileWatcher decides, one check at a time, what a keys file's content means
// for the keys in force.
type fileWatcher struct {
	logger     *slog.Logger
	ring       *Keyring
	last, seen []byte // what was last acted on, and read on the last check
	lastErr    string
}

// check takes one reading of the file: its content, or why it couldn't be
// read.
func (w *fileWatcher) check(b []byte, err error) {
	var keys []Key
	if err == nil {
		stable := bytes.Equal(b, w.seen)
		w.seen = b
		if !stable || bytes.Equal(b, w.last) {
			return // still changing, or nothing new
		}
		keys, err = ParseKeys(string(b))
		if err == nil && len(keys) == 0 {
			err = errors.New("the file holds no keys")
		}
	}
	if err != nil {
		if err.Error() != w.lastErr {
			w.logger.Error("API keys not reloaded", logx.KeyError, err)
			w.lastErr = err.Error()
		}
		return
	}
	w.last, w.lastErr = b, ""
	if slices.Equal(keys, w.ring.Keys()) {
		return
	}
	w.ring.Set(keys)
	w.logger.Info("API keys reloaded", logx.KeyAPIKeys, Describe(keys))
}

// Describe lists keys for the logs: names, scopes and limits, never the keys.
func Describe(keys []Key) string {
	names := make([]string, len(keys))
	for i, k := range keys {
		names[i] = k.String()
	}
	return strings.Join(names, ", ")
}

// Generate returns a new random key and the API_KEYS entry for it.
func Generate(name string, scope Scope) (key, entry string, err error) {
	if !nameRE.MatchString(name) {
		return "", "", fmt.Errorf("name %q: use up to 64 lower-case letters, digits, - and _", name)
	}
	if scope != Read && scope != Write {
		return "", "", errors.New("scope must be read or write")
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	key = "gvs_" + base64.RawURLEncoding.EncodeToString(b)
	return key, fmt.Sprintf("%s:%s:%s", name, scope, HashHex(key)), nil
}

// HashHex is a key's SHA-256 in lower-case hex, as API_KEYS lists it.
func HashHex(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:])
}

// lookup returns the key whose hash matches presented, comparing against
// every key so the time taken doesn't depend on which matched. It points into
// the keys in force, which are never changed in place (a reload replaces the
// whole list), so a request keeps the key it matched.
func (p Policy) lookup(presented string) (*Key, bool) {
	h := sha256.Sum256([]byte(presented))
	keys := p.keys()
	found := -1
	for i := range keys {
		if subtle.ConstantTimeCompare(h[:], keys[i].Hash[:]) == 1 {
			found = i
		}
	}
	if found < 0 {
		return nil, false
	}
	return &keys[found], true
}

type keyCtxKey struct{}

// Name returns the name of the key the request was authenticated with, or ""
// if it had none.
func Name(ctx context.Context) string {
	if k, _ := ctx.Value(keyCtxKey{}).(*Key); k != nil {
		return k.Name
	}
	return ""
}

// Limits returns the rate limit of the key the request was authenticated
// with, if it has one of its own.
func Limits(ctx context.Context) (rate float64, burst int, ok bool) {
	if k, _ := ctx.Value(keyCtxKey{}).(*Key); k != nil && k.Rate > 0 {
		return k.Rate, k.Burst, true
	}
	return 0, 0, false
}

func safe(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}

// Middleware refuses requests without a valid key (401), and requests a key
// isn't allowed to make (403), before next sees them. Each refusal is logged
// once, with its reason and, for a 403, the key's name; never the key. An
// allowed request's later log lines all carry the key's name as api_key.
func Middleware(logger *slog.Logger, p Policy, next http.Handler) http.Handler {
	exempt := map[string]bool{}
	for _, path := range p.Exempt {
		exempt[path] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p.Off || exempt[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}
		header := r.Header.Get("Authorization")
		if header == "" && p.OpenReads && safe(r.Method) {
			next.ServeHTTP(w, r)
			return
		}

		refuse := func(status int, code httpx.Code, reason, msg string, attrs ...slog.Attr) {
			logger.LogAttrs(r.Context(), slog.LevelInfo, "request refused by authentication",
				append([]slog.Attr{slog.String(logx.KeyReason, reason), slog.Int(logx.KeyStatus, status)}, attrs...)...)
			if status == http.StatusUnauthorized {
				w.Header().Set("WWW-Authenticate", `Bearer realm="api"`)
			}
			httpx.WriteError(w, status, code, msg)
		}
		scheme, presented, _ := strings.Cut(header, " ")
		switch {
		case header == "":
			refuse(http.StatusUnauthorized, httpx.CodeAPIKeyRequired, "missing key", "an API key is required: send Authorization: Bearer <key>")
			return
		case !strings.EqualFold(scheme, "Bearer") || presented == "":
			refuse(http.StatusUnauthorized, httpx.CodeAPIKeyRequired, "not a bearer token", "send the API key as Authorization: Bearer <key>")
			return
		}
		k, ok := p.lookup(presented)
		if !ok {
			refuse(http.StatusUnauthorized, httpx.CodeAPIKeyInvalid, "unknown key", "the API key is not valid")
			return
		}
		if k.Scope != Write && !safe(r.Method) {
			refuse(http.StatusForbidden, httpx.CodeReadOnlyKey, "read-only key", "this API key may only read", slog.String(logx.KeyAPIKey, k.Name))
			return
		}
		// Every later log line of the request names the key, those written
		// outside this handler (such as "response sent") included.
		logx.AddAttrs(r.Context(), slog.String(logx.KeyAPIKey, k.Name))
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), keyCtxKey{}, k)))
	})
}
