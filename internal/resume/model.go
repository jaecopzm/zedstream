package resume

import "time"

// ResumePoint is where a user left off in a track.
type ResumePoint struct {
	UserID      string    `json:"-"`
	TrackID     string    `json:"track_id"`
	PositionSec int       `json:"position_sec"`
	DurationSec int       `json:"duration_sec"`
	UpdatedAt   time.Time `json:"updated_at"`

	// Joined track metadata for list responses.
	Title      string  `json:"title,omitempty"`
	ArtistID   string  `json:"artist_id,omitempty"`
	ArtistName string  `json:"artist_name,omitempty"`
	CoverURL   *string `json:"cover_url,omitempty"`
}
