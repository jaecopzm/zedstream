package downloader

import "time"

type Status string

const (
	StatusQueued      Status = "queued"
	StatusDownloading Status = "downloading"
	StatusConverting  Status = "converting"
	StatusUploading   Status = "uploading"
	StatusCompleted   Status = "completed"
	StatusFailed      Status = "failed"
)

type Job struct {
	ID          string     `json:"id"`
	UserID      string     `json:"user_id"`
	URL         string     `json:"url"`
	Title       *string    `json:"title"`
	ArtistName  *string    `json:"artist_name"`
	Quality     string     `json:"quality"`
	Status      Status     `json:"status"`
	Progress    int        `json:"progress"`
	Error       *string    `json:"error,omitempty"`
	FileSize    *int64     `json:"file_size,omitempty"`
	DurationSec *int       `json:"duration_sec,omitempty"`
	CoverURL    *string    `json:"cover_url,omitempty"`
	AudioKey    *string    `json:"audio_key,omitempty"`
	TrackID     *string    `json:"track_id,omitempty"`
	StreamURL   *string    `json:"stream_url,omitempty"` // signed url when completed
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

// CreateRequest is the Flutter/client payload.
type CreateRequest struct {
	URL     string  `json:"url"`
	Quality string  `json:"quality"` // 128,192,256,320
	Title   *string `json:"title,omitempty"`
}
