package recommendations

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jaecopzm/zedstream/pkg/middleware"
	"github.com/jaecopzm/zedstream/pkg/response"
)

// Handler serves recommendation and radio endpoints.
type Handler struct {
	db *pgxpool.Pool
}

// NewHandler creates a new recommendations handler.
func NewHandler(db *pgxpool.Pool) *Handler {
	return &Handler{db: db}
}

// GetRecommendations returns personalized track recommendations for the user.
// Strategy: tracks liked/played by users who also liked the same tracks as me, fallback to genre.
//
// @Summary     Get personalized recommendations
// @Tags        recommendations
// @Security    BearerAuth
// @Router      /me/recommendations [get]
func (h *Handler) GetRecommendations(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 50 {
		limit = 20
	}

	// Collaborative filtering: find tracks liked by similar users (shared likes)
	rows, err := h.db.Query(r.Context(), `
		WITH my_likes AS (
		    SELECT track_id FROM likes WHERE user_id = $1
		),
		similar_users AS (
		    SELECT DISTINCT l.user_id
		    FROM likes l
		    WHERE l.track_id IN (SELECT track_id FROM my_likes)
		      AND l.user_id != $1
		    LIMIT 100
		),
		candidate_tracks AS (
		    SELECT l.track_id, COUNT(*) AS score
		    FROM likes l
		    WHERE l.user_id IN (SELECT user_id FROM similar_users)
		      AND l.track_id NOT IN (SELECT track_id FROM my_likes)
		    GROUP BY l.track_id
		    ORDER BY score DESC
		    LIMIT $2
		)
		SELECT t.id, t.title, a.stage_name, t.cover_url, t.duration_sec, t.play_count
		FROM candidate_tracks ct
		JOIN tracks t ON t.id = ct.track_id
		JOIN artists a ON a.id = t.artist_id
		WHERE t.status = 'published'
		ORDER BY ct.score DESC
	`, userID, limit)

	type RecommendedTrack struct {
		ID          string  `json:"id"`
		Title       string  `json:"title"`
		ArtistName  string  `json:"artist_name"`
		CoverURL    *string `json:"cover_url"`
		DurationSec int     `json:"duration_sec"`
		PlayCount   int64   `json:"play_count"`
	}

	var tracks []RecommendedTrack

	if err == nil {
		defer rows.Close()
		for rows.Next() {
			t := RecommendedTrack{}
			if err := rows.Scan(&t.ID, &t.Title, &t.ArtistName, &t.CoverURL, &t.DurationSec, &t.PlayCount); err == nil {
				tracks = append(tracks, t)
			}
		}
	}

	// Cold-start fallback: return top tracks by play count from genres the user likes
	if len(tracks) < limit {
		fallbackRows, err := h.db.Query(r.Context(), `
			WITH user_genres AS (
			    SELECT DISTINCT t.genre_id
			    FROM play_events pe
			    JOIN tracks t ON t.id = pe.track_id
			    WHERE pe.user_id = $1 AND t.genre_id IS NOT NULL
			    LIMIT 5
			)
			SELECT t.id, t.title, a.stage_name, t.cover_url, t.duration_sec, t.play_count
			FROM tracks t
			JOIN artists a ON a.id = t.artist_id
			WHERE t.status = 'published'
			  AND (t.genre_id IN (SELECT genre_id FROM user_genres) OR (SELECT COUNT(*) FROM user_genres) = 0)
			ORDER BY t.play_count DESC
			LIMIT $2
		`, userID, limit-len(tracks))
		if err == nil {
			defer fallbackRows.Close()
			for fallbackRows.Next() {
				t := RecommendedTrack{}
				if err := fallbackRows.Scan(&t.ID, &t.Title, &t.ArtistName, &t.CoverURL, &t.DurationSec, &t.PlayCount); err == nil {
					tracks = append(tracks, t)
				}
			}
		}
	}

	if tracks == nil {
		tracks = []RecommendedTrack{}
	}

	response.OK(w, map[string]any{"recommendations": tracks})
}

// GetRadio returns a queue of similar tracks for autoplay.
//
// @Summary     Get radio queue for a track
// @Tags        recommendations
// @Security    BearerAuth
// @Param       id path string true "Track ID"
// @Router      /tracks/{id}/radio [get]
func (h *Handler) GetRadio(w http.ResponseWriter, r *http.Request) {
	trackID := chi.URLParam(r, "id")
	userID := middleware.UserIDFromContext(r.Context())
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 30 {
		limit = 10
	}

	// Find same-genre tracks not recently played by this user
	rows, err := h.db.Query(r.Context(), `
		WITH source AS (
		    SELECT genre_id, artist_id, embedding FROM tracks WHERE id = $1
		),
		recent_plays AS (
		    SELECT track_id FROM play_events
		    WHERE user_id = $2
		    ORDER BY played_at DESC
		    LIMIT 50
		)
		SELECT t.id, t.title, a.stage_name, t.cover_url, t.duration_sec, t.artist_id, t.play_count, COALESCE(al.title, '')
		FROM tracks t
		JOIN artists a ON a.id = t.artist_id
		LEFT JOIN albums al ON al.id = t.album_id
		CROSS JOIN source s
		WHERE t.status = 'published'
		  AND t.id != $1
		  AND t.id NOT IN (SELECT track_id FROM recent_plays)
		ORDER BY 
		  CASE WHEN s.embedding IS NOT NULL AND t.embedding IS NOT NULL THEN (t.embedding <=> s.embedding) END ASC NULLS LAST,
		  (t.genre_id = s.genre_id OR t.artist_id = s.artist_id) DESC,
		  t.play_count DESC, 
		  RANDOM()
		LIMIT $3
	`, trackID, userID, limit)
	if err != nil {
		response.InternalServerError(w, "failed to build radio queue")
		return
	}
	defer rows.Close()

	type RadioTrack struct {
		ID          string  `json:"id"`
		Title       string  `json:"title"`
		ArtistName  string  `json:"artist_name"`
		CoverURL    *string `json:"cover_url"`
		DurationSec int     `json:"duration_sec"`
		ArtistID    string  `json:"artist_id"`
		PlayCount   int     `json:"play_count"`
		AlbumName   string  `json:"album_name"`
	}

	var queue []RadioTrack
	for rows.Next() {
		t := RadioTrack{}
		if err := rows.Scan(&t.ID, &t.Title, &t.ArtistName, &t.CoverURL, &t.DurationSec, &t.ArtistID, &t.PlayCount, &t.AlbumName); err == nil {
			queue = append(queue, t)
		}
	}
	if queue == nil {
		queue = []RadioTrack{}
	}

	response.OK(w, map[string]any{"queue": queue, "seed_track_id": trackID})
}
