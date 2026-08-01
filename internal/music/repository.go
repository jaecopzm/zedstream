package music

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jaecopzm/zedstream/pkg/id"
)

// Track represents a music track.
type Track struct {
	ID          string     `json:"id"`
	ArtistID    string     `json:"artist_id"`
	AlbumID     *string    `json:"album_id"`
	Title       string     `json:"title"`
	DurationSec int        `json:"duration_sec"`
	GenreID     *string    `json:"genre_id"`
	GenreName   *string    `json:"genre_name,omitempty"`
	GenreSlug   *string    `json:"genre_slug,omitempty"`
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

	HlsPlaylistKey *string `json:"hls_playlist_key,omitempty"`
	HlsStatus      string  `json:"hls_status"`

	Section string `json:"section"`

	// Expanded fields
	ArtistName    string          `json:"artist_name,omitempty"`
	AlbumName     string          `json:"album_name,omitempty"`
	Collaborators []Collaborator  `json:"collaborators,omitempty"`

	// Description
	Description *string `json:"description,omitempty"`
}

// Collaborator is a featured artist on a track.
type Collaborator struct {
	ArtistID  string  `json:"artist_id"`
	StageName string  `json:"stage_name"`
	Role      string  `json:"role"`
	PhotoURL  *string `json:"photo_url,omitempty"`
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
	ArtistName  string     `json:"artist_name,omitempty"`

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
	t.ID = id.New()
	err := r.db.QueryRow(ctx, `
		INSERT INTO tracks (id, artist_id, album_id, title, duration_sec, genre_id, cover_url, audio_key, file_size, mime_type, status, hls_status, section, description)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, 'pending', $12, $13)
		RETURNING id, artist_id, album_id, title, duration_sec, genre_id, cover_url, audio_key,
		          file_size, mime_type, status, scheduled_at, released_at, play_count, like_count,
		          track_order, created_at, updated_at, hls_playlist_key, hls_status, section,
		          description
	`, t.ID, t.ArtistID, t.AlbumID, t.Title, t.DurationSec, t.GenreID, t.CoverURL, t.AudioKey,
		t.FileSize, t.MimeType, t.Status, t.Section, t.Description,
	).Scan(
		&t.ID, &t.ArtistID, &t.AlbumID, &t.Title, &t.DurationSec, &t.GenreID, &t.CoverURL,
		&t.AudioKey, &t.FileSize, &t.MimeType, &t.Status, &t.ScheduledAt, &t.ReleasedAt,
		&t.PlayCount, &t.LikeCount, &t.TrackOrder, &t.CreatedAt, &t.UpdatedAt, &t.HlsPlaylistKey, &t.HlsStatus, &t.Section,
		&t.Description,
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
		       t.play_count, t.like_count, t.track_order, t.created_at, t.updated_at, t.hls_playlist_key, t.hls_status,
		       t.section,
		       a.stage_name,
		       g.name, g.slug,
		       COALESCE(al.title, ''),
		       t.description
		FROM tracks t
		JOIN artists a ON a.id = t.artist_id
		LEFT JOIN genres g ON g.id = t.genre_id
		LEFT JOIN albums al ON al.id = t.album_id
		WHERE t.id = $1
	`, trackID).Scan(
		&t.ID, &t.ArtistID, &t.AlbumID, &t.Title, &t.DurationSec, &t.GenreID, &t.CoverURL,
		&t.AudioKey, &t.FileSize, &t.MimeType, &t.Status, &t.ScheduledAt, &t.ReleasedAt,
		&t.PlayCount, &t.LikeCount, &t.TrackOrder, &t.CreatedAt, &t.UpdatedAt, &t.HlsPlaylistKey, &t.HlsStatus, &t.Section,
		&t.ArtistName,
		&t.GenreName, &t.GenreSlug,
		&t.AlbumName,
		&t.Description,
	)
	if err != nil {
		return nil, fmt.Errorf("get track by id: %w", err)
	}
	// Load collaborators
	if err := r.loadTrackCollaborators(ctx, []*Track{t}); err != nil {
		return nil, fmt.Errorf("load collaborators: %w", err)
	}
	return t, nil
}

// ListTracksByArtist returns all tracks for a given artist.
func (r *Repository) ListTracksByArtist(ctx context.Context, artistID string) ([]*Track, error) {
	rows, err := r.db.Query(ctx, `
		SELECT t.id, t.artist_id, t.album_id, t.title, t.duration_sec, t.genre_id, t.cover_url,
		       t.audio_key, t.file_size, t.mime_type, t.status, t.scheduled_at, t.released_at,
		       t.play_count, t.like_count, t.track_order, t.created_at, t.updated_at, t.hls_playlist_key, t.hls_status,
		       t.section,
		       t.description,
		       a.stage_name,
		       g.name, g.slug,
		       COALESCE(al.title, '')
		FROM tracks t
		JOIN artists a ON a.id = t.artist_id
		LEFT JOIN genres g ON g.id = t.genre_id
		LEFT JOIN albums al ON al.id = t.album_id
		WHERE t.artist_id = $1
		ORDER BY t.created_at DESC
	`, artistID)
	if err != nil {
		return nil, fmt.Errorf("list tracks by artist: %w", err)
	}
	defer rows.Close()

	tracks, err := scanTracks(rows)
	if err != nil {
		return nil, err
	}
	if err := r.loadTrackCollaborators(ctx, tracks); err != nil {
		return nil, fmt.Errorf("load collaborators: %w", err)
	}
	return tracks, nil
}

// ListTracksByCollaborator returns published tracks where the given artist is featured as a collaborator.
func (r *Repository) ListTracksByCollaborator(ctx context.Context, artistID string, limit int) ([]*Track, error) {
	rows, err := r.db.Query(ctx, `
		SELECT t.id, t.artist_id, t.album_id, t.title, t.duration_sec, t.genre_id, t.cover_url,
		       t.audio_key, t.file_size, t.mime_type, t.status, t.scheduled_at, t.released_at,
		       t.play_count, t.like_count, t.track_order, t.created_at, t.updated_at, t.hls_playlist_key, t.hls_status,
		       t.section,
		       t.description,
		       a.stage_name,
		       g.name, g.slug,
		       COALESCE(al.title, '')
		FROM track_collaborators tc
		JOIN tracks t ON t.id = tc.track_id
		JOIN artists a ON a.id = t.artist_id
		LEFT JOIN genres g ON g.id = t.genre_id
		LEFT JOIN albums al ON al.id = t.album_id
		WHERE tc.artist_id = $1 AND t.status = 'published' AND t.artist_id != $1
		ORDER BY t.play_count DESC, t.created_at DESC
		LIMIT $2
	`, artistID, limit)
	if err != nil {
		return nil, fmt.Errorf("list tracks by collaborator: %w", err)
	}
	defer rows.Close()

	tracks, err := scanTracks(rows)
	if err != nil {
		return nil, err
	}
	if err := r.loadTrackCollaborators(ctx, tracks); err != nil {
		return nil, fmt.Errorf("load collaborators: %w", err)
	}
	return tracks, nil
}

// ListAllTracks returns all tracks (any status) with pagination. For admin use.
func (r *Repository) ListAllTracks(ctx context.Context, limit, offset int) ([]*Track, error) {
	rows, err := r.db.Query(ctx, `
		SELECT t.id, t.artist_id, t.album_id, t.title, t.duration_sec, t.genre_id, t.cover_url,
		       t.audio_key, t.file_size, t.mime_type, t.status, t.scheduled_at, t.released_at,
		       t.play_count, t.like_count, t.track_order, t.created_at, t.updated_at, t.hls_playlist_key, t.hls_status,
		       t.section,
		       t.description,
		       a.stage_name,
		       g.name, g.slug,
		       COALESCE(al.title, '')
		FROM tracks t
		JOIN artists a ON a.id = t.artist_id
		LEFT JOIN genres g ON g.id = t.genre_id
		LEFT JOIN albums al ON al.id = t.album_id
		ORDER BY t.created_at DESC
		LIMIT $1 OFFSET $2
	`, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list all tracks: %w", err)
	}
	defer rows.Close()
	return scanTracks(rows)
}

// ListPublishedTracks returns published tracks with pagination.
func (r *Repository) ListPublishedTracks(ctx context.Context, limit, offset int, section string) ([]*Track, error) {
	var rows pgx.Rows
	var err error

	if section != "" {
		rows, err = r.db.Query(ctx, `
			SELECT t.id, t.artist_id, t.album_id, t.title, t.duration_sec, t.genre_id, t.cover_url,
			       t.audio_key, t.file_size, t.mime_type, t.status, t.scheduled_at, t.released_at,
			       t.play_count, t.like_count, t.track_order, t.created_at, t.updated_at, t.hls_playlist_key, t.hls_status,
			       t.section,
			       t.description,
			       a.stage_name,
		       g.name, g.slug,
		       COALESCE(al.title, '')
			FROM tracks t
			JOIN artists a ON a.id = t.artist_id
			LEFT JOIN genres g ON g.id = t.genre_id
			LEFT JOIN albums al ON al.id = t.album_id
			WHERE t.status = 'published' AND t.section = $1
			ORDER BY t.play_count DESC, t.created_at DESC
			LIMIT $2 OFFSET $3
		`, section, limit, offset)
	} else {
		rows, err = r.db.Query(ctx, `
			SELECT t.id, t.artist_id, t.album_id, t.title, t.duration_sec, t.genre_id, t.cover_url,
			       t.audio_key, t.file_size, t.mime_type, t.status, t.scheduled_at, t.released_at,
			       t.play_count, t.like_count, t.track_order, t.created_at, t.updated_at, t.hls_playlist_key, t.hls_status,
			       t.section,
			       t.description,
			       a.stage_name,
			       g.name, g.slug,
			       COALESCE(al.title, '')
			FROM tracks t
			JOIN artists a ON a.id = t.artist_id
			LEFT JOIN genres g ON g.id = t.genre_id
			LEFT JOIN albums al ON al.id = t.album_id
			WHERE t.status = 'published'
			ORDER BY t.play_count DESC, t.created_at DESC
			LIMIT $1 OFFSET $2
		`, limit, offset)
	}
	if err != nil {
		return nil, fmt.Errorf("list published tracks: %w", err)
	}
	defer rows.Close()

	tracks, err := scanTracks(rows)
	if err != nil {
		return nil, err
	}
	if err := r.loadTrackCollaborators(ctx, tracks); err != nil {
		return nil, fmt.Errorf("load collaborators: %w", err)
	}
	return tracks, nil
}

// loadTrackCollaborators batch-loads collaborators for a slice of tracks.
func (r *Repository) loadTrackCollaborators(ctx context.Context, tracks []*Track) error {
	if len(tracks) == 0 {
		return nil
	}
	ids := make([]string, len(tracks))
	for i, t := range tracks {
		ids[i] = t.ID
	}
	rows, err := r.db.Query(ctx, `
		SELECT tc.track_id, tc.artist_id, a.stage_name, tc.role, a.photo_url
		FROM track_collaborators tc
		JOIN artists a ON a.id = tc.artist_id
		WHERE tc.track_id = ANY($1)
	`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()

	collabMap := make(map[string][]Collaborator)
	for rows.Next() {
		var trackID, artistID, stageName, role string
		var photoURL *string
		if err := rows.Scan(&trackID, &artistID, &stageName, &role, &photoURL); err != nil {
			return err
		}
		collabMap[trackID] = append(collabMap[trackID], Collaborator{
			ArtistID:  artistID,
			StageName: stageName,
			PhotoURL:  photoURL,
			Role:      role,
		})
	}
	for _, t := range tracks {
		t.Collaborators = collabMap[t.ID]
	}
	return nil
}

// SearchTracks performs full-text search on tracks.
func (r *Repository) SearchTracks(ctx context.Context, query string, limit, offset int) ([]*Track, error) {
	rows, err := r.db.Query(ctx, `
		SELECT t.id, t.artist_id, t.album_id, t.title, t.duration_sec, t.genre_id, t.cover_url,
		       t.audio_key, t.file_size, t.mime_type, t.status, t.scheduled_at, t.released_at,
		       t.play_count, t.like_count, t.track_order, t.created_at, t.updated_at, t.hls_playlist_key, t.hls_status,
		       t.section,
		       t.description,
		       a.stage_name,
		       g.name, g.slug,
		       COALESCE(al.title, '')
		FROM tracks t
		JOIN artists a ON a.id = t.artist_id
		LEFT JOIN genres g ON g.id = t.genre_id
		LEFT JOIN albums al ON al.id = t.album_id
		WHERE t.status = 'published'
		  AND (
		    t.search_vector @@ plainto_tsquery('english', $1)
		    OR a.stage_name ILIKE '%' || $1 || '%'
		    OR al.title ILIKE '%' || $1 || '%'
		  )
		ORDER BY
		  CASE WHEN a.stage_name ILIKE '%' || $1 || '%' OR al.title ILIKE '%' || $1 || '%'
		       THEN 0 ELSE 1 END,
		  ts_rank(t.search_vector, plainto_tsquery('english', $1)) DESC,
		  t.play_count DESC
		LIMIT $2 OFFSET $3
	`, query, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("search tracks: %w", err)
	}
	defer rows.Close()

	tracks, err := scanTracks(rows)
	if err != nil {
		return nil, err
	}
	if err := r.loadTrackCollaborators(ctx, tracks); err != nil {
		return nil, fmt.Errorf("load collaborators: %w", err)
	}
	return tracks, nil
}

// UpdateTrack updates mutable track fields.
func (r *Repository) UpdateTrack(ctx context.Context, trackID string, title string, genreID *string, status string, coverURL *string, description *string) (*Track, error) {
	t := &Track{}
	err := r.db.QueryRow(ctx, `
		UPDATE tracks
		SET title       = $2,
		    genre_id    = $3,
		    status      = $4,
		    cover_url   = COALESCE($5, cover_url),
		    description = COALESCE($6, description),
		    updated_at  = NOW()
		WHERE id = $1
		RETURNING id, artist_id, album_id, title, duration_sec, genre_id, cover_url, audio_key,
		          file_size, mime_type, status, scheduled_at, released_at, play_count, like_count,
		          track_order, created_at, updated_at, hls_playlist_key, hls_status, section,
		          description
	`, trackID, title, genreID, status, coverURL, description,
	).Scan(
		&t.ID, &t.ArtistID, &t.AlbumID, &t.Title, &t.DurationSec, &t.GenreID, &t.CoverURL,
		&t.AudioKey, &t.FileSize, &t.MimeType, &t.Status, &t.ScheduledAt, &t.ReleasedAt,
		&t.PlayCount, &t.LikeCount, &t.TrackOrder, &t.CreatedAt, &t.UpdatedAt, &t.HlsPlaylistKey, &t.HlsStatus, &t.Section,
		&t.Description,
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
	a.ID = id.New()
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
		       t.play_count, t.like_count, t.track_order, t.created_at, t.updated_at, t.hls_playlist_key, t.hls_status,
		       t.section,
		       t.description,
		       ar.stage_name,
		       g.name, g.slug,
		       COALESCE(al.title, '')
		FROM tracks t
		JOIN artists ar ON ar.id = t.artist_id
		LEFT JOIN genres g ON g.id = t.genre_id
		LEFT JOIN albums al ON al.id = t.album_id
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
	if err := r.loadTrackCollaborators(ctx, tracks); err != nil {
		return nil, fmt.Errorf("load collaborators: %w", err)
	}
	for _, t := range tracks {
		a.Tracks = append(a.Tracks, *t)
	}
	return a, nil
}

// ListAlbumsByArtist returns all albums for a given artist.
func (r *Repository) ListAlbumsByArtist(ctx context.Context, artistID string) ([]*Album, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, artist_id, title, cover_url, type, status, scheduled_at, released_at, created_at, updated_at
		FROM albums WHERE artist_id = $1
		ORDER BY created_at DESC
	`, artistID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var albums []*Album
	for rows.Next() {
		a := &Album{}
		if err := rows.Scan(&a.ID, &a.ArtistID, &a.Title, &a.CoverURL, &a.Type, &a.Status, &a.ScheduledAt, &a.ReleasedAt, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		albums = append(albums, a)
	}
	return albums, nil
}

// ListAllAlbums returns all albums with pagination.
func (r *Repository) ListAllAlbums(ctx context.Context, limit, offset int) ([]*Album, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, artist_id, title, cover_url, type, status, scheduled_at, released_at, created_at, updated_at
		FROM albums
		ORDER BY created_at DESC
		LIMIT $1 OFFSET $2
	`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var albums []*Album
	for rows.Next() {
		a := &Album{}
		if err := rows.Scan(&a.ID, &a.ArtistID, &a.Title, &a.CoverURL, &a.Type, &a.Status, &a.ScheduledAt, &a.ReleasedAt, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		albums = append(albums, a)
	}
	return albums, nil
}

// UpdateAlbum modifies an album's metadata.
func (r *Repository) UpdateAlbum(ctx context.Context, albumID string, title string, coverURL *string, albumType string, status string) (*Album, error) {
	a := &Album{}
	err := r.db.QueryRow(ctx, `
		UPDATE albums
		SET title = $2, cover_url = COALESCE($3, cover_url), type = $4, status = $5, updated_at = NOW()
		WHERE id = $1
		RETURNING id, artist_id, title, cover_url, type, status, scheduled_at, released_at, created_at, updated_at
	`, albumID, title, coverURL, albumType, status).
		Scan(&a.ID, &a.ArtistID, &a.Title, &a.CoverURL, &a.Type, &a.Status, &a.ScheduledAt, &a.ReleasedAt, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("update album: %w", err)
	}
	return a, nil
}

// DeleteAlbum removes an album by ID.
func (r *Repository) DeleteAlbum(ctx context.Context, albumID string) error {
	_, err := r.db.Exec(ctx, `DELETE FROM albums WHERE id = $1`, albumID)
	if err != nil {
		return fmt.Errorf("delete album: %w", err)
	}
	return nil
}

// ListFeaturedAlbums returns recently published albums with artist names, for the home page.
func (r *Repository) ListFeaturedAlbums(ctx context.Context, limit, offset int) ([]*Album, error) {
	rows, err := r.db.Query(ctx, `
		SELECT al.id, al.artist_id, al.title, al.cover_url, al.type, al.status, al.scheduled_at, al.released_at, al.created_at, al.updated_at,
		       a.stage_name AS artist_name
		FROM albums al
		JOIN artists a ON a.id = al.artist_id
		WHERE al.status = 'published'
		ORDER BY al.released_at DESC NULLS LAST, al.created_at DESC
		LIMIT $1 OFFSET $2
	`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var albums []*Album
	for rows.Next() {
		a := &Album{}
		if err := rows.Scan(&a.ID, &a.ArtistID, &a.Title, &a.CoverURL, &a.Type, &a.Status, &a.ScheduledAt, &a.ReleasedAt, &a.CreatedAt, &a.UpdatedAt, &a.ArtistName); err != nil {
			return nil, err
		}
		albums = append(albums, a)
	}
	return albums, nil
}

// SearchAlbums searches published albums by title, artist name, or collaborator.
func (r *Repository) SearchAlbums(ctx context.Context, query string, limit, offset int) ([]*Album, error) {
	rows, err := r.db.Query(ctx, `
		SELECT al.id, al.artist_id, al.title, al.cover_url, al.type, al.status, al.scheduled_at, al.released_at, al.created_at, al.updated_at,
		       a.stage_name AS artist_name
		FROM albums al
		JOIN artists a ON a.id = al.artist_id
		WHERE al.status = 'published'
		  AND (
		    al.title ILIKE '%' || $1 || '%'
		    OR a.stage_name ILIKE '%' || $1 || '%'
		    OR EXISTS (
		      SELECT 1 FROM album_collaborators ac
		      JOIN artists ca ON ca.id = ac.artist_id
		      WHERE ac.album_id = al.id AND ca.stage_name ILIKE '%' || $1 || '%'
		    )
		  )
		ORDER BY
		  CASE WHEN a.stage_name ILIKE '%' || $1 || '%' THEN 0 ELSE 1 END,
		  al.released_at DESC NULLS LAST,
		  al.created_at DESC
		LIMIT $2 OFFSET $3
	`, query, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var albums []*Album
	for rows.Next() {
		a := &Album{}
		if err := rows.Scan(&a.ID, &a.ArtistID, &a.Title, &a.CoverURL, &a.Type, &a.Status, &a.ScheduledAt, &a.ReleasedAt, &a.CreatedAt, &a.UpdatedAt, &a.ArtistName); err != nil {
			return nil, err
		}
		albums = append(albums, a)
	}
	return albums, nil
}
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
		       t.play_count, t.like_count, t.track_order, t.created_at, t.updated_at, t.hls_playlist_key, t.hls_status,
		       t.section,
		       t.description,
		       a.stage_name,
		       g.name, g.slug,
		       COALESCE(al.title, '')
		FROM tracks t
		JOIN artists a ON a.id = t.artist_id
		LEFT JOIN genres g ON g.id = t.genre_id
		LEFT JOIN albums al ON al.id = t.album_id
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

// ResolveGenre looks up a genre by id or slug and returns its id.
func (r *Repository) ResolveGenre(ctx context.Context, idOrSlug string) *string {
	if idOrSlug == "" {
		return nil
	}
	var foundID string
	err := r.db.QueryRow(ctx,
		`SELECT id FROM genres WHERE id = $1 OR slug = $1`, idOrSlug,
	).Scan(&foundID)
	if err != nil {
		return nil
	}
	return &foundID
}

// FindOrCreateArtist finds an artist by stage_name or creates one (with user).
func (r *Repository) FindOrCreateArtist(ctx context.Context, name string) (string, error) {
	var artistID string
	err := r.db.QueryRow(ctx, `SELECT id FROM artists WHERE stage_name = $1`, name).Scan(&artistID)
	if err == nil {
		return artistID, nil
	}

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	slug := strings.ToLower(strings.ReplaceAll(name, " ", "-"))
	slug = regexp.MustCompile(`[^a-z0-9-]`).ReplaceAllString(slug, "")
	email := slug + "@imported.zedbeatz"
	providerID := "import:" + slug

	var userID string
	err = tx.QueryRow(ctx, `
		INSERT INTO users (id, email, name, role, provider, provider_id)
		VALUES ($1, $2, $3, 'artist', 'google', $4)
		RETURNING id
	`, id.New(), email, name, providerID).Scan(&userID)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") {
			_ = tx.QueryRow(ctx, `SELECT id FROM users WHERE email = $1`, email).Scan(&userID)
		} else {
			return "", fmt.Errorf("create user: %w", err)
		}
	}

	err = tx.QueryRow(ctx, `
		INSERT INTO artists (id, user_id, stage_name, verified)
		VALUES ($1, $2, $3, true)
		RETURNING id
	`, id.New(), userID, name).Scan(&artistID)
	if err != nil {
		return "", fmt.Errorf("create artist: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit artist: %w", err)
	}
	return artistID, nil
}

// DetectDuration reads duration (seconds) from an audio file using ffprobe.
func DetectDuration(filePath string) int {
	out, err := exec.Command("ffprobe",
		"-v", "quiet",
		"-of", "csv=p=0",
		"-show_entries", "format=duration",
		filePath,
	).Output()
	if err != nil {
		return 0
	}
	sec, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		return 0
	}
	return int(sec)
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
			&t.PlayCount, &t.LikeCount, &t.TrackOrder, &t.CreatedAt, &t.UpdatedAt, &t.HlsPlaylistKey, &t.HlsStatus, &t.Section,
			&t.Description,
			&t.ArtistName,
			&t.GenreName, &t.GenreSlug,
			&t.AlbumName,
		); err != nil {
			return nil, err
		}
		tracks = append(tracks, t)
	}
	return tracks, nil
}

// SitemapEntry is a minimal record for sitemap generation.
type SitemapEntry struct {
	ID        string    `json:"id"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ListSitemapURLs returns lightweight id/timestamp lists for published tracks, albums, and artists.
// Kept intentionally minimal and indexed so crawler sitemap generation stays fast.
func (r *Repository) ListSitemapURLs(ctx context.Context) (tracks []SitemapEntry, albums []SitemapEntry, artists []SitemapEntry, err error) {
	tracks, err = r.querySitemap(ctx, `SELECT id, updated_at FROM tracks WHERE status = 'published' ORDER BY updated_at DESC`)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("sitemap tracks: %w", err)
	}
	albums, err = r.querySitemap(ctx, `SELECT id, updated_at FROM albums WHERE status = 'published' ORDER BY updated_at DESC`)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("sitemap albums: %w", err)
	}
	artists, err = r.querySitemap(ctx, `SELECT id, updated_at FROM artists ORDER BY updated_at DESC`)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("sitemap artists: %w", err)
	}
	return tracks, albums, artists, nil
}

func (r *Repository) querySitemap(ctx context.Context, query string) ([]SitemapEntry, error) {
	rows, err := r.db.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SitemapEntry
	for rows.Next() {
		var e SitemapEntry
		if err := rows.Scan(&e.ID, &e.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
