package streaming

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jaecopzm/zedstream/pkg/middleware"
	"github.com/jaecopzm/zedstream/pkg/response"
	"github.com/jaecopzm/zedstream/pkg/storage"
)

const signedURLExpiry = 2 * time.Hour

// Handler handles music streaming endpoints.
type Handler struct {
	db          *pgxpool.Pool
	storage     *storage.Client
	audioBucket string
}

// NewHandler creates a new streaming handler.
func NewHandler(db *pgxpool.Pool, store *storage.Client, audioBucket string) *Handler {
	return &Handler{db: db, storage: store, audioBucket: audioBucket}
}

// StreamTrack returns a short-lived signed URL for streaming a track.
//
// @Summary     Get streaming URL for a track
// @Tags        streaming
// @Security    BearerAuth
// @Param       id path string true "Track ID"
// @Router      /tracks/{id}/stream [get]
func (h *Handler) StreamTrack(w http.ResponseWriter, r *http.Request) {
	trackID := chi.URLParam(r, "id")
	userID := middleware.UserIDFromContext(r.Context())

	// Fetch track audio key and verify it's published
	var audioKey, status string
	err := h.db.QueryRow(r.Context(),
		`SELECT audio_key, status FROM tracks WHERE id = $1`, trackID,
	).Scan(&audioKey, &status)
	if err != nil {
		response.NotFound(w, "track not found")
		return
	}

	if status != "published" {
		response.NotFound(w, "track is not available")
		return
	}

	// Generate signed URL
	signedURL, err := h.storage.GetSignedURL(r.Context(), h.audioBucket, audioKey, signedURLExpiry)
	if err != nil {
		response.InternalServerError(w, "failed to generate stream URL")
		return
	}

	// Record play event (non-blocking with timeout)
	go h.recordPlayEvent(userID, trackID)

	response.OK(w, map[string]any{
		"url":        signedURL,
		"expires_in": int(signedURLExpiry.Seconds()),
	})
}

// RecordPlayProgress records how long a user listened (called by client on pause/stop).
//
// @Summary     Record play progress
// @Tags        streaming
// @Security    BearerAuth
// @Param       id path string true "Track ID"
// @Router      /tracks/{id}/play [post]
func (h *Handler) RecordPlayProgress(w http.ResponseWriter, r *http.Request) {
	trackID := chi.URLParam(r, "id")
	userID := middleware.UserIDFromContext(r.Context())

	var body struct {
		DurationListened int `json:"duration_listened"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		response.BadRequest(w, "invalid request body")
		return
	}

	go h.updatePlayDuration(userID, trackID, body.DurationListened)
	response.NoContent(w)
}

func (h *Handler) recordPlayEvent(userID, trackID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, _ = h.db.Exec(ctx,
		`INSERT INTO play_events (user_id, track_id) VALUES ($1, $2)`,
		userID, trackID,
	)
	_, _ = h.db.Exec(ctx,
		`UPDATE tracks SET play_count = play_count + 1 WHERE id = $1`, trackID,
	)
}

func (h *Handler) updatePlayDuration(userID, trackID string, duration int) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, _ = h.db.Exec(ctx, `
		UPDATE play_events
		SET duration_listened = $3
		WHERE id = (
		    SELECT id FROM play_events
		    WHERE user_id = $1 AND track_id = $2
		    ORDER BY played_at DESC LIMIT 1
		)
	`, userID, trackID, duration)
}
