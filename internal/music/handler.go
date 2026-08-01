package music

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jaecopzm/zedstream/internal/credits"
	"github.com/jaecopzm/zedstream/pkg/middleware"
	"github.com/jaecopzm/zedstream/pkg/response"
	"github.com/jaecopzm/zedstream/pkg/search"
	"github.com/jaecopzm/zedstream/pkg/storage"
)

const (
	maxAudioSize = 50 << 20 // 50 MB
	maxImageSize = 5 << 20  // 5 MB
)

var allowedAudioTypes = map[string]bool{
  "audio/mpeg": true,
  "audio/mp3":  true,
  "audio/flac": true,
  "audio/wav":  true,
  "audio/wave": true,
  "audio/x-wav": true,
  "audio/ogg":  true,
  "audio/mp4":  true,
  "audio/x-m4a": true,
  "audio/m4a": true,
}

// Handler exposes music HTTP endpoints.
type Handler struct {
	repo         *Repository
	storage      *storage.Client
	searchClient *search.Client
	audioBucket  string
	imageBucket  string
	credits      *credits.Repository
}

// NewHandler creates a new music handler.
func NewHandler(repo *Repository, store *storage.Client, searchClient *search.Client, audioBucket, imageBucket string, creditsRepo *credits.Repository) *Handler {
	return &Handler{
		repo:         repo,
		storage:      store,
		searchClient: searchClient,
		audioBucket:  audioBucket,
		imageBucket:  imageBucket,
		credits:      creditsRepo,
	}
}

// GetArtistTracks returns published tracks for an artist.
func (h *Handler) GetArtistTracks(w http.ResponseWriter, r *http.Request) {
	artistID := chi.URLParam(r, "id")
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

// GetCollaboratorTracks returns tracks where the artist is featured.
func (h *Handler) GetCollaboratorTracks(w http.ResponseWriter, r *http.Request) {
	artistID := chi.URLParam(r, "id")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 20 {
		limit = 10
	}
	tracks, err := h.repo.ListTracksByCollaborator(r.Context(), artistID, limit)
	if err != nil {
		response.InternalServerError(w, "failed to fetch collaborator tracks")
		return
	}
	if tracks == nil {
		tracks = []*Track{}
	}
	response.OK(w, map[string]any{"tracks": tracks})
}

// GetArtistAlbums returns albums for an artist.
func (h *Handler) GetArtistAlbums(w http.ResponseWriter, r *http.Request) {
	artistID := chi.URLParam(r, "id")
	albums, err := h.repo.ListAlbumsByArtist(r.Context(), artistID)
	if err != nil {
		response.InternalServerError(w, "failed to fetch albums")
		return
	}
	if albums == nil {
		albums = []*Album{}
	}
	response.OK(w, map[string]any{"albums": albums})
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

	artistID := middleware.ArtistIDFromContext(r.Context())

	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		response.BadRequest(w, "title is required")
		return
	}

	// Check credits before doing any expensive work.
	if h.credits != nil {
		bal, err := h.credits.GetBalance(r.Context(), artistID)
		if err != nil {
			response.InternalServerError(w, "failed to check credit balance")
			return
		}
		if bal.Balance < 1 {
			response.JSON(w, 402, map[string]any{
				"error":               "insufficient credits",
				"balance":             0,
				"price_per_credit_zmw": credits.PricePerCreditZMW,
			})
			return
		}
	}

	genreIDPtr := h.repo.ResolveGenre(r.Context(), r.FormValue("genre_id"))

	// Handle inline album creation
	albumIDStr := r.FormValue("album_id")
	albumTitle := strings.TrimSpace(r.FormValue("album_title"))
	if albumIDStr == "" && albumTitle != "" {
		album := &Album{
			ArtistID: artistID,
			Title:    albumTitle,
			Type:     "single",
			Status:   "draft",
		}
		if coverFile, _, coverErr := r.FormFile("cover"); coverErr == nil {
			coverFile.Close()
		}
		created, err := h.repo.CreateAlbum(r.Context(), album)
		if err == nil {
			albumIDStr = created.ID
		}
	}

	var albumIDPtr *string
	if albumIDStr != "" {
		albumIDPtr = &albumIDStr
	}

	// Save audio to temp file for metadata detection + upload
	audioFile, audioHeader, err := r.FormFile("audio")
	if err != nil {
		response.BadRequest(w, "audio file is required")
		return
	}
	defer audioFile.Close()

	contentType := audioHeader.Header.Get("Content-Type")
	// Strip parameters (e.g. "audio/mpeg; charset=binary") and lowercase
	if idx := strings.Index(contentType, ";"); idx != -1 {
		contentType = strings.TrimSpace(contentType[:idx])
	}
	contentType = strings.ToLower(contentType)
	if !allowedAudioTypes[contentType] {
		response.BadRequest(w, "unsupported audio format. Use MP3, FLAC, WAV, OGG, or M4A")
		return
	}

	tmpDir, err := os.MkdirTemp("", "zedstream-upload-*")
	if err != nil {
		response.InternalServerError(w, "failed to process audio")
		return
	}
	defer os.RemoveAll(tmpDir)

	tmpPath := filepath.Join(tmpDir, audioHeader.Filename)
	tmpFile, err := os.Create(tmpPath)
	if err != nil {
		response.InternalServerError(w, "failed to process audio")
		return
	}
	if _, err := io.Copy(tmpFile, audioFile); err != nil {
		tmpFile.Close()
		response.InternalServerError(w, "failed to process audio")
		return
	}
	tmpFile.Close()

	// Auto-detect duration if not provided
	durationStr := r.FormValue("duration_sec")
	durationSec, _ := strconv.Atoi(durationStr)
	if durationSec <= 0 {
		durationSec = DetectDuration(tmpPath)
	}

	// Upload to R2 from temp file
	audioKey := fmt.Sprintf("tracks/%s/%s", artistID, audioHeader.Filename)
	uploadFile, err := os.Open(tmpPath)
	if err != nil {
		response.InternalServerError(w, "failed to upload audio")
		return
	}
	defer uploadFile.Close()

	if err := h.storage.UploadFile(r.Context(), h.audioBucket, audioKey, contentType, uploadFile); err != nil {
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

	description := strings.TrimSpace(r.FormValue("description"))

	track := &Track{
		ArtistID:    artistID,
		AlbumID:     albumIDPtr,
		Title:       title,
		DurationSec: durationSec,
		GenreID:     genreIDPtr,
		CoverURL:    coverURLPtr,
		AudioKey:    audioKey,
		FileSize:    audioHeader.Size,
		MimeType:    contentType,
		Status:      "draft",
		Description: &description,
	}
	if description == "" {
		track.Description = nil
	}

	created, err := h.repo.CreateTrack(r.Context(), track)
	if err != nil {
		response.InternalServerError(w, "failed to save track")
		return
	}

	// Deduct one credit for the upload.
	if h.credits != nil {
		if err := h.credits.DeductForTrack(r.Context(), artistID, created.ID); err != nil {
			slog.Warn("credit deduction failed", "artist_id", artistID, "track_id", created.ID, "error", err)
		}
	}

	// Handle featured artists
	featStr := r.FormValue("featured_artists")
	if featStr != "" {
		for _, name := range strings.Split(featStr, ",") {
			name = strings.TrimSpace(name)
			if name == "" || strings.EqualFold(name, title) {
				continue
			}
			featID, err := h.repo.FindOrCreateArtist(r.Context(), name)
			if err != nil {
				continue
			}
			_ = h.repo.AddCollaborator(r.Context(), created.ID, featID, "featured")
		}
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
		Title       string  `json:"title"`
		GenreID     *string `json:"genre_id"`
		Status      string  `json:"status"`
		Description *string `json:"description"`
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

	updated, err := h.repo.UpdateTrack(r.Context(), trackID, body.Title, body.GenreID, body.Status, nil, body.Description)
	if err != nil {
		response.InternalServerError(w, "failed to update track")
		return
	}

	// Sync with Meilisearch
	if updated.Status == "published" {
		fullTrack, _ := h.repo.GetTrackByID(r.Context(), trackID)
		if fullTrack != nil {
			cover := ""
			if fullTrack.CoverURL != nil {
				cover = *fullTrack.CoverURL
			}
			_ = h.searchClient.IndexTrack(r.Context(), search.TrackDocument{
				ID:          fullTrack.ID,
				Title:       fullTrack.Title,
				ArtistName:  fullTrack.ArtistName,
				DurationSec: fullTrack.DurationSec,
				PlayCount:   fullTrack.PlayCount,
				CoverURL:    cover,
			})
		}
	} else {
		_ = h.searchClient.DeleteTrack(r.Context(), trackID)
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

	// Remove from search index
	_ = h.searchClient.DeleteTrack(r.Context(), trackID)

	// Refund 1 credit if the artist uploaded this track
	if h.credits != nil {
		h.credits.RefundForTrack(r.Context(), artistID, trackID)
	}

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
	section := r.URL.Query().Get("section")
	if limit <= 0 {
		limit = 20
	}
	if limit > 500 {
		limit = 500
	}

	tracks, err := h.repo.ListPublishedTracks(r.Context(), limit, offset, section)
	if err != nil {
		response.InternalServerError(w, "failed to fetch tracks")
		return
	}
	if tracks == nil {
		tracks = []*Track{}
	}
	response.OK(w, map[string]any{"tracks": tracks, "limit": limit, "offset": offset})
}

// GetTrack returns a track by ID.
//
// @Summary     Get track by ID
// @Tags        tracks
// @Param       id path string true "Track ID"
// @Router      /tracks/{id} [get]
func (h *Handler) GetTrack(w http.ResponseWriter, r *http.Request) {
	trackID := chi.URLParam(r, "id")
	track, err := h.repo.GetTrackByID(r.Context(), trackID)
	if err != nil {
		response.NotFound(w, "track not found")
		return
	}
	response.OK(w, track)
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

// ListAlbums returns published albums for the home page.
func (h *Handler) ListAlbums(w http.ResponseWriter, r *http.Request) {
	limit := 20
	offset := 0
	if l, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && l > 0 {
		limit = l
	}
	if limit > 500 {
		limit = 500
	}
	if o, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && o >= 0 {
		offset = o
	}
	albums, err := h.repo.ListFeaturedAlbums(r.Context(), limit, offset)
	if err != nil {
		response.InternalServerError(w, "failed to fetch albums")
		return
	}
	if albums == nil {
		albums = []*Album{}
	}
	response.OK(w, map[string]any{"albums": albums})
}

// ListSitemap returns lightweight ID/timestamp lists for published tracks, albums, and artists.
// Used by the frontend sitemap generator to keep crawl data fast.
func (h *Handler) ListSitemap(w http.ResponseWriter, r *http.Request) {
	tracks, albums, artists, err := h.repo.ListSitemapURLs(r.Context())
	if err != nil {
		response.InternalServerError(w, "failed to fetch sitemap data")
		return
	}
	response.OK(w, map[string]any{
		"tracks":  tracks,
		"albums":  albums,
		"artists": artists,
	})
}

// SearchAlbums searches published albums by title, artist name, or collaborator.
func (h *Handler) SearchAlbums(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		response.BadRequest(w, "q parameter is required")
		return
	}
	albums, err := h.repo.SearchAlbums(r.Context(), q, 20, 0)
	if err != nil {
		response.InternalServerError(w, "search failed")
		return
	}
	if albums == nil {
		albums = []*Album{}
	}
	response.OK(w, map[string]any{"results": albums, "query": q})
}
