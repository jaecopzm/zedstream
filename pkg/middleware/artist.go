package middleware

import (
	"context"
	"net/http"

	"github.com/jaecopzm/zedstream/pkg/response"
)

type artistContextKey string

const artistIDKey artistContextKey = "artist_id"

// ArtistContext injects the artist_id from JWT claims into the request context.
// Requires Authenticate middleware to have run first.
func ArtistContext() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := ClaimsFromContext(r.Context())
			if claims == nil {
				response.Unauthorized(w, "not authenticated")
				return
			}
			if claims.ArtistID == "" {
				response.Forbidden(w, "artist profile required")
				return
			}

			ctx := context.WithValue(r.Context(), artistIDKey, claims.ArtistID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// ArtistIDFromContext retrieves the artist ID from the request context.
func ArtistIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(artistIDKey).(string)
	return id
}
