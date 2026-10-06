package httpx

import (
	"net/http"
	"slices"
	"strings"
)

// APIVersionHeader carries the API version: a client may send it to say which
// version it's written for, and every response carries the version that
// answered. Versioning is by header, not by path, so a version never changes
// a URL (ADR-0036).
const APIVersionHeader = "API-Version"

// APIVersions are the versions the API serves, oldest first; the last is the
// current one, which a request without the header gets.
var APIVersions = []string{"1"}

// CurrentAPIVersion is the version a request without API-Version gets.
func CurrentAPIVersion() string { return APIVersions[len(APIVersions)-1] }

// APIVersion refuses a request for a version the API doesn't serve, with 400
// and unsupported_api_version, before anything else reads it. Paths in
// exempt (the probes) are never refused. A request without the header is for
// the current version. Every response says which version answered: the
// standard middleware sets API-Version (see defensiveHeaders), so the
// responses refused before this runs carry it too.
//
// It doesn't record the version on the request, which would cost every
// request an allocation, while there's one version to serve: when there are
// more, a handler that differs between them reads the header itself.
func APIVersion(exempt []string, next http.Handler) http.Handler {
	msg := "unsupported API-Version: this API serves " + strings.Join(APIVersions, ", ") + "; leave it out for " + CurrentAPIVersion()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Indexed by the canonical name: Header.Get would canonicalize
		// "API-Version", allocating, on every request.
		if v := r.Header[canonicalAPIVersion]; len(v) > 0 && !slices.Contains(APIVersions, v[0]) && !slices.Contains(exempt, r.URL.Path) {
			WriteError(w, http.StatusBadRequest, CodeUnsupportedAPIVersion, msg)
			return
		}
		next.ServeHTTP(w, r)
	})
}

var (
	canonicalAPIVersion = http.CanonicalHeaderKey(APIVersionHeader)
	currentAPIVersion   = []string{CurrentAPIVersion()} // shared, like the other defensive headers
)
