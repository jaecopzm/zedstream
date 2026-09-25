package resume

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jaecopzm/zedstream/pkg/middleware"
	"github.com/jaecopzm/zedstream/pkg/response"
)

type Handler struct {
	repo *Repository
}

func NewHandler(db *pgxpool.Pool) *Handler {
	return &Handler{repo: NewRepository(db)}
}

type putRequest struct {
	TrackID     string `json:"track_id"`
	PositionSec int    `json:"position_sec"`
	DurationSec int    `json:"duration_sec"`
}

// Put handles PUT /api/v1/me/resume — save where the user left off.
func (h *Handler) Put(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	if userID == "" {
		response.Unauthorized(w, "authentication required")
		return
	}
	var req putRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.BadRequest(w, "invalid json")
		return
	}
	if req.TrackID == "" {
		response.BadRequest(w, "track_id is required")
		return
	}
	if req.PositionSec < 0 || req.DurationSec < 0 {
		response.BadRequest(w, "positions must be >= 0")
		return
	}
	if err := h.repo.Upsert(r.Context(), userID, req.TrackID, req.PositionSec, req.DurationSec); err != nil {
		response.InternalServerError(w, "failed to save resume point")
		return
	}
	response.OK(w, map[string]bool{"ok": true})
}

// List handles GET /api/v1/me/resume — Jump Back In rail data.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	if userID == "" {
		response.Unauthorized(w, "authentication required")
		return
	}
	limit := 10
	if q := r.URL.Query().Get("limit"); q != "" {
		if n, err := strconv.Atoi(q); err == nil {
			limit = n
		}
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 25 {
		limit = 25
	}
	points, err := h.repo.List(r.Context(), userID, limit)
	if err != nil {
		response.InternalServerError(w, "failed to load resume points")
		return
	}
	response.OK(w, map[string]any{"resume": points})
}

// Delete handles DELETE /api/v1/me/resume/{trackId}.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	if userID == "" {
		response.Unauthorized(w, "authentication required")
		return
	}
	trackID := chi.URLParam(r, "trackId")
	if trackID == "" {
		response.BadRequest(w, "trackId is required")
		return
	}
	if err := h.repo.Delete(r.Context(), userID, trackID); err != nil {
		response.InternalServerError(w, "failed to delete resume point")
		return
	}
	response.OK(w, map[string]bool{"ok": true})
}
