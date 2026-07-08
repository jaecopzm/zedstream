package social

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jaecopzm/zedstream/pkg/middleware"
	"github.com/jaecopzm/zedstream/pkg/response"
)

// Playlist represents a user playlist.
type Playlist struct {
	ID          string    `json:"id"`
	UserID      string    `json:"user_id"`
	Title       string    `json:"title"`
	Description *string   `json:"description"`
	CoverURL    *string   `json:"cover_url"`
	IsPublic    bool      `json:"is_public"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Handler handles social feature endpoints.
type Handler struct {
	db      *pgxpool.Pool
	baseURL string
}

// NewHandler creates a new social handler.
func NewHandler(db *pgxpool.Pool, baseURL string) *Handler {
	return &Handler{db: db, baseURL: baseURL}
}

// FollowArtist follows an artist.
//
// @Summary     Follow an artist
// @Tags        social
// @Security    BearerAuth
// @Param       id path string true "Artist ID"
// @Router      /artists/{id}/follow [post]
func (h *Handler) FollowArtist(w http.ResponseWriter, r *http.Request) {
	artistID := chi.URLParam(r, "id")
	userID := middleware.UserIDFromContext(r.Context())

	_, err := h.db.Exec(r.Context(),
		`INSERT INTO follows (follower_id, artist_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		userID, artistID,
	)
	if err != nil {
		response.InternalServerError(w, "failed to follow artist")
		return
	}
	response.OK(w, map[string]string{"message": "following"})
}

// UnfollowArtist unfollows an artist.
//
// @Summary     Unfollow an artist
// @Tags        social
// @Security    BearerAuth
// @Param       id path string true "Artist ID"
// @Router      /artists/{id}/follow [delete]
func (h *Handler) UnfollowArtist(w http.ResponseWriter, r *http.Request) {
	artistID := chi.URLParam(r, "id")
	userID := middleware.UserIDFromContext(r.Context())

	_, err := h.db.Exec(r.Context(),
		`DELETE FROM follows WHERE follower_id = $1 AND artist_id = $2`, userID, artistID,
	)
	if err != nil {
		response.InternalServerError(w, "failed to unfollow artist")
		return
	}
	response.NoContent(w)
}

// LikeTrack likes a track.
//
// @Summary     Like a track
// @Tags        social
// @Security    BearerAuth
// @Param       id path string true "Track ID"
// @Router      /tracks/{id}/like [post]
func (h *Handler) LikeTrack(w http.ResponseWriter, r *http.Request) {
	trackID := chi.URLParam(r, "id")
	userID := middleware.UserIDFromContext(r.Context())

	_, err := h.db.Exec(r.Context(),
		`INSERT INTO likes (user_id, track_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		userID, trackID,
	)
	if err != nil {
		response.InternalServerError(w, "failed to like track")
		return
	}

	// Update denormalized count
	_, _ = h.db.Exec(r.Context(),
		`UPDATE tracks SET like_count = like_count + 1 WHERE id = $1`, trackID,
	)

	response.OK(w, map[string]string{"message": "liked"})
}

// UnlikeTrack removes a like from a track.
//
// @Summary     Unlike a track
// @Tags        social
// @Security    BearerAuth
// @Param       id path string true "Track ID"
// @Router      /tracks/{id}/like [delete]
func (h *Handler) UnlikeTrack(w http.ResponseWriter, r *http.Request) {
	trackID := chi.URLParam(r, "id")
	userID := middleware.UserIDFromContext(r.Context())

	cmd, err := h.db.Exec(r.Context(),
		`DELETE FROM likes WHERE user_id = $1 AND track_id = $2`, userID, trackID,
	)
	if err != nil {
		response.InternalServerError(w, "failed to unlike track")
		return
	}

	if cmd.RowsAffected() > 0 {
		_, _ = h.db.Exec(r.Context(),
			`UPDATE tracks SET like_count = GREATEST(0, like_count - 1) WHERE id = $1`, trackID,
		)
	}

	response.NoContent(w)
}

// CreatePlaylist creates a new playlist.
//
// @Summary     Create a playlist
// @Tags        playlists
// @Security    BearerAuth
// @Router      /playlists [post]
func (h *Handler) CreatePlaylist(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())

	var body struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		IsPublic    bool   `json:"is_public"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Title == "" {
		response.BadRequest(w, "title is required")
		return
	}

	p := &Playlist{}
	var desc *string
	if body.Description != "" {
		desc = &body.Description
	}

	err := h.db.QueryRow(r.Context(), `
		INSERT INTO playlists (id, user_id, title, description, is_public)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, user_id, title, description, cover_url, is_public, created_at, updated_at
	`, uuid.New().String(), userID, body.Title, desc, body.IsPublic,
	).Scan(&p.ID, &p.UserID, &p.Title, &p.Description, &p.CoverURL, &p.IsPublic, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		response.InternalServerError(w, "failed to create playlist")
		return
	}

	response.Created(w, p)
}

// UpdatePlaylist updates a playlist's metadata.
//
// @Summary     Update playlist
// @Tags        playlists
// @Security    BearerAuth
// @Param       id path string true "Playlist ID"
// @Router      /playlists/{id} [put]
func (h *Handler) UpdatePlaylist(w http.ResponseWriter, r *http.Request) {
	playlistID := chi.URLParam(r, "id")
	userID := middleware.UserIDFromContext(r.Context())

	var body struct {
		Title    string `json:"title"`
		IsPublic *bool  `json:"is_public"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		response.BadRequest(w, "invalid body")
		return
	}

	p := &Playlist{}
	err := h.db.QueryRow(r.Context(), `
		UPDATE playlists
		SET title     = COALESCE(NULLIF($3, ''), title),
		    is_public = COALESCE($4, is_public),
		    updated_at = NOW()
		WHERE id = $1 AND user_id = $2
		RETURNING id, user_id, title, description, cover_url, is_public, created_at, updated_at
	`, playlistID, userID, body.Title, body.IsPublic,
	).Scan(&p.ID, &p.UserID, &p.Title, &p.Description, &p.CoverURL, &p.IsPublic, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		response.NotFound(w, "playlist not found")
		return
	}

	response.OK(w, p)
}

// AddTrackToPlaylist adds a track to a playlist.
//
// @Summary     Add track to playlist
// @Tags        playlists
// @Security    BearerAuth
// @Param       id path string true "Playlist ID"
// @Router      /playlists/{id}/tracks [post]
func (h *Handler) AddTrackToPlaylist(w http.ResponseWriter, r *http.Request) {
	playlistID := chi.URLParam(r, "id")
	userID := middleware.UserIDFromContext(r.Context())

	// Verify ownership
	var ownerID string
	if err := h.db.QueryRow(r.Context(),
		`SELECT user_id FROM playlists WHERE id = $1`, playlistID,
	).Scan(&ownerID); err != nil || ownerID != userID {
		response.NotFound(w, "playlist not found")
		return
	}

	var body struct {
		TrackID string `json:"track_id"`
		Order   int    `json:"order"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.TrackID == "" {
		response.BadRequest(w, "track_id is required")
		return
	}

	_, err := h.db.Exec(r.Context(), `
		INSERT INTO playlist_tracks (playlist_id, track_id, track_order)
		VALUES ($1, $2, $3)
		ON CONFLICT (playlist_id, track_id) DO UPDATE SET track_order = EXCLUDED.track_order
	`, playlistID, body.TrackID, body.Order)
	if err != nil {
		response.InternalServerError(w, "failed to add track")
		return
	}

	response.OK(w, map[string]string{"message": "track added"})
}

// GetMyPlaylists returns the authenticated user's playlists.
//
// @Summary     Get my playlists
// @Tags        playlists
// @Security    BearerAuth
// @Router      /me/playlists [get]
func (h *Handler) GetMyPlaylists(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	rows, err := h.db.Query(r.Context(), `
		SELECT id, user_id, title, description, cover_url, is_public, created_at, updated_at
		FROM playlists WHERE user_id = $1 ORDER BY updated_at DESC
	`, userID)
	if err != nil {
		response.InternalServerError(w, "failed to fetch playlists")
		return
	}
	defer rows.Close()

	var playlists []Playlist
	for rows.Next() {
		p := Playlist{}
		if err := rows.Scan(&p.ID, &p.UserID, &p.Title, &p.Description, &p.CoverURL, &p.IsPublic, &p.CreatedAt, &p.UpdatedAt); err != nil {
			slog.Warn("scan playlist row", "error", err)
			continue
		}
		playlists = append(playlists, p)
	}
	if playlists == nil {
		playlists = []Playlist{}
	}
	response.OK(w, map[string]any{"playlists": playlists})
}

// GetMyLikes returns the tracks a user has liked.
//
// @Summary     Get liked tracks
// @Tags        social
// @Security    BearerAuth
// @Router      /me/likes [get]
func (h *Handler) GetMyLikes(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit <= 0 {
		limit = 20
	}

	rows, err := h.db.Query(r.Context(), `
		SELECT t.id, t.title, a.stage_name, t.cover_url, t.duration_sec, l.created_at
		FROM likes l
		JOIN tracks t ON t.id = l.track_id
		JOIN artists a ON a.id = t.artist_id
		WHERE l.user_id = $1
		ORDER BY l.created_at DESC
		LIMIT $2 OFFSET $3
	`, userID, limit, offset)
	if err != nil {
		response.InternalServerError(w, "failed to fetch likes")
		return
	}
	defer rows.Close()

	type LikedTrack struct {
		ID          string     `json:"id"`
		Title       string     `json:"title"`
		ArtistName  string     `json:"artist_name"`
		CoverURL    *string    `json:"cover_url"`
		DurationSec int        `json:"duration_sec"`
		LikedAt     time.Time  `json:"liked_at"`
	}

	var liked []LikedTrack
	for rows.Next() {
		lt := LikedTrack{}
		if err := rows.Scan(&lt.ID, &lt.Title, &lt.ArtistName, &lt.CoverURL, &lt.DurationSec, &lt.LikedAt); err != nil {
			slog.Warn("scan liked track row", "error", err)
			continue
		}
		liked = append(liked, lt)
	}
	if liked == nil {
		liked = []LikedTrack{}
	}
	response.OK(w, map[string]any{"liked_tracks": liked})
}

// GetHistory returns the user's listening history.
//
// @Summary     Get listening history
// @Tags        social
// @Security    BearerAuth
// @Router      /me/history [get]
func (h *Handler) GetHistory(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit <= 0 {
		limit = 20
	}

	rows, err := h.db.Query(r.Context(), `
		SELECT DISTINCT ON (t.id) t.id, t.title, a.stage_name, t.cover_url, t.duration_sec,
		                           MAX(pe.played_at) AS last_played
		FROM play_events pe
		JOIN tracks t ON t.id = pe.track_id
		JOIN artists a ON a.id = t.artist_id
		WHERE pe.user_id = $1
		GROUP BY t.id, t.title, a.stage_name, t.cover_url, t.duration_sec
		ORDER BY t.id, last_played DESC
		LIMIT $2 OFFSET $3
	`, userID, limit, offset)
	if err != nil {
		response.InternalServerError(w, "failed to fetch history")
		return
	}
	defer rows.Close()

	type HistoryEntry struct {
		ID          string    `json:"id"`
		Title       string    `json:"title"`
		ArtistName  string    `json:"artist_name"`
		CoverURL    *string   `json:"cover_url"`
		DurationSec int       `json:"duration_sec"`
		LastPlayed  time.Time `json:"last_played"`
	}

	var history []HistoryEntry
	for rows.Next() {
		e := HistoryEntry{}
		if err := rows.Scan(&e.ID, &e.Title, &e.ArtistName, &e.CoverURL, &e.DurationSec, &e.LastPlayed); err != nil {
			slog.Warn("scan history row", "error", err)
			continue
		}
		history = append(history, e)
	}
	if history == nil {
		history = []HistoryEntry{}
	}
	response.OK(w, map[string]any{"history": history})
}

// ShareTrack returns a shareable link for a track.
//
// @Summary     Get shareable link for a track
// @Tags        social
// @Param       id path string true "Track ID"
// @Router      /tracks/{id}/share [get]
func (h *Handler) ShareTrack(w http.ResponseWriter, r *http.Request) {
	trackID := chi.URLParam(r, "id")

	var title string
	if err := h.db.QueryRow(r.Context(),
		`SELECT title FROM tracks WHERE id = $1 AND status = 'published'`, trackID,
	).Scan(&title); err != nil {
		response.NotFound(w, "track not found")
		return
	}

	shareURL := fmt.Sprintf("%s/tracks/%s", h.baseURL, trackID)
	response.OK(w, map[string]string{
		"url":   shareURL,
		"title": title,
	})
}

// AddComment adds a comment to a track.
//
// @Summary     Add comment to track
// @Tags        social
// @Security    BearerAuth
// @Param       id path string true "Track ID"
// @Router      /tracks/{id}/comments [post]
func (h *Handler) AddComment(w http.ResponseWriter, r *http.Request) {
	trackID := chi.URLParam(r, "id")
	userID := middleware.UserIDFromContext(r.Context())

	var body struct {
		Body string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Body == "" {
		response.BadRequest(w, "body is required")
		return
	}

	type Comment struct {
		ID        string    `json:"id"`
		TrackID   string    `json:"track_id"`
		UserID    string    `json:"user_id"`
		Body      string    `json:"body"`
		CreatedAt time.Time `json:"created_at"`
	}

	c := &Comment{}
	err := h.db.QueryRow(r.Context(), `
		INSERT INTO track_comments (id, track_id, user_id, body)
		VALUES ($1, $2, $3, $4)
		RETURNING id, track_id, user_id, body, created_at
	`, uuid.New().String(), trackID, userID, body.Body,
	).Scan(&c.ID, &c.TrackID, &c.UserID, &c.Body, &c.CreatedAt)
	if err != nil {
		response.InternalServerError(w, "failed to post comment")
		return
	}

	response.Created(w, c)
}

// GetComments returns paginated comments for a track.
//
// @Summary     Get track comments
// @Tags        social
// @Param       id path string true "Track ID"
// @Router      /tracks/{id}/comments [get]
func (h *Handler) GetComments(w http.ResponseWriter, r *http.Request) {
	trackID := chi.URLParam(r, "id")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit <= 0 {
		limit = 20
	}

	rows, err := h.db.Query(r.Context(), `
		SELECT tc.id, tc.user_id, u.name, tc.body, tc.created_at
		FROM track_comments tc
		JOIN users u ON u.id = tc.user_id
		WHERE tc.track_id = $1
		ORDER BY tc.created_at DESC
		LIMIT $2 OFFSET $3
	`, trackID, limit, offset)
	if err != nil {
		response.InternalServerError(w, "failed to fetch comments")
		return
	}
	defer rows.Close()

	type CommentView struct {
		ID        string    `json:"id"`
		UserID    string    `json:"user_id"`
		UserName  string    `json:"user_name"`
		Body      string    `json:"body"`
		CreatedAt time.Time `json:"created_at"`
	}

	var comments []CommentView
	for rows.Next() {
		c := CommentView{}
		if err := rows.Scan(&c.ID, &c.UserID, &c.UserName, &c.Body, &c.CreatedAt); err != nil {
			slog.Warn("scan comment row", "error", err)
			continue
		}
		comments = append(comments, c)
	}
	if comments == nil {
		comments = []CommentView{}
	}
	response.OK(w, map[string]any{"comments": comments})
}

// DeleteComment deletes a comment (owner or admin only).
//
// @Summary     Delete a comment
// @Tags        social
// @Security    BearerAuth
// @Param       id path string true "Track ID"
// @Param       commentId path string true "Comment ID"
// @Router      /tracks/{id}/comments/{commentId} [delete]
func (h *Handler) DeleteComment(w http.ResponseWriter, r *http.Request) {
	commentID := chi.URLParam(r, "commentId")
	userID := middleware.UserIDFromContext(r.Context())
	role := middleware.RoleFromContext(r.Context())

	var ownerID string
	if err := h.db.QueryRow(r.Context(),
		`SELECT user_id FROM track_comments WHERE id = $1`, commentID,
	).Scan(&ownerID); err != nil {
		response.NotFound(w, "comment not found")
		return
	}

	if ownerID != userID && role != "admin" {
		response.Forbidden(w, "not authorized to delete this comment")
		return
	}

	_, _ = h.db.Exec(r.Context(), `DELETE FROM track_comments WHERE id = $1`, commentID)
	response.NoContent(w)
}

// GetMyMessages returns the authenticated listener's message inbox.
//
// @Summary     Get my messages
// @Tags        messaging
// @Security    BearerAuth
// @Router      /me/messages [get]
func (h *Handler) GetMyMessages(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit <= 0 {
		limit = 20
	}

	rows, err := h.db.Query(r.Context(), `
		SELECT m.id, m.subject, m.body, a.stage_name, mr.read, m.created_at
		FROM message_recipients mr
		JOIN messages m ON m.id = mr.message_id
		JOIN artists a ON a.id = m.artist_id
		WHERE mr.user_id = $1
		ORDER BY m.created_at DESC
		LIMIT $2 OFFSET $3
	`, userID, limit, offset)
	if err != nil {
		response.InternalServerError(w, "failed to fetch messages")
		return
	}
	defer rows.Close()

	type Message struct {
		ID         string    `json:"id"`
		Subject    string    `json:"subject"`
		Body       string    `json:"body"`
		ArtistName string    `json:"artist_name"`
		Read       bool      `json:"read"`
		CreatedAt  time.Time `json:"created_at"`
	}

	var messages []Message
	for rows.Next() {
		msg := Message{}
		if err := rows.Scan(&msg.ID, &msg.Subject, &msg.Body, &msg.ArtistName, &msg.Read, &msg.CreatedAt); err != nil {
			slog.Warn("scan message row", "error", err)
			continue
		}
		messages = append(messages, msg)
	}
	if messages == nil {
		messages = []Message{}
	}
	response.OK(w, map[string]any{"messages": messages})
}


