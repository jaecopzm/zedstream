package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/jaecopzm/zedstream/internal/auth"
	"github.com/jaecopzm/zedstream/pkg/response"
)

// contextKey is a private type for context keys to avoid collisions.
type contextKey string

const (
	claimsKey contextKey = "auth_claims"
)

// TryAuthenticate optionally parses the JWT token and injects claims into context,
// but does NOT return an error if no token is provided. Invalid tokens are silently ignored.
func TryAuthenticate(authSvc *auth.Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader != "" {
				parts := strings.SplitN(authHeader, " ", 2)
				if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
					if claims, err := authSvc.ValidateAccessToken(parts[1]); err == nil {
						ctx := context.WithValue(r.Context(), claimsKey, claims)
						next.ServeHTTP(w, r.WithContext(ctx))
						return
					}
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Authenticate validates the JWT Bearer token and injects claims into context.
func Authenticate(authSvc *auth.Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				response.Unauthorized(w, "authorization header required")
				return
			}

			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
				response.Unauthorized(w, "invalid authorization header format")
				return
			}

			claims, err := authSvc.ValidateAccessToken(parts[1])
			if err != nil {
				response.Unauthorized(w, "invalid or expired token")
				return
			}

			ctx := context.WithValue(r.Context(), claimsKey, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireRole returns a middleware that enforces one of the allowed roles.
func RequireRole(roles ...string) func(http.Handler) http.Handler {
	allowed := make(map[string]bool, len(roles))
	for _, r := range roles {
		allowed[r] = true
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := ClaimsFromContext(r.Context())
			if claims == nil {
				response.Unauthorized(w, "not authenticated")
				return
			}

			if !allowed[claims.Role] {
				response.Forbidden(w, "insufficient permissions")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// ClaimsFromContext retrieves auth claims from the request context.
func ClaimsFromContext(ctx context.Context) *auth.Claims {
	claims, _ := ctx.Value(claimsKey).(*auth.Claims)
	return claims
}

// UserIDFromContext is a convenience helper to extract the user ID.
func UserIDFromContext(ctx context.Context) string {
	if c := ClaimsFromContext(ctx); c != nil {
		return c.UserID
	}
	return ""
}

// RoleFromContext extracts the user role from context.
func RoleFromContext(ctx context.Context) string {
	if c := ClaimsFromContext(ctx); c != nil {
		return c.Role
	}
	return ""
}
