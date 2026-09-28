package streaming

import (
	"context"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jaecopzm/zedstream/pkg/middleware"
	"github.com/jaecopzm/zedstream/pkg/response"
	"github.com/jaecopzm/zedstream/pkg/storage"
)

const signedURLExpiry = 2 * time.Hour

// sanitizeFilename strips characters that are illegal in file names or
// would break the Content-Disposition header value.
func sanitizeFilename(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "track"
	}
	if len(s) > 120 {
		s = s[:120]
	}
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '"', '\\', '/', ':', '*', '?', '<', '>', '|':
			b.WriteRune('-')
		default:
			if r < 32 || r == 127 {
				continue
			}
			b.WriteRune(r)
		}
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		return "track"
	}
	return out
}

// downloadURL returns a signed download URL for a copy of the track with
// its cover art embedded. Copies are cached in the audio bucket under a
// content-derived key, so each track/cover combination is built once.
// Any failure falls back to the artless original — downloads never 500.
func (h *Handler) downloadURL(ctx context.Context, trackID, audioKey, title, artistName string, coverURL *string) (string, error) {
	ext := strings.ToLower(path.Ext(audioKey))
	if ext == "" {
		ext = ".mp3"
	}
	filename := sanitizeFilename(artistName+" - "+title) + ext

	cover := ""
	if coverURL != nil {
		cover = strings.TrimSpace(*coverURL)
	}
	if cover == "" {
		return h.storage.GetSignedDownloadURL(ctx, h.audioBucket, audioKey, signedURLExpiry, filename)
	}

	sum := sha1.Sum([]byte(audioKey + "|" + cover))
	key := fmt.Sprintf("embeds/%s/%x%s", trackID, sum, ext)
	if !h.storage.ObjectExists(ctx, h.audioBucket, key) {
		if err := h.buildEmbeddedCopy(ctx, audioKey, cover, key, ext); err != nil {
			return h.storage.GetSignedDownloadURL(ctx, h.audioBucket, audioKey, signedURLExpiry, filename)
		}
	}
	return h.storage.GetSignedDownloadURL(ctx, h.audioBucket, key, signedURLExpiry, filename)
}

// buildEmbeddedCopy downloads the audio + cover, muxes the cover in with
// ffmpeg (stream copy, no re-encode) and stores the result at destKey.
func (h *Handler) buildEmbeddedCopy(ctx context.Context, audioKey, coverURL, destKey, ext string) error {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	tmpDir, err := os.MkdirTemp("", "zedstream-embed-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	audioSrc, err := h.storage.GetSignedURL(ctx, h.audioBucket, audioKey, 5*time.Minute)
	if err != nil {
		return err
	}
	audioPath := tmpDir + string(os.PathSeparator) + "audio" + ext
	if err := fetchToFile(ctx, audioSrc, audioPath, ""); err != nil {
		return err
	}
	coverPath := tmpDir + string(os.PathSeparator) + "cover.img"
	if err := fetchToFile(ctx, coverURL, coverPath, "image/"); err != nil {
		return err
	}

	outPath := tmpDir + string(os.PathSeparator) + "out" + ext
	var args []string
	if ext == ".mp3" {
		args = []string{"-y", "-i", audioPath, "-i", coverPath,
			"-map", "0:a", "-map", "1:v", "-c", "copy",
			"-id3v2_version", "3",
			"-metadata:s:v", `title="Album cover"`,
			"-metadata:s:v", `comment="Cover (front)"`,
			outPath}
	} else {
		args = []string{"-y", "-i", audioPath, "-i", coverPath,
			"-map", "0", "-map", "1", "-c", "copy",
			"-disposition:v:1", "attached_pic",
			outPath}
	}
	if out, err := exec.CommandContext(ctx, "ffmpeg", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg embed: %w (%s)", err, strings.TrimSpace(string(out)))
	}

	out, err := os.Open(outPath)
	if err != nil {
		return err
	}
	defer out.Close()
	return h.storage.UploadFile(ctx, h.audioBucket, destKey, audioContentType(ext), out)
}

// fetchToFile downloads url to dest, requiring the response content type
// to start with wantPrefix when non-empty.
func fetchToFile(ctx context.Context, url, dest, wantPrefix string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch %s: status %d", url, res.StatusCode)
	}
	if wantPrefix != "" && !strings.HasPrefix(res.Header.Get("Content-Type"), wantPrefix) {
		return fmt.Errorf("fetch %s: unexpected content type %q", url, res.Header.Get("Content-Type"))
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(f, io.LimitReader(res.Body, 60<<20)); err != nil {
		return err
	}
	return nil
}

func audioContentType(ext string) string {
	switch strings.ToLower(ext) {
	case ".mp3":
		return "audio/mpeg"
	case ".m4a", ".mp4":
		return "audio/mp4"
	case ".ogg", ".oga", ".opus":
		return "audio/ogg"
	case ".wav":
		return "audio/wav"
	case ".flac":
		return "audio/flac"
	default:
		return "application/octet-stream"
	}
}

// Handler handles music streaming endpoints.
type Handler struct {
	db          *pgxpool.Pool
	storage     *storage.Client
	audioBucket string
}

// NewHandler creates a new streaming handler.
func NewHandler(db *pgxpool.Pool, store *storage.Client, audioBucket string) *Handler {
	return &Handler{db: db, storage: store, audioBucket: audioBucket}
}

// StreamTrack returns a short-lived signed URL for streaming a track.
//
// @Summary     Get streaming URL for a track
// @Tags        streaming
// @Security    BearerAuth
// @Param       id path string true "Track ID"
// @Router      /tracks/{id}/stream [get]
func (h *Handler) StreamTrack(w http.ResponseWriter, r *http.Request) {
	trackID := chi.URLParam(r, "id")
	userID := middleware.UserIDFromContext(r.Context())

	// Fetch track audio key and verify it's published
	var audioKey, status, title, artistName string
	var coverURL *string
	err := h.db.QueryRow(r.Context(),
		`SELECT t.audio_key, t.status, t.title, COALESCE(a.stage_name, 'Unknown Artist'), t.cover_url
		 FROM tracks t LEFT JOIN artists a ON a.id = t.artist_id WHERE t.id = $1`, trackID,
	).Scan(&audioKey, &status, &title, &artistName, &coverURL)
	if err != nil {
		response.NotFound(w, "track not found")
		return
	}

	if status != "published" {
		response.NotFound(w, "track is not available")
		return
	}

	// Download mode (?download=1) returns a signed URL for a copy of the
	// track with its cover art embedded, so local players show artwork.
	// The plain stream URL cannot be used for downloads: browsers fetch it
	// cross-origin (CORS) and ignore the anchor download attribute, so it
	// would play inline instead of saving.
	if r.URL.Query().Get("download") == "1" {
		downloadURL, err := h.downloadURL(r.Context(), trackID, audioKey, title, artistName, coverURL)
		if err != nil {
			response.InternalServerError(w, "failed to generate download URL")
			return
		}
		response.OK(w, map[string]any{
			"url":        downloadURL,
			"expires_in": int(signedURLExpiry.Seconds()),
		})
		return
	}

	// Generate signed URL
	signedURL, err := h.storage.GetSignedURL(r.Context(), h.audioBucket, audioKey, signedURLExpiry)
	if err != nil {
		response.InternalServerError(w, "failed to generate stream URL")
		return
	}

	// Record play event (non-blocking with timeout)
	go h.recordPlayEvent(userID, trackID)

	response.OK(w, map[string]any{
		"url":        signedURL,
		"expires_in": int(signedURLExpiry.Seconds()),
	})
}

// RecordPlayProgress records how long a user listened (called by client on pause/stop).
//
// @Summary     Record play progress
// @Tags        streaming
// @Security    BearerAuth
// @Param       id path string true "Track ID"
// @Router      /tracks/{id}/play [post]
func (h *Handler) RecordPlayProgress(w http.ResponseWriter, r *http.Request) {
	trackID := chi.URLParam(r, "id")
	userID := middleware.UserIDFromContext(r.Context())

	var body struct {
		DurationListened int `json:"duration_listened"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		response.BadRequest(w, "invalid request body")
		return
	}

	go h.updatePlayDuration(userID, trackID, body.DurationListened)
	response.NoContent(w)
}

func (h *Handler) recordPlayEvent(userID, trackID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, _ = h.db.Exec(ctx,
		`INSERT INTO play_events (user_id, track_id) VALUES ($1, $2)`,
		userID, trackID,
	)
	_, _ = h.db.Exec(ctx,
		`UPDATE tracks SET play_count = play_count + 1 WHERE id = $1`, trackID,
	)
}

func (h *Handler) updatePlayDuration(userID, trackID string, duration int) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, _ = h.db.Exec(ctx, `
		UPDATE play_events
		SET duration_listened = $3
		WHERE id = (
		    SELECT id FROM play_events
		    WHERE user_id = $1 AND track_id = $2
		    ORDER BY played_at DESC LIMIT 1
		)
	`, userID, trackID, duration)
}
