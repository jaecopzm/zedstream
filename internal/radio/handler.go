package radio

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jaecopzm/zedstream/pkg/id"
	"github.com/jaecopzm/zedstream/pkg/middleware"
	"github.com/jaecopzm/zedstream/pkg/response"
	"github.com/jaecopzm/zedstream/pkg/storage"
)

type Station struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Slug        string     `json:"slug"`
	Description *string    `json:"description"`
	CoverURL    *string    `json:"cover_url"`
	Type        string     `json:"type"`
	GenreID     *string    `json:"genre_id"`
	GenreName   *string    `json:"genre_name,omitempty"`
	CreatedBy   string     `json:"created_by"`
	IsActive    bool       `json:"is_active"`
	TrackCount  int        `json:"track_count,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

type StationTrack struct {
	ID          string              `json:"id"`
	ArtistID    string              `json:"artist_id"`
	Title       string              `json:"title"`
	ArtistName  string              `json:"artist_name"`
	CoverURL    *string             `json:"cover_url"`
	DurationSec int                 `json:"duration_sec"`
	PlayCount   int64               `json:"play_count"`
	AlbumID     *string             `json:"album_id,omitempty"`
	AlbumName   *string             `json:"album_name,omitempty"`
	Collaborators []map[string]string `json:"collaborators,omitempty"`
}

type Handler struct {
	db         *pgxpool.Pool
	storage    *storage.Client
	imageBucket string
}

func NewHandler(db *pgxpool.Pool, storageClient *storage.Client, imageBucket string) *Handler {
	return &Handler{db: db, storage: storageClient, imageBucket: imageBucket}
}

// slugify converts a name into a URL-friendly slug.
func slugify(name string) string {
	s := strings.ToLower(name)
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.ReplaceAll(s, "&", "and")
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func extensionFromMime(mime string) string {
	switch mime {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	default:
		return ".jpg"
	}
}

// ListStations returns active radio stations. Genre stations are dynamically
// generated from the genres table. Curated stations and radio-flagged playlists
// are read from radio_stations and playlists respectively.
//
// @Summary List radio stations
// @Tags radio
// @Router /radio/stations [get]
func (h *Handler) ListStations(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())

	// Genre stations — one per genre that has published tracks
	genreRows, err := h.db.Query(r.Context(), `
		SELECT g.slug, g.name, g.slug, COUNT(t.id) AS track_count,
		       (SELECT t2.cover_url FROM tracks t2 WHERE t2.genre_id = g.id AND t2.status = 'published' AND t2.cover_url IS NOT NULL ORDER BY t2.play_count DESC LIMIT 1) AS cover_url
		FROM genres g
		JOIN tracks t ON t.genre_id = g.id AND t.status = 'published'
		GROUP BY g.id, g.slug, g.name
		ORDER BY g.name ASC
	`)
	if err != nil {
		response.InternalServerError(w, "failed to fetch genre stations")
		return
	}
	defer genreRows.Close()

	type GenreStation struct {
		ID         string  `json:"id"`
		Name       string  `json:"name"`
		Slug       string  `json:"slug"`
		CoverURL   *string `json:"cover_url"`
		TrackCount int     `json:"track_count"`
	}

	var genreStations []GenreStation
	for genreRows.Next() {
		gs := GenreStation{}
		if err := genreRows.Scan(&gs.ID, &gs.Name, &gs.Slug, &gs.TrackCount, &gs.CoverURL); err != nil {
			slog.Warn("scan genre station", "error", err)
			continue
		}
		genreStations = append(genreStations, gs)
	}

	// Curated stations from radio_stations table
	curatedRows, err := h.db.Query(r.Context(), `
		SELECT rs.id, rs.name, rs.slug, rs.description,
		       COALESCE(rs.cover_url, (
		           SELECT t.cover_url FROM radio_station_tracks rst
		           JOIN tracks t ON t.id = rst.track_id
		           WHERE rst.station_id = rs.id AND t.cover_url IS NOT NULL
		           ORDER BY rst.track_order ASC, rst.added_at ASC LIMIT 1
		       )) AS cover_url,
		       rs.type, rs.genre_id, rs.created_by, rs.is_active, rs.created_at, rs.updated_at,
		       (SELECT COUNT(*) FROM radio_station_tracks WHERE station_id = rs.id) AS track_count
		FROM radio_stations rs
		WHERE rs.is_active = true
		ORDER BY rs.name ASC
	`)
	if err != nil {
		response.InternalServerError(w, "failed to fetch curated stations")
		return
	}
	defer curatedRows.Close()

	var curatedStations []Station
	for curatedRows.Next() {
		s := Station{}
		if err := curatedRows.Scan(&s.ID, &s.Name, &s.Slug, &s.Description, &s.CoverURL,
			&s.Type, &s.GenreID, &s.CreatedBy, &s.IsActive, &s.CreatedAt, &s.UpdatedAt, &s.TrackCount); err != nil {
			slog.Warn("scan curated station", "error", err)
			continue
		}
		curatedStations = append(curatedStations, s)
	}

	// Admin radio playlists
	playlistRows, err := h.db.Query(r.Context(), `
		SELECT p.id, p.title, p.description,
		       COALESCE(p.cover_url, (
		           SELECT t.cover_url FROM playlist_tracks pt
		           JOIN tracks t ON t.id = pt.track_id
		           WHERE pt.playlist_id = p.id AND t.cover_url IS NOT NULL
		           ORDER BY pt.track_order ASC, pt.added_at ASC LIMIT 1
		       )) AS cover_url,
		       p.created_at, p.updated_at,
		       (SELECT COUNT(*) FROM playlist_tracks WHERE playlist_id = p.id) AS track_count
		FROM playlists p
		WHERE p.is_radio = true AND p.is_public = true
		ORDER BY p.updated_at DESC
	`)
	if err != nil {
		response.InternalServerError(w, "failed to fetch radio playlists")
		return
	}
	defer playlistRows.Close()

	type RadioPlaylist struct {
		ID          string    `json:"id"`
		Name        string    `json:"name"`
		Description *string   `json:"description"`
		CoverURL    *string   `json:"cover_url"`
		TrackCount  int       `json:"track_count"`
		CreatedAt   time.Time `json:"created_at"`
		UpdatedAt   time.Time `json:"updated_at"`
	}

	var radioPlaylists []RadioPlaylist
	for playlistRows.Next() {
		rp := RadioPlaylist{}
		if err := playlistRows.Scan(&rp.ID, &rp.Name, &rp.Description, &rp.CoverURL, &rp.CreatedAt, &rp.UpdatedAt, &rp.TrackCount); err != nil {
			slog.Warn("scan radio playlist", "error", err)
			continue
		}
		radioPlaylists = append(radioPlaylists, rp)
	}

	// Build response
	type StationResponse struct {
		Genre    []GenreStation    `json:"genre"`
		Curated  []Station         `json:"curated"`
		Playlists []RadioPlaylist  `json:"playlists"`
	}

	resp := StationResponse{
		Genre:     genreStations,
		Curated:   curatedStations,
		Playlists: radioPlaylists,
	}

	if resp.Genre == nil {
		resp.Genre = []GenreStation{}
	}
	if resp.Curated == nil {
		resp.Curated = []Station{}
	}
	if resp.Playlists == nil {
		resp.Playlists = []RadioPlaylist{}
	}

	// Add personalized section if user is authenticated
	response.OK(w, map[string]any{
		"stations": resp,
		"user_id":  userID,
	})
}

// GetStation returns a single station's details and tracks.
//
// @Summary Get radio station
// @Tags radio
// @Param id path string true "Station ID"
// @Router /radio/stations/{id} [get]
func (h *Handler) GetStation(w http.ResponseWriter, r *http.Request) {
	stationID := chi.URLParam(r, "id")
	limit := 50

	type stationInfo struct {
		Station
		Tracks []StationTrack `json:"tracks"`
	}

	info := stationInfo{}
	info.Tracks = []StationTrack{}
	var genreID string

	// Try radio_stations table first
	s := &Station{}
	err := h.db.QueryRow(r.Context(), `
		SELECT id, name, slug, description, cover_url, type, genre_id, created_by, is_active, created_at, updated_at
		FROM radio_stations WHERE id = $1 AND is_active = true
	`, stationID).Scan(&s.ID, &s.Name, &s.Slug, &s.Description, &s.CoverURL,
		&s.Type, &s.GenreID, &s.CreatedBy, &s.IsActive, &s.CreatedAt, &s.UpdatedAt)
	if err == nil {
		info.Station = *s
	} else {
		// Check if it's a genre (genres use their slug as the station ID)
		var genreName, genreSlug string
		err = h.db.QueryRow(r.Context(), `
			SELECT id, name, slug FROM genres WHERE slug = $1 OR id = $1
		`, stationID).Scan(&genreID, &genreName, &genreSlug)
		if err == nil {
			info.Station = Station{
				ID:   genreID,
				Name: genreName,
				Slug: genreSlug,
				Type: "genre",
			}
		} else {
			// Check if it's a radio playlist
			var pTitle string
			var pDesc, pCover *string
			err = h.db.QueryRow(r.Context(), `
				SELECT title, description, cover_url FROM playlists WHERE id = $1 AND is_radio = true AND is_public = true
			`, stationID).Scan(&pTitle, &pDesc, &pCover)
			if err != nil {
				response.NotFound(w, "station not found")
				return
			}
			info.Station = Station{
				ID:          stationID,
				Name:        pTitle,
				Description: pDesc,
				CoverURL:    pCover,
				Type:        "playlist",
			}
		}
	}

	// Load tracks
	switch info.Type {
	case "genre":
		rows, err := h.db.Query(r.Context(), `
			SELECT t.id, t.artist_id, t.title, a.stage_name, t.cover_url, t.duration_sec, t.play_count,
			       t.album_id, al.title
			FROM tracks t
			JOIN artists a ON a.id = t.artist_id
			LEFT JOIN albums al ON al.id = t.album_id
			WHERE t.genre_id = $1 AND t.status = 'published'
			ORDER BY t.play_count DESC
			LIMIT $2
		`, genreID, limit)
		if err != nil {
			response.InternalServerError(w, "failed to fetch tracks")
			return
		}
		defer rows.Close()
		for rows.Next() {
			t := StationTrack{}
			if err := rows.Scan(&t.ID, &t.ArtistID, &t.Title, &t.ArtistName, &t.CoverURL, &t.DurationSec, &t.PlayCount, &t.AlbumID, &t.AlbumName); err != nil {
				slog.Warn("scan genre track", "error", err)
				continue
			}
			info.Tracks = append(info.Tracks, t)
		}

	case "curated":
		rows, err := h.db.Query(r.Context(), `
			SELECT t.id, t.artist_id, t.title, a.stage_name, t.cover_url, t.duration_sec, t.play_count,
			       t.album_id, al.title
			FROM radio_station_tracks rst
			JOIN tracks t ON t.id = rst.track_id
			JOIN artists a ON a.id = t.artist_id
			LEFT JOIN albums al ON al.id = t.album_id
			WHERE rst.station_id = $1 AND t.status = 'published'
			ORDER BY rst.track_order ASC, rst.added_at ASC
		`, stationID)
		if err != nil {
			response.InternalServerError(w, "failed to fetch station tracks")
			return
		}
		defer rows.Close()
		for rows.Next() {
			t := StationTrack{}
			if err := rows.Scan(&t.ID, &t.ArtistID, &t.Title, &t.ArtistName, &t.CoverURL, &t.DurationSec, &t.PlayCount, &t.AlbumID, &t.AlbumName); err != nil {
				slog.Warn("scan curated track", "error", err)
				continue
			}
			info.Tracks = append(info.Tracks, t)
		}

	case "playlist":
		rows, err := h.db.Query(r.Context(), `
			SELECT t.id, t.artist_id, t.title, a.stage_name, t.cover_url, t.duration_sec, t.play_count,
			       t.album_id, al.title
			FROM playlist_tracks pt
			JOIN tracks t ON t.id = pt.track_id
			JOIN artists a ON a.id = t.artist_id
			LEFT JOIN albums al ON al.id = t.album_id
			WHERE pt.playlist_id = $1 AND t.status = 'published'
			ORDER BY pt.track_order ASC, pt.added_at ASC
		`, stationID)
		if err != nil {
			response.InternalServerError(w, "failed to fetch playlist tracks")
			return
		}
		defer rows.Close()
		for rows.Next() {
			t := StationTrack{}
			if err := rows.Scan(&t.ID, &t.ArtistID, &t.Title, &t.ArtistName, &t.CoverURL, &t.DurationSec, &t.PlayCount, &t.AlbumID, &t.AlbumName); err != nil {
				slog.Warn("scan playlist track", "error", err)
				continue
			}
			info.Tracks = append(info.Tracks, t)
		}
	}

	// Load collaborators
	trackIDs := make([]string, len(info.Tracks))
	for i, t := range info.Tracks {
		trackIDs[i] = t.ID
	}
	colabs := h.loadTrackCollaborators(r.Context(), trackIDs)
	for i := range info.Tracks {
		if c, ok := colabs[info.Tracks[i].ID]; ok {
			info.Tracks[i].Collaborators = c
		}
	}

	if info.Tracks == nil {
		info.Tracks = []StationTrack{}
	}

	response.OK(w, map[string]any{
		"station": info.Station,
		"tracks":  info.Tracks,
	})
}

// GetPersonalized returns a personalized radio station based on the user's likes.
//
// @Summary Get personalized radio
// @Tags radio
// @Security BearerAuth
// @Router /radio/personalized [get]
func (h *Handler) GetPersonalized(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	if userID == "" {
		response.Unauthorized(w, "authentication required")
		return
	}

	limit := 50

	// Get tracks from liked genres + similar to liked tracks
	rows, err := h.db.Query(r.Context(), `
		SELECT t.id, t.artist_id, t.title, a.stage_name, t.cover_url, t.duration_sec, t.play_count,
		       t.album_id, al.title
		FROM tracks t
		JOIN artists a ON a.id = t.artist_id
		LEFT JOIN albums al ON al.id = t.album_id
		WHERE t.status = 'published'
		  AND (
		    t.genre_id IN (
		      SELECT DISTINCT t2.genre_id FROM likes l
		      JOIN tracks t2 ON t2.id = l.track_id
		      WHERE l.user_id = $1 AND t2.genre_id IS NOT NULL
		    )
		    OR t.id IN (
		      SELECT t3.id FROM likes l2
		      JOIN tracks t3 ON t3.id = l2.track_id
		      WHERE l2.user_id = $1
		      ORDER BY t3.play_count DESC
		      LIMIT 20
		    )
		  )
		  AND t.id NOT IN (
		    SELECT track_id FROM likes WHERE user_id = $1
		  )
		ORDER BY t.play_count DESC
		LIMIT $2
	`, userID, limit)
	if err != nil {
		response.InternalServerError(w, "failed to fetch personalized radio")
		return
	}
	defer rows.Close()

	var tracks []StationTrack
	for rows.Next() {
		t := StationTrack{}
		if err := rows.Scan(&t.ID, &t.ArtistID, &t.Title, &t.ArtistName, &t.CoverURL, &t.DurationSec, &t.PlayCount, &t.AlbumID, &t.AlbumName); err != nil {
			slog.Warn("scan personalized track", "error", err)
			continue
		}
		tracks = append(tracks, t)
	}
	if tracks == nil {
		tracks = []StationTrack{}
	}

	trackIDs := make([]string, len(tracks))
	for i, t := range tracks {
		trackIDs[i] = t.ID
	}
	colabs := h.loadTrackCollaborators(r.Context(), trackIDs)
	for i := range tracks {
		if c, ok := colabs[tracks[i].ID]; ok {
			tracks[i].Collaborators = c
		}
	}

	response.OK(w, map[string]any{
		"station": Station{
			ID:   "personalized",
			Name: "Made for You",
			Type: "personalized",
		},
		"tracks": tracks,
	})
}

// ── Admin Handlers ──────────────────────────────────────────────────────────

// CreateStation creates a new curated radio station.
//
// @Summary Create radio station
// @Tags admin
// @Security BearerAuth
// @Router /admin/radio [post]
func (h *Handler) CreateStation(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())

	if err := r.ParseMultipartForm(10 << 20); err != nil {
		_ = r.ParseForm()
	}

	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		response.BadRequest(w, "name is required")
		return
	}

	description := r.FormValue("description")
	var desc *string
	if description != "" {
		desc = &description
	}

	genreID := r.FormValue("genre_id")
	var genreIDPtr *string
	if genreID != "" {
		genreIDPtr = &genreID
	}

	var coverURL *string
	file, header, fileErr := r.FormFile("cover")
	if fileErr == nil {
		defer file.Close()
		contentType := header.Header.Get("Content-Type")
		if strings.HasPrefix(contentType, "image/") {
			key := fmt.Sprintf("radio/covers/%s%s", id.New(), extensionFromMime(contentType))
			if err := h.storage.UploadFile(r.Context(), h.imageBucket, key, contentType, file); err == nil {
				url := h.storage.PublicURL(key)
				coverURL = &url
			}
		}
	}

	slug := slugify(name)

	s := &Station{}
	err := h.db.QueryRow(r.Context(), `
		INSERT INTO radio_stations (id, name, slug, description, cover_url, type, genre_id, created_by)
		VALUES ($1, $2, $3, $4, $5, 'curated', $6, $7)
		RETURNING id, name, slug, description, cover_url, type, genre_id, created_by, is_active, created_at, updated_at
	`, id.New(), name, slug, desc, coverURL, genreIDPtr, userID,
	).Scan(&s.ID, &s.Name, &s.Slug, &s.Description, &s.CoverURL, &s.Type, &s.GenreID, &s.CreatedBy, &s.IsActive, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		response.InternalServerError(w, "failed to create station")
		return
	}

	response.Created(w, s)
}

// UpdateStation updates a curated station's metadata.
//
// @Summary Update radio station
// @Tags admin
// @Security BearerAuth
// @Router /admin/radio/{id} [patch]
func (h *Handler) UpdateStation(w http.ResponseWriter, r *http.Request) {
	stationID := chi.URLParam(r, "id")

	if err := r.ParseMultipartForm(10 << 20); err != nil {
		_ = r.ParseForm()
	}

	name := r.FormValue("name")
	var namePtr *string
	if name != "" {
		namePtr = &name
	}

	description := r.FormValue("description")
	var descPtr *string
	if description != "" {
		descPtr = &description
	}

	genreID := r.FormValue("genre_id")
	var genreIDPtr *string
	if genreID != "" {
		genreIDPtr = &genreID
	}

	isActive := r.FormValue("is_active") == "true"
	var isActivePtr *bool
	if r.FormValue("is_active") != "" {
		isActivePtr = &isActive
	}

	var slug *string
	if namePtr != nil {
		s := slugify(*namePtr)
		slug = &s
	}

	var coverURL *string
	file, header, fileErr := r.FormFile("cover")
	if fileErr == nil {
		defer file.Close()
		contentType := header.Header.Get("Content-Type")
		if strings.HasPrefix(contentType, "image/") {
			key := fmt.Sprintf("radio/covers/%s%s", id.New(), extensionFromMime(contentType))
			if err := h.storage.UploadFile(r.Context(), h.imageBucket, key, contentType, file); err == nil {
				url := h.storage.PublicURL(key)
				coverURL = &url
			}
		}
	}

	s := &Station{}
	err := h.db.QueryRow(r.Context(), `
		UPDATE radio_stations
		SET name        = COALESCE($2, name),
		    slug        = COALESCE($3, slug),
		    description = $4,
		    cover_url   = $5,
		    genre_id    = $6,
		    is_active   = COALESCE($7, is_active),
		    updated_at  = NOW()
		WHERE id = $1
		RETURNING id, name, slug, description, cover_url, type, genre_id, created_by, is_active, created_at, updated_at
	`, stationID, namePtr, slug,
		descPtr,
		coverURL,
		genreIDPtr,
		isActivePtr,
	).Scan(&s.ID, &s.Name, &s.Slug, &s.Description, &s.CoverURL, &s.Type, &s.GenreID, &s.CreatedBy, &s.IsActive, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		response.NotFound(w, "station not found")
		return
	}

	response.OK(w, s)
}

// DeleteStation soft-deletes a station by setting is_active to false.
//
// @Summary Delete radio station
// @Tags admin
// @Security BearerAuth
// @Router /admin/radio/{id} [delete]
func (h *Handler) DeleteStation(w http.ResponseWriter, r *http.Request) {
	stationID := chi.URLParam(r, "id")

	cmd, err := h.db.Exec(r.Context(),
		`UPDATE radio_stations SET is_active = false, updated_at = NOW() WHERE id = $1`, stationID,
	)
	if err != nil {
		response.InternalServerError(w, "failed to delete station")
		return
	}
	if cmd.RowsAffected() == 0 {
		response.NotFound(w, "station not found")
		return
	}

	response.NoContent(w)
}

// SetStationTracks replaces all tracks in a curated station (max 50).
//
// @Summary Set station tracks
// @Tags admin
// @Security BearerAuth
// @Router /admin/radio/{id}/tracks [put]
func (h *Handler) SetStationTracks(w http.ResponseWriter, r *http.Request) {
	stationID := chi.URLParam(r, "id")

	var body struct {
		TrackIDs []string `json:"track_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		response.BadRequest(w, "track_ids is required")
		return
	}

	if len(body.TrackIDs) > 50 {
		response.BadRequest(w, "maximum 50 tracks per station")
		return
	}

	tx, err := h.db.Begin(r.Context())
	if err != nil {
		response.InternalServerError(w, "failed to begin transaction")
		return
	}
	defer tx.Rollback(r.Context())

	_, err = tx.Exec(r.Context(), `DELETE FROM radio_station_tracks WHERE station_id = $1`, stationID)
	if err != nil {
		response.InternalServerError(w, "failed to clear tracks")
		return
	}

	for i, trackID := range body.TrackIDs {
		_, err = tx.Exec(r.Context(), `
			INSERT INTO radio_station_tracks (station_id, track_id, track_order)
			VALUES ($1, $2, $3)
			ON CONFLICT (station_id, track_id) DO UPDATE SET track_order = EXCLUDED.track_order
		`, stationID, trackID, i)
		if err != nil {
			response.InternalServerError(w, "failed to add track")
			return
		}
	}

	if err := tx.Commit(r.Context()); err != nil {
		response.InternalServerError(w, "failed to commit")
		return
	}

	response.OK(w, map[string]any{
		"track_count": len(body.TrackIDs),
	})
}

// AddStationTrack adds a single track to a curated station.
//
// @Summary Add track to station
// @Tags admin
// @Security BearerAuth
// @Router /admin/radio/{id}/tracks [post]
func (h *Handler) AddStationTrack(w http.ResponseWriter, r *http.Request) {
	stationID := chi.URLParam(r, "id")

	var body struct {
		TrackID string `json:"track_id"`
		Order   int    `json:"order"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.TrackID == "" {
		response.BadRequest(w, "track_id is required")
		return
	}

	// Check current track count
	var count int
	h.db.QueryRow(r.Context(),
		`SELECT COUNT(*) FROM radio_station_tracks WHERE station_id = $1`, stationID,
	).Scan(&count)
	if count >= 50 {
		response.BadRequest(w, "station track limit reached (max 50)")
		return
	}

	_, err := h.db.Exec(r.Context(), `
		INSERT INTO radio_station_tracks (station_id, track_id, track_order)
		VALUES ($1, $2, $3)
		ON CONFLICT (station_id, track_id) DO UPDATE SET track_order = EXCLUDED.track_order
	`, stationID, body.TrackID, body.Order)
	if err != nil {
		response.InternalServerError(w, "failed to add track")
		return
	}

	response.OK(w, map[string]string{"message": "track added"})
}

// RemoveStationTrack removes a track from a curated station.
//
// @Summary Remove track from station
// @Tags admin
// @Security BearerAuth
// @Router /admin/radio/{id}/tracks/{trackId} [delete]
func (h *Handler) RemoveStationTrack(w http.ResponseWriter, r *http.Request) {
	stationID := chi.URLParam(r, "id")
	trackID := chi.URLParam(r, "trackId")

	_, err := h.db.Exec(r.Context(),
		`DELETE FROM radio_station_tracks WHERE station_id = $1 AND track_id = $2`,
		stationID, trackID,
	)
	if err != nil {
		response.InternalServerError(w, "failed to remove track")
		return
	}

	response.NoContent(w)
}

// ListAllStations returns all stations (including inactive) for admin management.
//
// @Summary List all stations (admin)
// @Tags admin
// @Security BearerAuth
// @Router /admin/radio [get]
func (h *Handler) ListAllStations(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query(r.Context(), `
		SELECT rs.id, rs.name, rs.slug, rs.description, rs.cover_url, rs.type,
		       rs.genre_id, g.name AS genre_name, rs.created_by, rs.is_active, rs.created_at, rs.updated_at,
		       (SELECT COUNT(*) FROM radio_station_tracks WHERE station_id = rs.id) AS track_count
		FROM radio_stations rs
		LEFT JOIN genres g ON g.id = rs.genre_id
		ORDER BY rs.name ASC
	`)
	if err != nil {
		response.InternalServerError(w, "failed to fetch stations")
		return
	}
	defer rows.Close()

	var stations []Station
	for rows.Next() {
		s := Station{}
		if err := rows.Scan(&s.ID, &s.Name, &s.Slug, &s.Description, &s.CoverURL,
			&s.Type, &s.GenreID, &s.GenreName, &s.CreatedBy, &s.IsActive, &s.CreatedAt, &s.UpdatedAt, &s.TrackCount); err != nil {
			slog.Warn("scan station", "error", err)
			continue
		}
		stations = append(stations, s)
	}
	if stations == nil {
		stations = []Station{}
	}

	response.OK(w, map[string]any{"stations": stations})
}

// ── Helpers ─────────────────────────────────────────────────────────────────

func (h *Handler) loadTrackCollaborators(ctx context.Context, trackIDs []string) map[string][]map[string]string {
	result := map[string][]map[string]string{}
	if len(trackIDs) == 0 {
		return result
	}
	rows, err := h.db.Query(ctx, `
		SELECT tc.track_id, tc.artist_id, a.stage_name, COALESCE(a.photo_url, '')
		FROM track_collaborators tc
		JOIN artists a ON a.id = tc.artist_id
		WHERE tc.track_id = ANY($1)
	`, trackIDs)
	if err != nil {
		slog.Warn("load track collaborators", "error", err)
		return result
	}
	defer rows.Close()
	for rows.Next() {
		var trackID, artistID, stageName, photoURL string
		if err := rows.Scan(&trackID, &artistID, &stageName, &photoURL); err != nil {
			slog.Warn("scan collaborator", "error", err)
			continue
		}
		m := map[string]string{
			"artist_id":  artistID,
			"stage_name": stageName,
		}
		if photoURL != "" {
			m["photo_url"] = photoURL
		}
		result[trackID] = append(result[trackID], m)
	}
	return result
}
