package middleware

import (
	"net/http"
	"time"

	"github.com/go-chi/httprate"
	"github.com/jaecopzm/zedstream/pkg/response"
)

// RateLimit returns a per-IP rate limiter middleware.
func RateLimit(requests int, window time.Duration) func(http.Handler) http.Handler {
	return httprate.Limit(
		requests,
		window,
		httprate.WithKeyFuncs(httprate.KeyByIP),
		httprate.WithLimitHandler(func(w http.ResponseWriter, r *http.Request) {
			response.TooManyRequests(w, "rate limit exceeded, please slow down")
		}),
	)
}

// RateLimitByUser returns a per-authenticated-user rate limiter.
func RateLimitByUser(requests int, window time.Duration) func(http.Handler) http.Handler {
	return httprate.Limit(
		requests,
		window,
		httprate.WithKeyFuncs(func(r *http.Request) (string, error) {
			userID := UserIDFromContext(r.Context())
			if userID != "" {
				return "user:" + userID, nil
			}
			return httprate.KeyByIP(r)
		}),
		httprate.WithLimitHandler(func(w http.ResponseWriter, r *http.Request) {
			response.TooManyRequests(w, "rate limit exceeded")
		}),
	)
}
