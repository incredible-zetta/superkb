package http

import (
	"crypto/subtle"
	"net/http"
)

// BasicAuth returns middleware enforcing HTTP basic auth with a single set of
// service credentials. Comparisons are constant-time to avoid timing leaks.
// When enabled is false the middleware is a no-op (useful for local dev).
func BasicAuth(enabled bool, username, password string) func(http.Handler) http.Handler {
	wantUser := []byte(username)
	wantPass := []byte(password)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !enabled {
				next.ServeHTTP(w, r)
				return
			}
			user, pass, ok := r.BasicAuth()
			if !ok || !constantTimeEqual([]byte(user), wantUser) || !constantTimeEqual([]byte(pass), wantPass) {
				w.Header().Set("WWW-Authenticate", `Basic realm="superkb", charset="UTF-8"`)
				writeError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// constantTimeEqual compares two byte slices without leaking length via early
// exit on differing lengths (subtle.ConstantTimeCompare returns 0 on length
// mismatch).
func constantTimeEqual(a, b []byte) bool {
	return subtle.ConstantTimeCompare(a, b) == 1
}
