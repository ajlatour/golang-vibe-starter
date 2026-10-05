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
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

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

// Key is one configured key: a name for the logs, what it may do, and the
// SHA-256 hash of the key itself.
type Key struct {
	Name  string
	Scope Scope
	Hash  [sha256.Size]byte
}

// Policy is the authentication configuration.
type Policy struct {
	// Off lets every request through without a key. Only for local
	// development and tests; the service logs a warning when it's set.
	Off bool
	// OpenReads lets safe requests (GET, HEAD, OPTIONS) through without a
	// key. A request that sends one anyway must send a valid one.
	OpenReads bool
	Keys      []Key
	// Exempt lists paths that never need a key, such as the healthcheck.
	Exempt []string
}

// Validate reports a policy that would refuse every request: on, with no
// keys.
func (p Policy) Validate() error {
	if !p.Off && len(p.Keys) == 0 {
		return errors.New("no API keys: set API_KEYS (make api-key generates one), or API_AUTH=off for local development only")
	}
	return nil
}

var (
	nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	hashRE = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// ParseKeys reads API_KEYS: comma-separated name:scope:hash entries, where
// scope is read or write and hash is the key's SHA-256 in lower-case hex.
func ParseKeys(s string) ([]Key, error) {
	var keys []Key
	names := map[string]bool{}
	for i, entry := range strings.Split(s, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		parts := strings.Split(entry, ":")
		if len(parts) != 3 {
			return nil, fmt.Errorf("entry %d: want name:scope:sha256, got %d fields", i+1, len(parts))
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
		hex.Decode(k.Hash[:], []byte(hash))
		keys = append(keys, k)
	}
	return keys, nil
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
// every key so the time taken doesn't depend on which matched.
func (p Policy) lookup(presented string) (Key, bool) {
	h := sha256.Sum256([]byte(presented))
	var found Key
	ok := 0
	for _, k := range p.Keys {
		if subtle.ConstantTimeCompare(h[:], k.Hash[:]) == 1 {
			found, ok = k, 1
		}
	}
	return found, ok == 1
}

type keyNameKey struct{}

// Name returns the name of the key the request was authenticated with, or ""
// if it had none.
func Name(ctx context.Context) string {
	n, _ := ctx.Value(keyNameKey{}).(string)
	return n
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

		refuse := func(status int, reason, msg string, attrs ...slog.Attr) {
			logger.LogAttrs(r.Context(), slog.LevelInfo, "request refused by authentication",
				append([]slog.Attr{slog.String(logx.KeyReason, reason), slog.Int(logx.KeyStatus, status)}, attrs...)...)
			if status == http.StatusUnauthorized {
				w.Header().Set("WWW-Authenticate", `Bearer realm="api"`)
			}
			httpx.WriteError(w, status, msg)
		}
		scheme, presented, _ := strings.Cut(header, " ")
		switch {
		case header == "":
			refuse(http.StatusUnauthorized, "missing key", "an API key is required: send Authorization: Bearer <key>")
			return
		case !strings.EqualFold(scheme, "Bearer") || presented == "":
			refuse(http.StatusUnauthorized, "not a bearer token", "send the API key as Authorization: Bearer <key>")
			return
		}
		k, ok := p.lookup(presented)
		if !ok {
			refuse(http.StatusUnauthorized, "unknown key", "the API key is not valid")
			return
		}
		if k.Scope != Write && !safe(r.Method) {
			refuse(http.StatusForbidden, "read-only key", "this API key may only read", slog.String(logx.KeyAPIKey, k.Name))
			return
		}
		// Every later log line of the request names the key, those written
		// outside this handler (such as "response sent") included.
		logx.AddAttrs(r.Context(), slog.String(logx.KeyAPIKey, k.Name))
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), keyNameKey{}, k.Name)))
	})
}
