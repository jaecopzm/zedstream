package artist

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Artist represents an artist profile.
type Artist struct {
	ID        string     `json:"id"`
	UserID    string     `json:"user_id"`
	StageName string     `json:"stage_name"`
	Bio       *string    `json:"bio"`
	PhotoURL  *string    `json:"photo_url"`
	Verified  bool       `json:"verified"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
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
		RETURNING id, user_id, stage_name, bio, photo_url, verified, created_at, updated_at
	`, uuid.New().String(), userID, stageName,
	).Scan(&a.ID, &a.UserID, &a.StageName, &a.Bio, &a.PhotoURL, &a.Verified, &a.CreatedAt, &a.UpdatedAt)
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

// GetByID fetches an artist by their ID.
func (r *Repository) GetByID(ctx context.Context, artistID string) (*Artist, error) {
	a := &Artist{}
	err := r.db.QueryRow(ctx, `
		SELECT id, user_id, stage_name, bio, photo_url, verified, created_at, updated_at
		FROM artists WHERE id = $1
	`, artistID).Scan(&a.ID, &a.UserID, &a.StageName, &a.Bio, &a.PhotoURL, &a.Verified, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("get artist by id: %w", err)
	}
	return a, nil
}

// GetByUserID fetches an artist by their user ID.
func (r *Repository) GetByUserID(ctx context.Context, userID string) (*Artist, error) {
	a := &Artist{}
	err := r.db.QueryRow(ctx, `
		SELECT id, user_id, stage_name, bio, photo_url, verified, created_at, updated_at
		FROM artists WHERE user_id = $1
	`, userID).Scan(&a.ID, &a.UserID, &a.StageName, &a.Bio, &a.PhotoURL, &a.Verified, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("get artist by user id: %w", err)
	}
	return a, nil
}

// UpdateProfile updates an artist's bio and photo URL.
func (r *Repository) UpdateProfile(ctx context.Context, artistID string, stageName, bio string, photoURL *string) (*Artist, error) {
	a := &Artist{}
	err := r.db.QueryRow(ctx, `
		UPDATE artists
		SET stage_name = $2,
		    bio        = $3,
		    photo_url  = COALESCE($4, photo_url),
		    updated_at = NOW()
		WHERE id = $1
		RETURNING id, user_id, stage_name, bio, photo_url, verified, created_at, updated_at
	`, artistID, stageName, bio, photoURL,
	).Scan(&a.ID, &a.UserID, &a.StageName, &a.Bio, &a.PhotoURL, &a.Verified, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("update artist profile: %w", err)
	}
	return a, nil
}

// Search searches artists by name using full-text search.
func (r *Repository) Search(ctx context.Context, query string, limit, offset int) ([]*Artist, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, user_id, stage_name, bio, photo_url, verified, created_at, updated_at
		FROM artists
		WHERE stage_name ILIKE '%' || $1 || '%'
		ORDER BY verified DESC, stage_name ASC
		LIMIT $2 OFFSET $3
	`, query, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("search artists: %w", err)
	}
	defer rows.Close()

	var artists []*Artist
	for rows.Next() {
		a := &Artist{}
		if err := rows.Scan(&a.ID, &a.UserID, &a.StageName, &a.Bio, &a.PhotoURL, &a.Verified, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		artists = append(artists, a)
	}
	return artists, nil
}
