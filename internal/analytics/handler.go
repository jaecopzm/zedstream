package analytics

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jaecopzm/zedstream/pkg/id"
	"github.com/jaecopzm/zedstream/pkg/middleware"
	"github.com/jaecopzm/zedstream/pkg/response"
)

// Handler serves analytics endpoints for artists.
type Handler struct {
	db *pgxpool.Pool
}

// NewHandler creates a new analytics handler.
func NewHandler(db *pgxpool.Pool) *Handler {
	return &Handler{db: db}
}

// Overview returns a high-level summary for the authenticated artist.
//
// @Summary     Artist analytics overview
// @Tags        analytics
// @Security    BearerAuth
// @Router      /artists/me/analytics/overview [get]
func (h *Handler) Overview(w http.ResponseWriter, r *http.Request) {
	artistID := middleware.ArtistIDFromContext(r.Context())

	var totalPlays, totalLikes, totalFollowers, totalTracks int64

	_ = h.db.QueryRow(r.Context(), `
		SELECT COALESCE(SUM(t.play_count), 0) FROM tracks t WHERE t.artist_id = $1
	`, artistID).Scan(&totalPlays)

	_ = h.db.QueryRow(r.Context(), `
		SELECT COALESCE(SUM(t.like_count), 0) FROM tracks t WHERE t.artist_id = $1
	`, artistID).Scan(&totalLikes)

	_ = h.db.QueryRow(r.Context(), `
		SELECT COUNT(*) FROM follows WHERE artist_id = $1
	`, artistID).Scan(&totalFollowers)

	_ = h.db.QueryRow(r.Context(), `
		SELECT COUNT(*) FROM tracks WHERE artist_id = $1 AND status = 'published'
	`, artistID).Scan(&totalTracks)

	response.OK(w, map[string]any{
		"total_plays":     totalPlays,
		"total_likes":     totalLikes,
		"total_followers": totalFollowers,
		"total_tracks":    totalTracks,
	})
}

// TopTracks returns per-track play stats for the artist.
//
// @Summary     Artist track analytics
// @Tags        analytics
// @Security    BearerAuth
// @Router      /artists/me/analytics/tracks [get]
func (h *Handler) TopTracks(w http.ResponseWriter, r *http.Request) {
	artistID := middleware.ArtistIDFromContext(r.Context())

	rows, err := h.db.Query(r.Context(), `
		SELECT id, title, play_count, like_count, duration_sec
		FROM tracks
		WHERE artist_id = $1 AND status = 'published'
		ORDER BY play_count DESC
		LIMIT 50
	`, artistID)
	if err != nil {
		response.InternalServerError(w, "failed to fetch track stats")
		return
	}
	defer rows.Close()

	type TrackStat struct {
		ID          string `json:"id"`
		Title       string `json:"title"`
		PlayCount   int64  `json:"play_count"`
		LikeCount   int64  `json:"like_count"`
		DurationSec int    `json:"duration_sec"`
	}

	var stats []TrackStat
	for rows.Next() {
		ts := TrackStat{}
		if err := rows.Scan(&ts.ID, &ts.Title, &ts.PlayCount, &ts.LikeCount, &ts.DurationSec); err != nil {
			slog.Warn("scan track stat row", "error", err)
			continue
		}
		stats = append(stats, ts)
	}
	if stats == nil {
		stats = []TrackStat{}
	}

	response.OK(w, map[string]any{"tracks": stats})
}

// Trends returns play and follower trends over a time period.
//
// @Summary     Artist trends
// @Tags        analytics
// @Security    BearerAuth
// @Param       period query string false "7d, 30d, 90d (default: 30d)"
// @Router      /artists/me/analytics/trends [get]
func (h *Handler) Trends(w http.ResponseWriter, r *http.Request) {
	artistID := middleware.ArtistIDFromContext(r.Context())

	period := r.URL.Query().Get("period")
	var days int
	switch period {
	case "7d":
		days = 7
	case "90d":
		days = 90
	default:
		days = 30
	}

	since := time.Now().AddDate(0, 0, -days)

	rows, err := h.db.Query(r.Context(), `
		SELECT DATE(pe.played_at) AS day, COUNT(*) AS plays
		FROM play_events pe
		JOIN tracks t ON t.id = pe.track_id
		WHERE t.artist_id = $1 AND pe.played_at >= $2
		GROUP BY day
		ORDER BY day ASC
	`, artistID, since)
	if err != nil {
		response.InternalServerError(w, "failed to fetch trends")
		return
	}
	defer rows.Close()

	type DayData struct {
		Day   string `json:"day"`
		Plays int64  `json:"plays"`
	}

	var playTrends []DayData
	for rows.Next() {
		d := DayData{}
		if err := rows.Scan(&d.Day, &d.Plays); err != nil {
			slog.Warn("scan trend day row", "error", err)
			continue
		}
		playTrends = append(playTrends, d)
	}
	if playTrends == nil {
		playTrends = []DayData{}
	}

	response.OK(w, map[string]any{
		"period":      period,
		"play_trends": playTrends,
	})
}

// GetMyMessages wraps the social handler for artist message broadcasting.
// Kept here for routing convenience.

// BroadcastMessage sends a message to all artist followers.
//
// @Summary     Broadcast message to followers
// @Tags        messaging
// @Security    BearerAuth
// @Router      /artists/me/messages [post]
func (h *Handler) BroadcastMessage(w http.ResponseWriter, r *http.Request) {
	artistID := middleware.ArtistIDFromContext(r.Context())

	var body struct {
		Subject string `json:"subject"`
		Body    string `json:"body"`
	}
	if err := parseJSON(r, &body); err != nil || body.Subject == "" || body.Body == "" {
		response.BadRequest(w, "subject and body are required")
		return
	}

	// Insert the message
	var msgID string
	err := h.db.QueryRow(r.Context(), `
		INSERT INTO messages (id, artist_id, subject, body)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, id.New(), artistID, body.Subject, body.Body).Scan(&msgID)
	if err != nil {
		response.InternalServerError(w, "failed to create message")
		return
	}

	// Fan-out to all followers (DB-backed)
	_, err = h.db.Exec(r.Context(), `
		INSERT INTO message_recipients (message_id, user_id)
		SELECT $1, follower_id FROM follows WHERE artist_id = $2
		ON CONFLICT DO NOTHING
	`, msgID, artistID)
	if err != nil {
		response.InternalServerError(w, "failed to deliver message")
		return
	}

	response.Created(w, map[string]string{"message_id": msgID, "status": "delivered"})
}

// ListSentMessages returns messages sent by the authenticated artist.
func (h *Handler) ListSentMessages(w http.ResponseWriter, r *http.Request) {
	artistID := middleware.ArtistIDFromContext(r.Context())

	rows, err := h.db.Query(r.Context(), `
		SELECT m.id, m.subject, m.body, m.created_at,
		       COUNT(mr.user_id) AS recipient_count
		FROM messages m
		LEFT JOIN message_recipients mr ON mr.message_id = m.id
		WHERE m.artist_id = $1
		GROUP BY m.id, m.subject, m.body, m.created_at
		ORDER BY m.created_at DESC
		LIMIT 50
	`, artistID)
	if err != nil {
		response.InternalServerError(w, "failed to fetch messages")
		return
	}
	defer rows.Close()

	type SentMessage struct {
		ID             string    `json:"id"`
		Subject        string    `json:"subject"`
		Body           string    `json:"body"`
		CreatedAt      time.Time `json:"created_at"`
		RecipientCount int       `json:"recipient_count"`
	}

	var messages []SentMessage
	for rows.Next() {
		m := SentMessage{}
		if err := rows.Scan(&m.ID, &m.Subject, &m.Body, &m.CreatedAt, &m.RecipientCount); err != nil {
			continue
		}
		messages = append(messages, m)
	}
	if messages == nil {
		messages = []SentMessage{}
	}

	response.OK(w, map[string]any{"messages": messages})
}

func parseJSON(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}
