package middleware

import (
	"net/http"
	"regexp"

	"github.com/go-chi/chi/v5"
	"github.com/jaecopzm/zedstream/pkg/response"
)

var uuidRegex = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// UUIDIsValid checks if a string is a valid UUID v4.
func UUIDIsValid(s string) bool {
	return uuidRegex.MatchString(s)
}

// RequireValidUUID is a middleware that validates a URL param is a valid UUID.
func RequireValidUUID(param string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			val := chi.URLParam(r, param)
			if val == "" || !uuidRegex.MatchString(val) {
				response.BadRequest(w, "invalid "+param)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
