package artist

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jaecopzm/zedstream/pkg/id"
)

// Artist represents an artist profile.
type Artist struct {
	ID            string         `json:"id"`
	UserID        string         `json:"user_id"`
	Email         string         `json:"email"`
	StageName     string         `json:"stage_name"`
	Bio           *string        `json:"bio"`
	PhotoURL      *string        `json:"photo_url"`
	CoverURL      *string        `json:"cover_url"`
	Location      *string        `json:"location"`
	GenreTags     []string       `json:"genre_tags"`
	SocialLinks   map[string]any `json:"social_links"`
	Verified      bool           `json:"verified"`
	FollowerCount *int           `json:"follower_count,omitempty"`
	IsFollowed    *bool          `json:"is_followed,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

// Repository handles artist database operations.
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository creates a new artist repository.
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// Create registers a new artist for the given user.
func (r *Repository) Create(ctx context.Context, userID, stageName string) (*Artist, error) {
	// Ensure user is not already an artist
	var exists bool
	err := r.db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM artists WHERE user_id = $1)`, userID,
	).Scan(&exists)
	if err != nil {
		return nil, fmt.Errorf("check existing artist: %w", err)
	}
	if exists {
		return nil, fmt.Errorf("user is already an artist")
	}

	a := &Artist{}
	err = r.db.QueryRow(ctx, `
		INSERT INTO artists (id, user_id, stage_name)
		VALUES ($1, $2, $3)
		RETURNING id, user_id, stage_name, bio, photo_url, cover_url, location, genre_tags, verified, 0, NULL, created_at, updated_at
	`, id.New(), userID, stageName,
	).Scan(&a.ID, &a.UserID, &a.StageName, &a.Bio, &a.PhotoURL, &a.CoverURL, &a.Location, &a.GenreTags, &a.Verified, &a.FollowerCount, &a.IsFollowed, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("insert artist: %w", err)
	}

	// Upgrade user role to artist
	_, err = r.db.Exec(ctx,
		`UPDATE users SET role = 'artist', updated_at = NOW() WHERE id = $1`, userID,
	)
	if err != nil {
		return nil, fmt.Errorf("upgrade user role: %w", err)
	}

	return a, nil
}

// GetByID fetches an artist by their ID, including follower count and follow status for the given user.
func (r *Repository) GetByID(ctx context.Context, artistID string, currentUserID string) (*Artist, error) {
	a := &Artist{}
	err := r.db.QueryRow(ctx, `
		SELECT a.id, a.user_id, a.stage_name, a.bio, a.photo_url, a.cover_url, a.location, a.genre_tags, a.verified,
		       (SELECT COUNT(*) FROM follows f WHERE f.artist_id = a.id) AS follower_count,
		       CASE WHEN $2 = '' THEN NULL
		            ELSE EXISTS(SELECT 1 FROM follows f WHERE f.follower_id = $2 AND f.artist_id = a.id)
		       END AS is_followed,
		       a.created_at, a.updated_at
		FROM artists a WHERE a.id = $1
	`, artistID, currentUserID).Scan(&a.ID, &a.UserID, &a.StageName, &a.Bio, &a.PhotoURL, &a.CoverURL, &a.Location, &a.GenreTags, &a.Verified, &a.FollowerCount, &a.IsFollowed, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("get artist by id: %w", err)
	}
	return a, nil
}

// GetByUserID fetches an artist by their user ID, including follower count.
func (r *Repository) GetByUserID(ctx context.Context, userID string) (*Artist, error) {
	a := &Artist{}
	err := r.db.QueryRow(ctx, `
		SELECT a.id, a.user_id, a.stage_name, a.bio, a.photo_url, a.cover_url, a.location, a.genre_tags, a.social_links, a.verified,
		       (SELECT COUNT(*) FROM follows f WHERE f.artist_id = a.id) AS follower_count,
		       NULL AS is_followed,
		       a.created_at, a.updated_at
		FROM artists a WHERE a.user_id = $1
	`, userID).Scan(&a.ID, &a.UserID, &a.StageName, &a.Bio, &a.PhotoURL, &a.CoverURL, &a.Location, &a.GenreTags, &a.SocialLinks, &a.Verified, &a.FollowerCount, &a.IsFollowed, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("get artist by user id: %w", err)
	}
	return a, nil
}

// UpdateProfile updates an artist's profile fields.
func (r *Repository) UpdateProfile(ctx context.Context, artistID string, stageName, bio string, photoURL *string, socialLinks map[string]any, coverURL *string, location string, genreTags []string) (*Artist, error) {
	if socialLinks == nil {
		socialLinks = map[string]any{}
	}
	if genreTags == nil {
		genreTags = []string{}
	}
	a := &Artist{}
	err := r.db.QueryRow(ctx, `
		UPDATE artists
		SET stage_name   = $2,
		    bio          = $3,
		    photo_url    = COALESCE($4, photo_url),
		    social_links = $5::jsonb,
		    cover_url    = COALESCE($6, cover_url),
		    location     = $7,
		    genre_tags   = $8::text[],
		    updated_at   = NOW()
		WHERE id = $1
		RETURNING id, user_id, stage_name, bio, photo_url, cover_url, location, genre_tags, social_links, verified,
		          (SELECT COUNT(*) FROM follows f WHERE f.artist_id = artists.id),
		          NULL, created_at, updated_at
	`, artistID, stageName, bio, photoURL, socialLinks, coverURL, location, genreTags,
	).Scan(&a.ID, &a.UserID, &a.StageName, &a.Bio, &a.PhotoURL, &a.CoverURL, &a.Location, &a.GenreTags, &a.SocialLinks, &a.Verified, &a.FollowerCount, &a.IsFollowed, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("update artist profile: %w", err)
	}
	return a, nil
}

// Delete removes an artist profile by ID.
func (r *Repository) Delete(ctx context.Context, artistID string) error {
	_, err := r.db.Exec(ctx, `DELETE FROM artists WHERE id = $1`, artistID)
	if err != nil {
		return fmt.Errorf("delete artist: %w", err)
	}
	return nil
}

// ListAll returns all artists ordered by stage name.
func (r *Repository) ListAll(ctx context.Context, limit, offset int) ([]*Artist, error) {
	rows, err := r.db.Query(ctx, `
		SELECT a.id, a.user_id, u.email, a.stage_name, a.bio, a.photo_url, a.cover_url, a.location, a.genre_tags, a.verified,
		       (SELECT COUNT(*) FROM follows f WHERE f.artist_id = a.id),
		       NULL,
		       a.created_at, a.updated_at
		FROM artists a
		JOIN users u ON u.id = a.user_id
		ORDER BY a.stage_name ASC
		LIMIT $1 OFFSET $2
	`, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list all artists: %w", err)
	}
	defer rows.Close()

	var artists []*Artist
	for rows.Next() {
		a := &Artist{}
		if err := rows.Scan(&a.ID, &a.UserID, &a.Email, &a.StageName, &a.Bio, &a.PhotoURL, &a.CoverURL, &a.Location, &a.GenreTags, &a.Verified, &a.FollowerCount, &a.IsFollowed, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		artists = append(artists, a)
	}
	return artists, nil
}

// ListFeatured returns top artists by follower count for the home page.
func (r *Repository) ListFeatured(ctx context.Context, limit int) ([]*Artist, error) {
	rows, err := r.db.Query(ctx, `
		SELECT a.id, a.user_id, a.stage_name, a.bio, a.photo_url, a.cover_url, a.location, a.genre_tags, a.verified,
		       (SELECT COUNT(*) FROM follows f WHERE f.artist_id = a.id),
		       NULL,
		       a.created_at, a.updated_at
		FROM artists a
		ORDER BY (SELECT COUNT(*) FROM follows f WHERE f.artist_id = a.id) DESC, a.verified DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list featured artists: %w", err)
	}
	defer rows.Close()

	var artists []*Artist
	for rows.Next() {
		a := &Artist{}
		if err := rows.Scan(&a.ID, &a.UserID, &a.StageName, &a.Bio, &a.PhotoURL, &a.CoverURL, &a.Location, &a.GenreTags, &a.Verified, &a.FollowerCount, &a.IsFollowed, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		artists = append(artists, a)
	}
	return artists, nil
}

// Search searches artists by name using full-text search.
func (r *Repository) Search(ctx context.Context, query string, limit, offset int) ([]*Artist, error) {
	rows, err := r.db.Query(ctx, `
		SELECT a.id, a.user_id, a.stage_name, a.bio, a.photo_url, a.cover_url, a.location, a.genre_tags, a.verified,
		       (SELECT COUNT(*) FROM follows f WHERE f.artist_id = a.id),
		       NULL,
		       a.created_at, a.updated_at
		FROM artists a
		WHERE a.stage_name ILIKE '%' || $1 || '%'
		ORDER BY a.verified DESC, a.stage_name ASC
		LIMIT $2 OFFSET $3
	`, query, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("search artists: %w", err)
	}
	defer rows.Close()

	var artists []*Artist
	for rows.Next() {
		a := &Artist{}
		if err := rows.Scan(&a.ID, &a.UserID, &a.StageName, &a.Bio, &a.PhotoURL, &a.CoverURL, &a.Location, &a.GenreTags, &a.Verified, &a.FollowerCount, &a.IsFollowed, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		artists = append(artists, a)
	}
	return artists, nil
}
