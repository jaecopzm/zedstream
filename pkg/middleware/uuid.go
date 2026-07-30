package middleware

import (
	"net/http"
	"regexp"

	"github.com/go-chi/chi/v5"
	"github.com/jaecopzm/zedstream/pkg/response"
)

var idRegex = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$|^[0-9A-Za-z]{12,21}$`)

// IDIsValid checks if a string is a valid UUID or NanoID.
func IDIsValid(s string) bool {
	return idRegex.MatchString(s)
}

// RequireValidID is a middleware that validates a URL param is a valid UUID or NanoID.
func RequireValidID(param string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			val := chi.URLParam(r, param)
			if val == "" || !idRegex.MatchString(val) {
				response.BadRequest(w, "invalid "+param)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
