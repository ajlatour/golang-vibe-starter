package httpx

import (
	"net/http"
	"slices"
	"strings"
)

// HandleUnmatched makes mux answer the requests none of its routes match
// in JSON, with a code, like every other error: 404, not_found, for a path
// no route has; 405, method_not_allowed, with Allow, for a path that has
// routes, but not for the method; and 400, invalid_target, for a target
// that isn't a path at all (OPTIONS *). ServeMux's own answers are plain
// text, "404 page not found".
//
// It registers a catch-all route, "/", so call it after the others; mux
// must not have one already. A matched route costs nothing more: the
// catch-all only runs for what nothing else matched, and works out the 405
// then. Its requests are logged with no route, as ServeMux's own were.
func HandleUnmatched(mux *http.ServeMux) {
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		r.Pattern = "" // matched no route of the API's: logged and counted as such
		if allow := allowedMethods(mux, r); len(allow) > 0 {
			w.Header().Set("Allow", strings.Join(allow, ", "))
			WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
			return
		}
		WriteError(w, http.StatusNotFound, CodeNotFound, "not found")
	})
}

// RejectInvalidTarget answers a request whose target isn't a path (OPTIONS
// *, the only one HTTP/1.1 allows besides CONNECT's) with 400 and
// invalid_target, in JSON, before it reaches a router: ServeMux answers one
// with a bare 400 and no body. The server must pass such requests on (see
// Serve: DisableGeneralOptionsHandler), or it answers OPTIONS * itself,
// unlogged.
func RejectInvalidTarget(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "" || r.URL.Path[0] != '/' {
			WriteError(w, http.StatusBadRequest, CodeInvalidTarget, "the request target must be a path")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// routeMethods are the methods a 405 can offer: those the API's routes use.
var routeMethods = []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch}

// allowedMethods lists the methods some route of mux other than the
// catch-all serves r's path with, HEAD with GET as ServeMux does.
func allowedMethods(mux *http.ServeMux, r *http.Request) []string {
	var allow []string
	for _, m := range routeMethods {
		probe := &http.Request{Method: m, URL: r.URL, Host: r.Host}
		if _, pattern := mux.Handler(probe); pattern != "" && pattern != "/" {
			allow = append(allow, m)
			if m == http.MethodGet {
				allow = append(allow, http.MethodHead)
			}
		}
	}
	if slices.Contains(allow, r.Method) {
		return nil // routed after all: not ours to refuse
	}
	return allow
}
