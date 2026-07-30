package artist

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jaecopzm/zedstream/internal/credits"
	"github.com/jaecopzm/zedstream/pkg/middleware"
	"github.com/jaecopzm/zedstream/pkg/response"
	"github.com/jaecopzm/zedstream/pkg/storage"
)

// Handler exposes artist HTTP endpoints.
type Handler struct {
	repo    *Repository
	storage *storage.Client
	bucket  string
	credits *credits.Repository
}

// NewHandler creates a new artist handler.
func NewHandler(repo *Repository, store *storage.Client, imageBucket string, creditsRepo *credits.Repository) *Handler {
	return &Handler{repo: repo, storage: store, bucket: imageBucket, credits: creditsRepo}
}

// Register upgrades the authenticated user to an artist.
//
// @Summary     Register as an artist
// @Tags        artists
// @Security    BearerAuth
// @Accept      json
// @Produce     json
// @Router      /artists/register [post]
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())

	if err := r.ParseMultipartForm(5 << 20); err != nil {
		_ = r.ParseForm()
	}

	stageName := strings.TrimSpace(r.FormValue("stage_name"))
	if stageName == "" {
		response.BadRequest(w, "stage_name is required")
		return
	}

	var photoURL *string
	file, header, fileErr := r.FormFile("photo")
	if fileErr == nil {
		defer file.Close()
		contentType := header.Header.Get("Content-Type")
		if !strings.HasPrefix(contentType, "image/") {
			response.BadRequest(w, "photo must be an image")
			return
		}
		ext := filepath.Ext(header.Filename)
		if ext == "" {
			ext = ".jpg"
		}
		key := fmt.Sprintf("artists/%s/photo%s", userID, ext)
		if err := h.storage.UploadFile(r.Context(), h.bucket, key, contentType, file); err != nil {
			response.InternalServerError(w, "failed to upload photo")
			return
		}
		url := h.storage.PublicURL(key)
		photoURL = &url
	}

	a, err := h.repo.Create(r.Context(), userID, stageName)
	if err != nil {
		if strings.Contains(err.Error(), "already an artist") {
			response.Conflict(w, "user is already an artist")
			return
		}
		response.InternalServerError(w, "failed to register artist")
		return
	}

	if photoURL != nil {
		a, err = h.repo.UpdateProfile(r.Context(), a.ID, stageName, "", photoURL, nil, nil, "", nil)
		if err != nil {
			response.InternalServerError(w, "failed to save photo")
			return
		}
	}

	// Grant the free starter credits.
	if h.credits != nil {
		if _, err := h.credits.GrantCredits(r.Context(), a.ID, credits.FreeCreditsOnSignup, credits.TypeFreeGrant, "welcome bonus"); err != nil {
			slog.Warn("failed to grant signup credits", "artist_id", a.ID, "error", err)
			// Non-fatal — the artist can still be created.
		}
	}

	response.Created(w, a)
}

// GetMe returns the authenticated artist's profile.
//
// @Summary     Get my artist profile
// @Tags        artists
// @Security    BearerAuth
// @Router      /artists/me [get]
func (h *Handler) GetMe(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	a, err := h.repo.GetByUserID(r.Context(), userID)
	if err != nil {
		response.NotFound(w, "artist profile not found")
		return
	}
	response.OK(w, a)
}

// UpdateMe updates the authenticated artist's profile.
//
// @Summary     Update my artist profile
// @Tags        artists
// @Security    BearerAuth
// @Router      /artists/me [put]
func (h *Handler) UpdateMe(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())

	a, err := h.repo.GetByUserID(r.Context(), userID)
	if err != nil {
		response.NotFound(w, "artist profile not found")
		return
	}

	// Parse multipart form (photo optional)
	if err := r.ParseMultipartForm(5 << 20); err != nil {
		// Fallback to JSON if no multipart
		_ = r.ParseForm()
	}

	stageName := r.FormValue("stage_name")
	if stageName == "" {
		stageName = a.StageName
	}
	bio := r.FormValue("bio")

	var photoURL *string

	// Handle optional photo upload
	file, header, fileErr := r.FormFile("photo")
	if fileErr == nil {
		defer file.Close()
		contentType := header.Header.Get("Content-Type")
		if !strings.HasPrefix(contentType, "image/") {
			response.BadRequest(w, "photo must be an image")
			return
		}
		key := fmt.Sprintf("artists/%s/photo%s", a.ID, extensionFromMime(contentType))
		if err := h.storage.UploadFile(r.Context(), h.bucket, key, contentType, file); err != nil {
			response.InternalServerError(w, "failed to upload photo")
			return
		}
		url := h.storage.PublicURL(key)
		photoURL = &url
	}

	var coverURL *string

	// Handle optional cover upload
	coverFile, coverHeader, coverErr := r.FormFile("cover")
	if coverErr == nil {
		defer coverFile.Close()
		contentType := coverHeader.Header.Get("Content-Type")
		if !strings.HasPrefix(contentType, "image/") {
			response.BadRequest(w, "cover must be an image")
			return
		}
		key := fmt.Sprintf("artists/%s/cover%s", a.ID, extensionFromMime(contentType))
		if err := h.storage.UploadFile(r.Context(), h.bucket, key, contentType, coverFile); err != nil {
			response.InternalServerError(w, "failed to upload cover")
			return
		}
		url := h.storage.PublicURL(key)
		coverURL = &url
	}

	// Parse social_links from form (JSON string)
	var socialLinks map[string]any
	if sl := r.FormValue("social_links"); sl != "" {
		if err := json.Unmarshal([]byte(sl), &socialLinks); err != nil {
			response.BadRequest(w, "invalid social_links JSON")
			return
		}
	} else {
		socialLinks = a.SocialLinks
	}

	location := r.FormValue("location")

	// Parse genre_tags from form (JSON array string)
	var genreTags []string
	if gt := r.FormValue("genre_tags"); gt != "" {
		if err := json.Unmarshal([]byte(gt), &genreTags); err != nil {
			response.BadRequest(w, "invalid genre_tags JSON")
			return
		}
	} else if a.GenreTags != nil {
		genreTags = a.GenreTags
	} else {
		genreTags = []string{}
	}

	updated, err := h.repo.UpdateProfile(r.Context(), a.ID, stageName, bio, photoURL, socialLinks, coverURL, location, genreTags)
	if err != nil {
		response.InternalServerError(w, "failed to update profile")
		return
	}

	response.OK(w, updated)
}

// GetByID returns a public artist profile by ID.
//
// @Summary     Get artist by ID
// @Tags        artists
// @Param       id path string true "Artist ID"
// @Router      /artists/{id} [get]
func (h *Handler) GetByID(w http.ResponseWriter, r *http.Request) {
	artistID := chi.URLParam(r, "id")
	currentUserID := middleware.UserIDFromContext(r.Context())
	a, err := h.repo.GetByID(r.Context(), artistID, currentUserID)
	if err != nil {
		response.NotFound(w, "artist not found")
		return
	}
	response.OK(w, a)
}

// ListFeatured returns top artists by follower count for the home page.
func (h *Handler) ListFeatured(w http.ResponseWriter, r *http.Request) {
	artists, err := h.repo.ListFeatured(r.Context(), 10)
	if err != nil {
		response.InternalServerError(w, "failed to fetch featured artists")
		return
	}
	if artists == nil {
		artists = []*Artist{}
	}
	response.OK(w, map[string]any{"artists": artists})
}

// Search searches artists by stage name.
//
// @Summary     Search artists
// @Tags        artists
// @Param       q query string true "Search query"
// @Router      /artists/search [get]
func (h *Handler) Search(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		response.BadRequest(w, "q parameter is required")
		return
	}

	artists, err := h.repo.Search(r.Context(), q, 20, 0)
	if err != nil {
		response.InternalServerError(w, "search failed")
		return
	}

	if artists == nil {
		artists = []*Artist{}
	}

	response.OK(w, map[string]any{"results": artists})
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
