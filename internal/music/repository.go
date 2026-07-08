package music

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Track represents a music track.
type Track struct {
	ID          string     `json:"id"`
	ArtistID    string     `json:"artist_id"`
	AlbumID     *string    `json:"album_id"`
	Title       string     `json:"title"`
	DurationSec int        `json:"duration_sec"`
	GenreID     *string    `json:"genre_id"`
	CoverURL    *string    `json:"cover_url"`
	AudioKey    string     `json:"-"` // Internal R2 key, not exposed
	FileSize    int64      `json:"file_size"`
	MimeType    string     `json:"mime_type"`
	Status      string     `json:"status"`
	ScheduledAt *time.Time `json:"scheduled_at,omitempty"`
	ReleasedAt  *time.Time `json:"released_at,omitempty"`
	PlayCount   int64      `json:"play_count"`
	LikeCount   int64      `json:"like_count"`
	TrackOrder  int        `json:"track_order"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`

	// Expanded fields
	ArtistName    string          `json:"artist_name,omitempty"`
	Collaborators []Collaborator  `json:"collaborators,omitempty"`
}

// Collaborator is a featured artist on a track.
type Collaborator struct {
	ArtistID  string `json:"artist_id"`
	StageName string `json:"stage_name"`
	Role      string `json:"role"`
}

// Album represents a collection of tracks.
type Album struct {
	ID          string     `json:"id"`
	ArtistID    string     `json:"artist_id"`
	Title       string     `json:"title"`
	CoverURL    *string    `json:"cover_url"`
	Type        string     `json:"type"`
	Status      string     `json:"status"`
	ScheduledAt *time.Time `json:"scheduled_at,omitempty"`
	ReleasedAt  *time.Time `json:"released_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`

	Tracks []Track `json:"tracks,omitempty"`
}

// Genre represents a music genre.
type Genre struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// Repository handles music database operations.
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository creates a new music repository.
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// CreateTrack inserts a new track record.
func (r *Repository) CreateTrack(ctx context.Context, t *Track) (*Track, error) {
	t.ID = uuid.New().String()
	err := r.db.QueryRow(ctx, `
		INSERT INTO tracks (id, artist_id, title, duration_sec, genre_id, cover_url, audio_key, file_size, mime_type, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING id, artist_id, album_id, title, duration_sec, genre_id, cover_url, audio_key,
		          file_size, mime_type, status, scheduled_at, released_at, play_count, like_count,
		          track_order, created_at, updated_at
	`, t.ID, t.ArtistID, t.Title, t.DurationSec, t.GenreID, t.CoverURL, t.AudioKey,
		t.FileSize, t.MimeType, t.Status,
	).Scan(
		&t.ID, &t.ArtistID, &t.AlbumID, &t.Title, &t.DurationSec, &t.GenreID, &t.CoverURL,
		&t.AudioKey, &t.FileSize, &t.MimeType, &t.Status, &t.ScheduledAt, &t.ReleasedAt,
		&t.PlayCount, &t.LikeCount, &t.TrackOrder, &t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("create track: %w", err)
	}
	return t, nil
}

// GetTrackByID fetches a track by ID, including artist name.
func (r *Repository) GetTrackByID(ctx context.Context, trackID string) (*Track, error) {
	t := &Track{}
	err := r.db.QueryRow(ctx, `
		SELECT t.id, t.artist_id, t.album_id, t.title, t.duration_sec, t.genre_id, t.cover_url,
		       t.audio_key, t.file_size, t.mime_type, t.status, t.scheduled_at, t.released_at,
		       t.play_count, t.like_count, t.track_order, t.created_at, t.updated_at,
		       a.stage_name
		FROM tracks t
		JOIN artists a ON a.id = t.artist_id
		WHERE t.id = $1
	`, trackID).Scan(
		&t.ID, &t.ArtistID, &t.AlbumID, &t.Title, &t.DurationSec, &t.GenreID, &t.CoverURL,
		&t.AudioKey, &t.FileSize, &t.MimeType, &t.Status, &t.ScheduledAt, &t.ReleasedAt,
		&t.PlayCount, &t.LikeCount, &t.TrackOrder, &t.CreatedAt, &t.UpdatedAt, &t.ArtistName,
	)
	if err != nil {
		return nil, fmt.Errorf("get track by id: %w", err)
	}
	return t, nil
}

// ListTracksByArtist returns all tracks for a given artist.
func (r *Repository) ListTracksByArtist(ctx context.Context, artistID string) ([]*Track, error) {
	rows, err := r.db.Query(ctx, `
		SELECT t.id, t.artist_id, t.album_id, t.title, t.duration_sec, t.genre_id, t.cover_url,
		       t.audio_key, t.file_size, t.mime_type, t.status, t.scheduled_at, t.released_at,
		       t.play_count, t.like_count, t.track_order, t.created_at, t.updated_at,
		       a.stage_name
		FROM tracks t
		JOIN artists a ON a.id = t.artist_id
		WHERE t.artist_id = $1
		ORDER BY t.created_at DESC
	`, artistID)
	if err != nil {
		return nil, fmt.Errorf("list tracks by artist: %w", err)
	}
	defer rows.Close()

	return scanTracks(rows)
}

// ListPublishedTracks returns published tracks with pagination.
func (r *Repository) ListPublishedTracks(ctx context.Context, limit, offset int) ([]*Track, error) {
	rows, err := r.db.Query(ctx, `
		SELECT t.id, t.artist_id, t.album_id, t.title, t.duration_sec, t.genre_id, t.cover_url,
		       t.audio_key, t.file_size, t.mime_type, t.status, t.scheduled_at, t.released_at,
		       t.play_count, t.like_count, t.track_order, t.created_at, t.updated_at,
		       a.stage_name
		FROM tracks t
		JOIN artists a ON a.id = t.artist_id
		WHERE t.status = 'published'
		ORDER BY t.play_count DESC, t.created_at DESC
		LIMIT $1 OFFSET $2
	`, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list published tracks: %w", err)
	}
	defer rows.Close()

	return scanTracks(rows)
}

// SearchTracks performs full-text search on tracks.
func (r *Repository) SearchTracks(ctx context.Context, query string, limit, offset int) ([]*Track, error) {
	rows, err := r.db.Query(ctx, `
		SELECT t.id, t.artist_id, t.album_id, t.title, t.duration_sec, t.genre_id, t.cover_url,
		       t.audio_key, t.file_size, t.mime_type, t.status, t.scheduled_at, t.released_at,
		       t.play_count, t.like_count, t.track_order, t.created_at, t.updated_at,
		       a.stage_name
		FROM tracks t
		JOIN artists a ON a.id = t.artist_id
		WHERE t.status = 'published'
		  AND t.search_vector @@ plainto_tsquery('english', $1)
		ORDER BY ts_rank(t.search_vector, plainto_tsquery('english', $1)) DESC
		LIMIT $2 OFFSET $3
	`, query, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("search tracks: %w", err)
	}
	defer rows.Close()

	return scanTracks(rows)
}

// UpdateTrack updates mutable track fields.
func (r *Repository) UpdateTrack(ctx context.Context, trackID string, title string, genreID *string, status string, coverURL *string) (*Track, error) {
	t := &Track{}
	err := r.db.QueryRow(ctx, `
		UPDATE tracks
		SET title    = $2,
		    genre_id = $3,
		    status   = $4,
		    cover_url = COALESCE($5, cover_url),
		    updated_at = NOW()
		WHERE id = $1
		RETURNING id, artist_id, album_id, title, duration_sec, genre_id, cover_url, audio_key,
		          file_size, mime_type, status, scheduled_at, released_at, play_count, like_count,
		          track_order, created_at, updated_at
	`, trackID, title, genreID, status, coverURL,
	).Scan(
		&t.ID, &t.ArtistID, &t.AlbumID, &t.Title, &t.DurationSec, &t.GenreID, &t.CoverURL,
		&t.AudioKey, &t.FileSize, &t.MimeType, &t.Status, &t.ScheduledAt, &t.ReleasedAt,
		&t.PlayCount, &t.LikeCount, &t.TrackOrder, &t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("update track: %w", err)
	}
	return t, nil
}

// DeleteTrack removes a track record.
func (r *Repository) DeleteTrack(ctx context.Context, trackID, artistID string) (string, error) {
	var audioKey string
	err := r.db.QueryRow(ctx,
		`DELETE FROM tracks WHERE id = $1 AND artist_id = $2 RETURNING audio_key`,
		trackID, artistID,
	).Scan(&audioKey)
	if err != nil {
		return "", fmt.Errorf("delete track: %w", err)
	}
	return audioKey, nil
}

// ScheduleTrack sets the scheduled_at field for a track.
func (r *Repository) ScheduleTrack(ctx context.Context, trackID, artistID string, scheduledAt time.Time) error {
	_, err := r.db.Exec(ctx,
		`UPDATE tracks SET status = 'scheduled', scheduled_at = $3, updated_at = NOW()
		 WHERE id = $1 AND artist_id = $2`,
		trackID, artistID, scheduledAt,
	)
	return err
}

// PublishScheduledTracks publishes all tracks whose scheduled_at has passed.
func (r *Repository) PublishScheduledTracks(ctx context.Context) (int64, error) {
	cmd, err := r.db.Exec(ctx, `
		UPDATE tracks
		SET status = 'published', released_at = NOW(), updated_at = NOW()
		WHERE status = 'scheduled' AND scheduled_at <= NOW()
	`)
	if err != nil {
		return 0, err
	}
	return cmd.RowsAffected(), nil
}

// CreateAlbum inserts a new album.
func (r *Repository) CreateAlbum(ctx context.Context, a *Album) (*Album, error) {
	a.ID = uuid.New().String()
	err := r.db.QueryRow(ctx, `
		INSERT INTO albums (id, artist_id, title, cover_url, type, status)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, artist_id, title, cover_url, type, status, scheduled_at, released_at, created_at, updated_at
	`, a.ID, a.ArtistID, a.Title, a.CoverURL, a.Type, a.Status,
	).Scan(&a.ID, &a.ArtistID, &a.Title, &a.CoverURL, &a.Type, &a.Status, &a.ScheduledAt, &a.ReleasedAt, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("create album: %w", err)
	}
	return a, nil
}

// GetAlbumByID fetches an album with its tracks.
func (r *Repository) GetAlbumByID(ctx context.Context, albumID string) (*Album, error) {
	a := &Album{}
	err := r.db.QueryRow(ctx, `
		SELECT id, artist_id, title, cover_url, type, status, scheduled_at, released_at, created_at, updated_at
		FROM albums WHERE id = $1
	`, albumID).Scan(&a.ID, &a.ArtistID, &a.Title, &a.CoverURL, &a.Type, &a.Status, &a.ScheduledAt, &a.ReleasedAt, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("get album: %w", err)
	}

	rows, err := r.db.Query(ctx, `
		SELECT t.id, t.artist_id, t.album_id, t.title, t.duration_sec, t.genre_id, t.cover_url,
		       t.audio_key, t.file_size, t.mime_type, t.status, t.scheduled_at, t.released_at,
		       t.play_count, t.like_count, t.track_order, t.created_at, t.updated_at,
		       ar.stage_name
		FROM tracks t
		JOIN artists ar ON ar.id = t.artist_id
		WHERE t.album_id = $1
		ORDER BY t.track_order ASC
	`, albumID)
	if err != nil {
		return nil, fmt.Errorf("get album tracks: %w", err)
	}
	defer rows.Close()

	tracks, err := scanTracks(rows)
	if err != nil {
		return nil, err
	}
	for _, t := range tracks {
		a.Tracks = append(a.Tracks, *t)
	}
	return a, nil
}

// AddTrackToAlbum assigns a track to an album.
func (r *Repository) AddTrackToAlbum(ctx context.Context, albumID, trackID, artistID string, order int) error {
	_, err := r.db.Exec(ctx, `
		UPDATE tracks SET album_id = $1, track_order = $2, updated_at = NOW()
		WHERE id = $3 AND artist_id = $4
	`, albumID, order, trackID, artistID)
	return err
}

// RemoveTrackFromAlbum disassociates a track from an album.
func (r *Repository) RemoveTrackFromAlbum(ctx context.Context, albumID, trackID, artistID string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE tracks SET album_id = NULL, updated_at = NOW()
		WHERE id = $1 AND album_id = $2 AND artist_id = $3
	`, trackID, albumID, artistID)
	return err
}

// ListGenres returns all genres.
func (r *Repository) ListGenres(ctx context.Context) ([]*Genre, error) {
	rows, err := r.db.Query(ctx, `SELECT id, name, slug FROM genres ORDER BY name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var genres []*Genre
	for rows.Next() {
		g := &Genre{}
		if err := rows.Scan(&g.ID, &g.Name, &g.Slug); err != nil {
			return nil, err
		}
		genres = append(genres, g)
	}
	return genres, nil
}

// GetTracksByGenre returns published tracks in a genre.
func (r *Repository) GetTracksByGenre(ctx context.Context, genreID string, limit, offset int) ([]*Track, error) {
	rows, err := r.db.Query(ctx, `
		SELECT t.id, t.artist_id, t.album_id, t.title, t.duration_sec, t.genre_id, t.cover_url,
		       t.audio_key, t.file_size, t.mime_type, t.status, t.scheduled_at, t.released_at,
		       t.play_count, t.like_count, t.track_order, t.created_at, t.updated_at,
		       a.stage_name
		FROM tracks t
		JOIN artists a ON a.id = t.artist_id
		WHERE t.genre_id = $1 AND t.status = 'published'
		ORDER BY t.play_count DESC
		LIMIT $2 OFFSET $3
	`, genreID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTracks(rows)
}

// AddCollaborator adds a featured artist to a track.
func (r *Repository) AddCollaborator(ctx context.Context, trackID, artistID, role string) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO track_collaborators (track_id, artist_id, role)
		VALUES ($1, $2, $3)
		ON CONFLICT (track_id, artist_id) DO UPDATE SET role = EXCLUDED.role
	`, trackID, artistID, role)
	return err
}

// RemoveCollaborator removes a featured artist from a track.
func (r *Repository) RemoveCollaborator(ctx context.Context, trackID, collaboratorArtistID string) error {
	_, err := r.db.Exec(ctx,
		`DELETE FROM track_collaborators WHERE track_id = $1 AND artist_id = $2`,
		trackID, collaboratorArtistID,
	)
	return err
}

// GetCollaborators returns all collaborators for a track.
func (r *Repository) GetCollaborators(ctx context.Context, trackID string) ([]Collaborator, error) {
	rows, err := r.db.Query(ctx, `
		SELECT tc.artist_id, a.stage_name, tc.role
		FROM track_collaborators tc
		JOIN artists a ON a.id = tc.artist_id
		WHERE tc.track_id = $1
	`, trackID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var collabs []Collaborator
	for rows.Next() {
		c := Collaborator{}
		if err := rows.Scan(&c.ArtistID, &c.StageName, &c.Role); err != nil {
			return nil, err
		}
		collabs = append(collabs, c)
	}
	return collabs, nil
}

// scanTracks is a helper to scan multiple track rows.
func scanTracks(rows interface {
	Next() bool
	Scan(...any) error
}) ([]*Track, error) {
	var tracks []*Track
	for rows.Next() {
		t := &Track{}
		if err := rows.Scan(
			&t.ID, &t.ArtistID, &t.AlbumID, &t.Title, &t.DurationSec, &t.GenreID, &t.CoverURL,
			&t.AudioKey, &t.FileSize, &t.MimeType, &t.Status, &t.ScheduledAt, &t.ReleasedAt,
			&t.PlayCount, &t.LikeCount, &t.TrackOrder, &t.CreatedAt, &t.UpdatedAt, &t.ArtistName,
		); err != nil {
			return nil, err
		}
		tracks = append(tracks, t)
	}
	return tracks, nil
}
