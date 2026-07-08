package artist

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jaecopzm/zedstream/pkg/middleware"
	"github.com/jaecopzm/zedstream/pkg/response"
	"github.com/jaecopzm/zedstream/pkg/storage"
)

// Handler exposes artist HTTP endpoints.
type Handler struct {
	repo    *Repository
	storage *storage.Client
	bucket  string
}

// NewHandler creates a new artist handler.
func NewHandler(repo *Repository, store *storage.Client, imageBucket string) *Handler {
	return &Handler{repo: repo, storage: store, bucket: imageBucket}
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

	var body struct {
		StageName string `json:"stage_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.StageName) == "" {
		response.BadRequest(w, "stage_name is required")
		return
	}

	a, err := h.repo.Create(r.Context(), userID, strings.TrimSpace(body.StageName))
	if err != nil {
		if strings.Contains(err.Error(), "already an artist") {
			response.Conflict(w, "user is already an artist")
			return
		}
		response.InternalServerError(w, "failed to register artist")
		return
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

	updated, err := h.repo.UpdateProfile(r.Context(), a.ID, stageName, bio, photoURL)
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
	a, err := h.repo.GetByID(r.Context(), artistID)
	if err != nil {
		response.NotFound(w, "artist not found")
		return
	}
	response.OK(w, a)
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
