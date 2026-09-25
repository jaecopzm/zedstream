package downloader

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jaecopzm/zedstream/pkg/middleware"
	"github.com/jaecopzm/zedstream/pkg/response"
	"github.com/jaecopzm/zedstream/pkg/storage"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Handler struct {
	svc     *Service
	storage *storage.Client
	bucket  string
}

func NewHandler(db *pgxpool.Pool, store *storage.Client, audioBucket, imageBucket string) *Handler {
	// concurrency 4 = 4 parallel yt-dlp jobs; each uses 8 fragment connections -> 32 total connections
	svc := NewService(db, store, audioBucket, imageBucket, 4)
	return &Handler{svc: svc, storage: store, bucket: audioBucket}
}

// Create handles POST /api/v1/downloader/jobs
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	if userID == "" {
		response.Unauthorized(w, "authentication required")
		return
	}
	var req CreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.BadRequest(w, "invalid json")
		return
	}
	if req.URL == "" {
		response.BadRequest(w, "url is required")
		return
	}
	if req.Quality == "" {
		req.Quality = "192"
	}
	if req.Quality != "128" && req.Quality != "192" && req.Quality != "256" && req.Quality != "320" {
		response.BadRequest(w, "quality must be one of 128,192,256,320")
		return
	}
	job, err := h.svc.Create(r.Context(), userID, req.URL, req.Quality, req.Title)
	if err != nil {
		response.InternalServerError(w, "failed to create job")
		return
	}
	response.Created(w, job)
}

// List handles GET /api/v1/downloader/jobs
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	jobs, err := h.svc.List(r.Context(), userID, limit, offset)
	if err != nil {
		response.InternalServerError(w, "failed to list jobs")
		return
	}
	if jobs == nil {
		jobs = []*Job{}
	}
	// attach signed urls for completed
	for _, j := range jobs {
		if j.Status == StatusCompleted && j.AudioKey != nil {
			if url, err := h.storage.GetSignedURL(r.Context(), h.bucket, *j.AudioKey, 2*3600000000000); err == nil { // 2h
				j.StreamURL = &url
			}
		}
	}
	response.OK(w, map[string]any{"jobs": jobs})
}

// Get handles GET /api/v1/downloader/jobs/{id}
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	id := chi.URLParam(r, "id")
	job, err := h.svc.Get(r.Context(), id)
	if err != nil {
		response.NotFound(w, "job not found")
		return
	}
	if job.UserID != userID {
		response.Forbidden(w, "not your job")
		return
	}
	if job.Status == StatusCompleted && job.AudioKey != nil {
		if url, err := h.storage.GetSignedURL(r.Context(), h.bucket, *job.AudioKey, 2*3600000000000); err == nil {
			job.StreamURL = &url
		}
	}
	response.OK(w, job)
}

// Delete handles DELETE /api/v1/downloader/jobs/{id}
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	id := chi.URLParam(r, "id")
	job, err := h.svc.Get(r.Context(), id)
	if err != nil {
		response.NotFound(w, "job not found")
		return
	}
	if job.UserID != userID {
		response.Forbidden(w, "not your job")
		return
	}
	if job.AudioKey != nil {
		_ = h.storage.DeleteFile(r.Context(), h.bucket, *job.AudioKey)
	}
	_ = h.svc.Delete(r.Context(), id, userID)
	response.NoContent(w)
}

// Stream handles GET /api/v1/downloader/jobs/{id}/stream -> 302 to signed R2 URL (Flutter friendly)
func (h *Handler) Stream(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	id := chi.URLParam(r, "id")
	job, err := h.svc.Get(r.Context(), id)
	if err != nil {
		response.NotFound(w, "job not found")
		return
	}
	if job.UserID != userID {
		response.Forbidden(w, "not your job")
		return
	}
	if job.Status != StatusCompleted || job.AudioKey == nil {
		response.BadRequest(w, "job not completed yet")
		return
	}
	url, err := h.storage.GetSignedURL(r.Context(), h.bucket, *job.AudioKey, 2*3600000000000)
	if err != nil {
		response.InternalServerError(w, "failed to generate stream url")
		return
	}
	// Flutter just_audio can follow redirect; also support ?redirect=false to return json
	if r.URL.Query().Get("redirect") == "false" {
		response.OK(w, map[string]string{"url": url})
		return
	}
	http.Redirect(w, r, url, http.StatusFound)
}

// Events handles GET /api/v1/downloader/jobs/{id}/events (SSE for live progress)
func (h *Handler) Events(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	id := chi.URLParam(r, "id")
	job, err := h.svc.Get(r.Context(), id)
	if err != nil {
		response.NotFound(w, "job not found")
		return
	}
	if job.UserID != userID {
		response.Forbidden(w, "not your job")
		return
	}

	// SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, ok := w.(http.Flusher)
	if !ok {
		response.InternalServerError(w, "streaming not supported")
		return
	}

	ch, unsub := h.svc.Subscribe(id)
	defer unsub()

	// send current state immediately
	sendSSE(w, job)
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case updated, ok := <-ch:
			if !ok {
				return
			}
			// attach stream url if completed
			if updated.Status == StatusCompleted && updated.AudioKey != nil {
				if url, err := h.storage.GetSignedURL(r.Context(), h.bucket, *updated.AudioKey, 2*3600000000000); err == nil {
					updated.StreamURL = &url
				}
			}
			sendSSE(w, &updated)
			flusher.Flush()
			if updated.Status == StatusCompleted || updated.Status == StatusFailed {
				return
			}
		}
	}
}

func sendSSE(w http.ResponseWriter, job *Job) {
	data, _ := json.Marshal(job)
	fmt.Fprintf(w, "data: %s\n\n", data)
}

// Retry handles POST /api/v1/downloader/jobs/{id}/retry
func (h *Handler) Retry(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	id := chi.URLParam(r, "id")
	job, err := h.svc.Get(r.Context(), id)
	if err != nil {
		response.NotFound(w, "job not found")
		return
	}
	if job.UserID != userID {
		response.Forbidden(w, "not your job")
		return
	}
	if job.Status != StatusFailed {
		response.BadRequest(w, "only failed jobs can be retried")
		return
	}
	// reset to queued
	_ = h.svc.repo.UpdateStatus(r.Context(), id, StatusQueued, 0)
	go h.svc.process(id)
	response.OK(w, map[string]string{"message": "retry queued"})
}
