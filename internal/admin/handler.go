package admin

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jaecopzm/zedstream/internal/artist"
	"github.com/jaecopzm/zedstream/internal/music"
	"github.com/jaecopzm/zedstream/pkg/response"
	"github.com/jaecopzm/zedstream/pkg/storage"
)

type Handler struct {
	musicRepo   *music.Repository
	artistRepo  *artist.Repository
	storage     *storage.Client
	audioBucket string
	imageBucket string
	db          *pgxpool.Pool
}

func NewHandler(
	musicRepo *music.Repository,
	artistRepo *artist.Repository,
	store *storage.Client,
	db *pgxpool.Pool,
	audioBucket, imageBucket string,
) *Handler {
	return &Handler{
		musicRepo:    musicRepo,
		artistRepo:   artistRepo,
		storage:      store,
		db:           db,
		audioBucket:  audioBucket,
		imageBucket:  imageBucket,
	}
}

// ListArtists returns all artists for the admin dropdown.
func (h *Handler) ListArtists(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 200 {
		limit = 100
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

	artists, err := h.artistRepo.ListAll(r.Context(), limit, offset)
	if err != nil {
		response.InternalServerError(w, "failed to fetch artists")
		return
	}
	if artists == nil {
		artists = []*artist.Artist{}
	}
	response.OK(w, map[string]any{"artists": artists})
}

// UpdateArtist updates an artist's profile by ID (admin).
func (h *Handler) UpdateArtist(w http.ResponseWriter, r *http.Request) {
	artistID := chi.URLParam(r, "id")
	if artistID == "" {
		response.BadRequest(w, "artist id is required")
		return
	}

	if err := r.ParseMultipartForm(5 << 20); err != nil {
		_ = r.ParseForm()
	}

	a, err := h.artistRepo.GetByID(r.Context(), artistID, "")
	if err != nil {
		response.NotFound(w, "artist not found")
		return
	}

	stageName := strings.TrimSpace(r.FormValue("stage_name"))
	if stageName == "" {
		stageName = a.StageName
	}
	bio := r.FormValue("bio")

	var photoURL *string
	file, header, fileErr := r.FormFile("photo")
	if fileErr == nil {
		defer file.Close()
		contentType := header.Header.Get("Content-Type")
		if !strings.HasPrefix(contentType, "image/") {
			response.BadRequest(w, "photo must be an image")
			return
		}
		key := fmt.Sprintf("artists/%s/photo%s", a.ID, extensionFromMime(contentType))
		if err := h.storage.UploadFile(r.Context(), h.imageBucket, key, contentType, file); err != nil {
			response.InternalServerError(w, "failed to upload photo")
			return
		}
		url := h.storage.PublicURL(key)
		photoURL = &url
	}

	var coverURL *string
	coverFile, coverHeader, coverErr := r.FormFile("cover")
	if coverErr == nil {
		defer coverFile.Close()
		contentType := coverHeader.Header.Get("Content-Type")
		if !strings.HasPrefix(contentType, "image/") {
			response.BadRequest(w, "cover must be an image")
			return
		}
		key := fmt.Sprintf("artists/%s/cover%s", a.ID, extensionFromMime(contentType))
		if err := h.storage.UploadFile(r.Context(), h.imageBucket, key, contentType, coverFile); err != nil {
			response.InternalServerError(w, "failed to upload cover")
			return
		}
		url := h.storage.PublicURL(key)
		coverURL = &url
	}

	verified := r.FormValue("verified") == "true"
	if verified != a.Verified {
		if _, dbErr := h.db.Exec(r.Context(),
			`UPDATE artists SET verified = $1, updated_at = NOW() WHERE id = $2`,
			verified, artistID,
		); dbErr != nil {
			response.InternalServerError(w, "failed to update verified status")
			return
		}
	}

	socialLinks := a.SocialLinks
	if sl := r.FormValue("social_links"); sl != "" {
		if err := json.Unmarshal([]byte(sl), &socialLinks); err != nil {
			response.BadRequest(w, "invalid social_links JSON")
			return
		}
	}

	location := r.FormValue("location")
	if location == "" {
		location = ""
		if locPtr := a.Location; locPtr != nil {
			location = *locPtr
		}
	}

	var genreTags []string
	if gt := r.FormValue("genre_tags"); gt != "" {
		if err := json.Unmarshal([]byte(gt), &genreTags); err != nil {
			response.BadRequest(w, "invalid genre_tags JSON")
			return
		}
	} else {
		genreTags = a.GenreTags
	}

	updated, err := h.artistRepo.UpdateProfile(r.Context(), artistID, stageName, bio, photoURL, socialLinks, coverURL, location, genreTags)
	if err != nil {
		response.InternalServerError(w, "failed to update artist")
		return
	}

	response.OK(w, updated)
}

// DeleteArtist removes an artist profile by ID (admin).
func (h *Handler) DeleteArtist(w http.ResponseWriter, r *http.Request) {
	artistID := chi.URLParam(r, "id")
	if artistID == "" {
		response.BadRequest(w, "artist id is required")
		return
	}

	if err := h.artistRepo.Delete(r.Context(), artistID); err != nil {
		response.InternalServerError(w, "failed to delete artist")
		return
	}

	response.OK(w, map[string]any{"status": "deleted"})
}

// UploadTrack uploads a track on behalf of any artist.
// Admin specifies artist_id in the form.
func (h *Handler) UploadTrack(w http.ResponseWriter, r *http.Request) {
	const maxSize = 55 << 20
	r.Body = http.MaxBytesReader(w, r.Body, maxSize)

	if err := r.ParseMultipartForm(50 << 20); err != nil {
		response.BadRequest(w, "request too large or invalid multipart form")
		return
	}

	artistID := strings.TrimSpace(r.FormValue("artist_id"))
	if artistID == "" {
		response.BadRequest(w, "artist_id is required")
		return
	}

	if _, err := h.artistRepo.GetByID(r.Context(), artistID, ""); err != nil {
		response.NotFound(w, "artist not found")
		return
	}

	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		response.BadRequest(w, "title is required")
		return
	}

	genreIDPtr := h.musicRepo.ResolveGenre(r.Context(), r.FormValue("genre_id"))

	// Handle inline album creation
	albumIDStr := r.FormValue("album_id")
	albumTitle := strings.TrimSpace(r.FormValue("album_title"))
	if albumIDStr == "" && albumTitle != "" {
		album := &music.Album{
			ArtistID: artistID,
			Title:    albumTitle,
			Type:     "single",
			Status:   "draft",
		}
		created, err := h.musicRepo.CreateAlbum(r.Context(), album)
		if err == nil {
			albumIDStr = created.ID
		}
	}

	var albumIDPtr *string
	if albumIDStr != "" {
		albumIDPtr = &albumIDStr
	}

	description := strings.TrimSpace(r.FormValue("description"))
	trackOrder, _ := strconv.Atoi(r.FormValue("track_order"))
	publishNow := r.FormValue("publish") == "true"

	// Save audio to temp file for metadata detection + upload
	audioFile, audioHeader, err := r.FormFile("audio")
	if err != nil {
		response.BadRequest(w, "audio file is required")
		return
	}
	defer audioFile.Close()

	contentType := audioHeader.Header.Get("Content-Type")
	allowedAudioTypes := map[string]bool{
		"audio/mpeg": true, "audio/flac": true,
		"audio/wav": true, "audio/ogg": true, "audio/mp4": true,
	}
	if !allowedAudioTypes[contentType] {
		response.BadRequest(w, "unsupported audio format. Use MP3, FLAC, WAV, OGG, or M4A")
		return
	}

	tmpDir, err := os.MkdirTemp("", "zedstream-admin-upload-*")
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
	durationSec, _ := strconv.Atoi(r.FormValue("duration_sec"))
	if durationSec <= 0 {
		durationSec = music.DetectDuration(tmpPath)
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

	status := "draft"
	if publishNow {
		status = "published"
	}

	section := strings.TrimSpace(r.FormValue("section"))

	track := &music.Track{
		ArtistID:    artistID,
		AlbumID:     albumIDPtr,
		Title:       title,
		DurationSec: durationSec,
		GenreID:     genreIDPtr,
		CoverURL:    coverURLPtr,
		AudioKey:    audioKey,
		FileSize:    audioHeader.Size,
		MimeType:    contentType,
		Status:      status,
		TrackOrder:  trackOrder,
		Section:     section,
		Description: &description,
	}
	if description == "" {
		track.Description = nil
	}

	created, err := h.musicRepo.CreateTrack(r.Context(), track)
	if err != nil {
		response.InternalServerError(w, "failed to save track")
		return
	}

	// If album specified, add track to album
	if albumIDPtr != nil && trackOrder > 0 {
		_ = h.musicRepo.AddTrackToAlbum(r.Context(), *albumIDPtr, created.ID, artistID, trackOrder)
	}

	// Handle featured artists
	featStr := r.FormValue("featured_artists")
	if featStr != "" {
		for _, name := range strings.Split(featStr, ",") {
			name = strings.TrimSpace(name)
			if name == "" || strings.EqualFold(name, title) {
				continue
			}
			featID, err := h.musicRepo.FindOrCreateArtist(r.Context(), name)
			if err != nil {
				continue
			}
			_ = h.musicRepo.AddCollaborator(r.Context(), created.ID, featID, "featured")
		}
	}

	response.Created(w, created)
}

// CreateAlbum creates an album for any artist.
func (h *Handler) CreateAlbum(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		_ = r.ParseForm()
	}

	artistID := strings.TrimSpace(r.FormValue("artist_id"))
	if artistID == "" {
		response.BadRequest(w, "artist_id is required")
		return
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

	publishNow := r.FormValue("publish") == "true"

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

	status := "draft"
	if publishNow {
		status = "published"
	}

	album := &music.Album{
		ArtistID: artistID,
		Title:    title,
		CoverURL: coverURLPtr,
		Type:     albumType,
		Status:   status,
	}

	created, err := h.musicRepo.CreateAlbum(r.Context(), album)
	if err != nil {
		response.InternalServerError(w, "failed to create album")
		return
	}

	response.Created(w, created)
}

// ListAlbums returns all albums with optional artist_id filter.
func (h *Handler) ListAlbums(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 200 {
		limit = 50
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	artistID := r.URL.Query().Get("artist_id")

	var albums []*music.Album
	var err error

	if artistID != "" {
		// Use the track listing approach via artist - we'll fetch albums via the music repo
		// For now, simple approach: fetch all published tracks and group
		albums, err = h.musicRepo.ListAlbumsByArtist(r.Context(), artistID)
	} else {
		albums, err = h.musicRepo.ListAllAlbums(r.Context(), limit, offset)
	}

	if err != nil {
		response.InternalServerError(w, "failed to fetch albums")
		return
	}
	if albums == nil {
		albums = []*music.Album{}
	}
	response.OK(w, map[string]any{"albums": albums})
}

// ListTracks returns all tracks with optional filters.
func (h *Handler) ListTracks(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 200 {
		limit = 50
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	artistID := r.URL.Query().Get("artist_id")

	var tracks []*music.Track
	var err error

	if artistID != "" {
		tracks, err = h.musicRepo.ListTracksByArtist(r.Context(), artistID)
	} else {
		tracks, err = h.musicRepo.ListAllTracks(r.Context(), limit, offset)
	}

	if err != nil {
		response.InternalServerError(w, "failed to fetch tracks")
		return
	}
	if tracks == nil {
		tracks = []*music.Track{}
	}
	response.OK(w, map[string]any{"tracks": tracks})
}

type updateTrackRequest struct {
	Section *string `json:"section,omitempty"`
	Status  *string `json:"status,omitempty"`
	GenreID *string `json:"genre_id,omitempty"`
}

// UpdateTrack updates track metadata (section, status, genre).
func (h *Handler) UpdateTrack(w http.ResponseWriter, r *http.Request) {
	trackID := chi.URLParam(r, "id")
	if trackID == "" {
		response.BadRequest(w, "track id is required")
		return
	}

	var req updateTrackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.BadRequest(w, "invalid JSON body")
		return
	}

	track, err := h.musicRepo.GetTrackByID(r.Context(), trackID)
	if err != nil {
		response.NotFound(w, "track not found")
		return
	}

	if req.Section != nil {
		track.Section = *req.Section
	}
	if req.Status != nil {
		track.Status = *req.Status
	}
	if req.GenreID != nil {
		track.GenreID = req.GenreID
	}

	updated, err := h.musicRepo.UpdateTrack(r.Context(), trackID, track.Title, track.GenreID, track.Status, track.CoverURL, nil)
	if err != nil {
		response.InternalServerError(w, "failed to update track")
		return
	}

	response.OK(w, updated)
}

// UpdateAlbum modifies an album's metadata.
func (h *Handler) UpdateAlbum(w http.ResponseWriter, r *http.Request) {
	albumID := chi.URLParam(r, "id")
	if albumID == "" {
		response.BadRequest(w, "album id is required")
		return
	}

	if err := r.ParseMultipartForm(10 << 20); err != nil {
		_ = r.ParseForm()
	}

	a, err := h.musicRepo.GetAlbumByID(r.Context(), albumID)
	if err != nil {
		response.NotFound(w, "album not found")
		return
	}

	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		title = a.Title
	}

	albumType := r.FormValue("type")
	if albumType == "" {
		albumType = a.Type
	}

	status := r.FormValue("status")
	if status == "" {
		status = a.Status
	}

	var coverURL *string
	coverFile, coverHeader, coverErr := r.FormFile("cover")
	if coverErr == nil {
		defer coverFile.Close()
		contentType := coverHeader.Header.Get("Content-Type")
		if strings.HasPrefix(contentType, "image/") {
			key := fmt.Sprintf("covers/album/%s%s", a.ID, extensionFromMime(contentType))
			if err := h.storage.UploadFile(r.Context(), h.imageBucket, key, contentType, coverFile); err == nil {
				url := h.storage.PublicURL(key)
				coverURL = &url
			}
		}
	}

	updated, err := h.musicRepo.UpdateAlbum(r.Context(), albumID, title, coverURL, albumType, status)
	if err != nil {
		response.InternalServerError(w, "failed to update album")
		return
	}

	response.OK(w, updated)
}

// DeleteAlbum removes an album by ID (admin).
func (h *Handler) DeleteAlbum(w http.ResponseWriter, r *http.Request) {
	albumID := chi.URLParam(r, "id")
	if albumID == "" {
		response.BadRequest(w, "album id is required")
		return
	}

	if err := h.musicRepo.DeleteAlbum(r.Context(), albumID); err != nil {
		response.InternalServerError(w, "failed to delete album")
		return
	}

	response.OK(w, map[string]any{"status": "deleted"})
}

func extensionFromMime(mime string) string {
	switch mime {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "audio/mpeg":
		return ".mp3"
	case "audio/flac":
		return ".flac"
	case "audio/wav":
		return ".wav"
	case "audio/ogg":
		return ".ogg"
	case "audio/mp4":
		return ".m4a"
	default:
		return ""
	}
}
