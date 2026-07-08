package music

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jaecopzm/zedstream/pkg/middleware"
	"github.com/jaecopzm/zedstream/pkg/response"
	"github.com/jaecopzm/zedstream/pkg/storage"
)

const (
	maxAudioSize = 50 << 20 // 50 MB
	maxImageSize = 5 << 20  // 5 MB
)

var allowedAudioTypes = map[string]bool{
	"audio/mpeg": true,
	"audio/flac": true,
	"audio/wav":  true,
	"audio/ogg":  true,
	"audio/mp4":  true,
}

// Handler exposes music HTTP endpoints.
type Handler struct {
	repo        *Repository
	storage     *storage.Client
	audioBucket string
	imageBucket string
}

// NewHandler creates a new music handler.
func NewHandler(repo *Repository, store *storage.Client, audioBucket, imageBucket string) *Handler {
	return &Handler{
		repo:        repo,
		storage:     store,
		audioBucket: audioBucket,
		imageBucket: imageBucket,
	}
}

// UploadTrack handles track audio upload with metadata.
//
// @Summary     Upload a new track
// @Tags        tracks
// @Security    BearerAuth
// @Router      /artists/me/tracks [post]
func (h *Handler) UploadTrack(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxAudioSize+maxImageSize)

	if err := r.ParseMultipartForm(maxAudioSize); err != nil {
		response.BadRequest(w, "request too large or invalid multipart form")
		return
	}

	// Extract artist ID from context (injected by artist middleware)
	artistID := 	middleware.ArtistIDFromContext(r.Context())

	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		response.BadRequest(w, "title is required")
		return
	}

	genreID := r.FormValue("genre_id")
	var genreIDPtr *string
	if genreID != "" {
		genreIDPtr = &genreID
	}

	durationStr := r.FormValue("duration_sec")
	durationSec, _ := strconv.Atoi(durationStr)

	// Handle audio file
	audioFile, audioHeader, err := r.FormFile("audio")
	if err != nil {
		response.BadRequest(w, "audio file is required")
		return
	}
	defer audioFile.Close()

	contentType := audioHeader.Header.Get("Content-Type")
	if !allowedAudioTypes[contentType] {
		response.BadRequest(w, "unsupported audio format. Use MP3, FLAC, WAV, OGG, or M4A")
		return
	}

	audioKey := fmt.Sprintf("tracks/%s/%s", artistID, audioHeader.Filename)
	if err := h.storage.UploadFile(r.Context(), h.audioBucket, audioKey, contentType, audioFile); err != nil {
		response.InternalServerError(w, "failed to upload audio")
		return
	}

	// Handle optional cover image
	var coverURLPtr *string
	coverFile, coverHeader, coverErr := r.FormFile("cover")
	if coverErr == nil {
		defer coverFile.Close()
		coverType := coverHeader.Header.Get("Content-Type")
		if strings.HasPrefix(coverType, "image/") {
			coverKey := fmt.Sprintf("covers/%s/%s", artistID, coverHeader.Filename)
			if err := h.storage.UploadFile(r.Context(), h.imageBucket, coverKey, coverType, coverFile); err == nil {
				url := h.storage.PublicURL(coverKey)
				coverURLPtr = &url
			}
		}
	}

	track := &Track{
		ArtistID:    artistID,
		Title:       title,
		DurationSec: durationSec,
		GenreID:     genreIDPtr,
		CoverURL:    coverURLPtr,
		AudioKey:    audioKey,
		FileSize:    audioHeader.Size,
		MimeType:    contentType,
		Status:      "draft",
	}

	created, err := h.repo.CreateTrack(r.Context(), track)
	if err != nil {
		response.InternalServerError(w, "failed to save track")
		return
	}

	response.Created(w, created)
}

// ListMyTracks returns the authenticated artist's tracks.
//
// @Summary     List my tracks
// @Tags        tracks
// @Security    BearerAuth
// @Router      /artists/me/tracks [get]
func (h *Handler) ListMyTracks(w http.ResponseWriter, r *http.Request) {
	artistID := 	middleware.ArtistIDFromContext(r.Context())
	tracks, err := h.repo.ListTracksByArtist(r.Context(), artistID)
	if err != nil {
		response.InternalServerError(w, "failed to fetch tracks")
		return
	}
	if tracks == nil {
		tracks = []*Track{}
	}
	response.OK(w, map[string]any{"tracks": tracks})
}

// UpdateTrack updates a track's metadata.
//
// @Summary     Update a track
// @Tags        tracks
// @Security    BearerAuth
// @Param       id path string true "Track ID"
// @Router      /artists/me/tracks/{id} [put]
func (h *Handler) UpdateTrack(w http.ResponseWriter, r *http.Request) {
	trackID := chi.URLParam(r, "id")
	artistID := 	middleware.ArtistIDFromContext(r.Context())

	// Verify ownership
	existing, err := h.repo.GetTrackByID(r.Context(), trackID)
	if err != nil || existing.ArtistID != artistID {
		response.NotFound(w, "track not found")
		return
	}

	var body struct {
		Title   string  `json:"title"`
		GenreID *string `json:"genre_id"`
		Status  string  `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		response.BadRequest(w, "invalid request body")
		return
	}

	if body.Title == "" {
		body.Title = existing.Title
	}
	if body.Status == "" {
		body.Status = existing.Status
	}

	updated, err := h.repo.UpdateTrack(r.Context(), trackID, body.Title, body.GenreID, body.Status, nil)
	if err != nil {
		response.InternalServerError(w, "failed to update track")
		return
	}

	response.OK(w, updated)
}

// DeleteTrack deletes a track and its R2 audio file.
//
// @Summary     Delete a track
// @Tags        tracks
// @Security    BearerAuth
// @Param       id path string true "Track ID"
// @Router      /artists/me/tracks/{id} [delete]
func (h *Handler) DeleteTrack(w http.ResponseWriter, r *http.Request) {
	trackID := chi.URLParam(r, "id")
	artistID := 	middleware.ArtistIDFromContext(r.Context())

	audioKey, err := h.repo.DeleteTrack(r.Context(), trackID, artistID)
	if err != nil {
		response.NotFound(w, "track not found or unauthorized")
		return
	}

	// Best-effort R2 cleanup
	_ = h.storage.DeleteFile(r.Context(), h.audioBucket, audioKey)

	response.NoContent(w)
}

// ScheduleTrack sets a future release time for a track.
//
// @Summary     Schedule a track for release
// @Tags        tracks
// @Security    BearerAuth
// @Param       id path string true "Track ID"
// @Router      /artists/me/tracks/{id}/schedule [put]
func (h *Handler) ScheduleTrack(w http.ResponseWriter, r *http.Request) {
	trackID := chi.URLParam(r, "id")
	artistID := 	middleware.ArtistIDFromContext(r.Context())

	var body struct {
		ScheduledAt time.Time `json:"scheduled_at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ScheduledAt.IsZero() {
		response.BadRequest(w, "scheduled_at is required (RFC3339 format)")
		return
	}

	if body.ScheduledAt.Before(time.Now()) {
		response.BadRequest(w, "scheduled_at must be in the future")
		return
	}

	if err := h.repo.ScheduleTrack(r.Context(), trackID, artistID, body.ScheduledAt); err != nil {
		response.InternalServerError(w, "failed to schedule track")
		return
	}

	response.OK(w, map[string]any{"scheduled_at": body.ScheduledAt})
}

// AddCollaborator adds a featured artist to a track.
//
// @Summary     Add collaborator to a track
// @Tags        tracks
// @Security    BearerAuth
// @Param       id path string true "Track ID"
// @Router      /artists/me/tracks/{id}/collaborators [post]
func (h *Handler) AddCollaborator(w http.ResponseWriter, r *http.Request) {
	trackID := chi.URLParam(r, "id")
	artistID := 	middleware.ArtistIDFromContext(r.Context())

	// Verify ownership
	existing, err := h.repo.GetTrackByID(r.Context(), trackID)
	if err != nil || existing.ArtistID != artistID {
		response.NotFound(w, "track not found")
		return
	}

	var body struct {
		ArtistID string `json:"artist_id"`
		Role     string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ArtistID == "" {
		response.BadRequest(w, "artist_id is required")
		return
	}
	if body.Role == "" {
		body.Role = "featured"
	}

	if err := h.repo.AddCollaborator(r.Context(), trackID, body.ArtistID, body.Role); err != nil {
		response.InternalServerError(w, "failed to add collaborator")
		return
	}

	response.Created(w, map[string]string{"message": "collaborator added"})
}

// RemoveCollaborator removes a featured artist from a track.
//
// @Summary     Remove collaborator from a track
// @Tags        tracks
// @Security    BearerAuth
// @Param       id path string true "Track ID"
// @Param       artistId path string true "Collaborator Artist ID"
// @Router      /artists/me/tracks/{id}/collaborators/{artistId} [delete]
func (h *Handler) RemoveCollaborator(w http.ResponseWriter, r *http.Request) {
	trackID := chi.URLParam(r, "id")
	collaboratorID := chi.URLParam(r, "artistId")
	artistID := 	middleware.ArtistIDFromContext(r.Context())

	existing, err := h.repo.GetTrackByID(r.Context(), trackID)
	if err != nil || existing.ArtistID != artistID {
		response.NotFound(w, "track not found")
		return
	}

	if err := h.repo.RemoveCollaborator(r.Context(), trackID, collaboratorID); err != nil {
		response.InternalServerError(w, "failed to remove collaborator")
		return
	}

	response.NoContent(w)
}

// CreateAlbum creates a new album for the artist.
//
// @Summary     Create album
// @Tags        albums
// @Security    BearerAuth
// @Router      /artists/me/albums [post]
func (h *Handler) CreateAlbum(w http.ResponseWriter, r *http.Request) {
	artistID := middleware.ArtistIDFromContext(r.Context())

	if err := r.ParseMultipartForm(5 << 20); err != nil {
		_ = r.ParseForm()
	}

	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		response.BadRequest(w, "title is required")
		return
	}

	albumType := r.FormValue("type")
	if albumType == "" {
		albumType = "album"
	}

	var coverURLPtr *string
	coverFile, coverHeader, coverErr := r.FormFile("cover")
	if coverErr == nil {
		defer coverFile.Close()
		contentType := coverHeader.Header.Get("Content-Type")
		if strings.HasPrefix(contentType, "image/") {
			coverKey := fmt.Sprintf("covers/%s/%s", artistID, coverHeader.Filename)
			if err := h.storage.UploadFile(r.Context(), h.imageBucket, coverKey, contentType, coverFile); err == nil {
				url := h.storage.PublicURL(coverKey)
				coverURLPtr = &url
			}
		}
	}

	album := &Album{
		ArtistID: artistID,
		Title:    title,
		CoverURL: coverURLPtr,
		Type:     albumType,
		Status:   "draft",
	}

	created, err := h.repo.CreateAlbum(r.Context(), album)
	if err != nil {
		response.InternalServerError(w, "failed to create album")
		return
	}

	response.Created(w, created)
}

// GetAlbum returns a public album with its tracks.
//
// @Summary     Get album by ID
// @Tags        albums
// @Param       id path string true "Album ID"
// @Router      /albums/{id} [get]
func (h *Handler) GetAlbum(w http.ResponseWriter, r *http.Request) {
	albumID := chi.URLParam(r, "id")
	album, err := h.repo.GetAlbumByID(r.Context(), albumID)
	if err != nil {
		response.NotFound(w, "album not found")
		return
	}
	response.OK(w, album)
}

// AddTrackToAlbum assigns a track to an album.
//
// @Summary     Add track to album
// @Tags        albums
// @Security    BearerAuth
// @Param       id path string true "Album ID"
// @Router      /artists/me/albums/{id}/tracks [post]
func (h *Handler) AddTrackToAlbum(w http.ResponseWriter, r *http.Request) {
	albumID := chi.URLParam(r, "id")
	artistID := 	middleware.ArtistIDFromContext(r.Context())

	var body struct {
		TrackID string `json:"track_id"`
		Order   int    `json:"order"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.TrackID == "" {
		response.BadRequest(w, "track_id is required")
		return
	}

	if err := h.repo.AddTrackToAlbum(r.Context(), albumID, body.TrackID, artistID, body.Order); err != nil {
		response.InternalServerError(w, "failed to add track to album")
		return
	}

	response.OK(w, map[string]string{"message": "track added to album"})
}

// RemoveTrackFromAlbum removes a track from an album.
//
// @Summary     Remove track from album
// @Tags        albums
// @Security    BearerAuth
// @Param       id path string true "Album ID"
// @Param       trackId path string true "Track ID"
// @Router      /artists/me/albums/{id}/tracks/{trackId} [delete]
func (h *Handler) RemoveTrackFromAlbum(w http.ResponseWriter, r *http.Request) {
	albumID := chi.URLParam(r, "id")
	trackID := chi.URLParam(r, "trackId")
	artistID := 	middleware.ArtistIDFromContext(r.Context())

	if err := h.repo.RemoveTrackFromAlbum(r.Context(), albumID, trackID, artistID); err != nil {
		response.InternalServerError(w, "failed to remove track from album")
		return
	}

	response.NoContent(w)
}

// ListTracks returns paginated published tracks.
//
// @Summary     List all tracks
// @Tags        tracks
// @Router      /tracks [get]
func (h *Handler) ListTracks(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	tracks, err := h.repo.ListPublishedTracks(r.Context(), limit, offset)
	if err != nil {
		response.InternalServerError(w, "failed to fetch tracks")
		return
	}
	if tracks == nil {
		tracks = []*Track{}
	}
	response.OK(w, map[string]any{"tracks": tracks, "limit": limit, "offset": offset})
}

// SearchTracks performs a full-text search on tracks.
//
// @Summary     Search tracks
// @Tags        tracks
// @Param       q query string true "Search query"
// @Router      /tracks/search [get]
func (h *Handler) SearchTracks(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		response.BadRequest(w, "q parameter is required")
		return
	}

	tracks, err := h.repo.SearchTracks(r.Context(), q, 20, 0)
	if err != nil {
		response.InternalServerError(w, "search failed")
		return
	}
	if tracks == nil {
		tracks = []*Track{}
	}
	response.OK(w, map[string]any{"results": tracks, "query": q})
}

// ListGenres returns all available genres.
//
// @Summary     List genres
// @Tags        tracks
// @Router      /genres [get]
func (h *Handler) ListGenres(w http.ResponseWriter, r *http.Request) {
	genres, err := h.repo.ListGenres(r.Context())
	if err != nil {
		response.InternalServerError(w, "failed to fetch genres")
		return
	}
	if genres == nil {
		genres = []*Genre{}
	}
	response.OK(w, map[string]any{"genres": genres})
}

// GetTracksByGenre returns published tracks for a genre.
//
// @Summary     Get tracks by genre
// @Tags        tracks
// @Param       id path string true "Genre ID"
// @Router      /genres/{id}/tracks [get]
func (h *Handler) GetTracksByGenre(w http.ResponseWriter, r *http.Request) {
	genreID := chi.URLParam(r, "id")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	tracks, err := h.repo.GetTracksByGenre(r.Context(), genreID, limit, offset)
	if err != nil {
		response.InternalServerError(w, "failed to fetch tracks")
		return
	}
	if tracks == nil {
		tracks = []*Track{}
	}
	response.OK(w, map[string]any{"tracks": tracks})
}



