package downloader

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

func (r *Repository) Create(ctx context.Context, j *Job) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO downloader_jobs (id, user_id, url, title, artist_name, quality, status, progress)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		j.ID, j.UserID, j.URL, j.Title, j.ArtistName, j.Quality, j.Status, j.Progress,
	)
	return err
}

func (r *Repository) Get(ctx context.Context, id string) (*Job, error) {
	j := &Job{}
	err := r.db.QueryRow(ctx, `
		SELECT id, user_id, url, title, artist_name, quality, status, progress,
		       error, file_size, duration_sec, cover_url, audio_key, track_id,
		       created_at, updated_at, completed_at
		FROM downloader_jobs WHERE id=$1`, id,
	).Scan(&j.ID, &j.UserID, &j.URL, &j.Title, &j.ArtistName, &j.Quality, &j.Status, &j.Progress,
		&j.Error, &j.FileSize, &j.DurationSec, &j.CoverURL, &j.AudioKey, &j.TrackID,
		&j.CreatedAt, &j.UpdatedAt, &j.CompletedAt)
	if err != nil {
		return nil, err
	}
	return j, nil
}

func (r *Repository) ListByUser(ctx context.Context, userID string, limit, offset int) ([]*Job, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, user_id, url, title, artist_name, quality, status, progress,
		       error, file_size, duration_sec, cover_url, audio_key, track_id,
		       created_at, updated_at, completed_at
		FROM downloader_jobs
		WHERE user_id=$1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Job
	for rows.Next() {
		j := &Job{}
		if err := rows.Scan(&j.ID, &j.UserID, &j.URL, &j.Title, &j.ArtistName, &j.Quality, &j.Status, &j.Progress,
			&j.Error, &j.FileSize, &j.DurationSec, &j.CoverURL, &j.AudioKey, &j.TrackID,
			&j.CreatedAt, &j.UpdatedAt, &j.CompletedAt); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func (r *Repository) UpdateStatus(ctx context.Context, id string, status Status, progress int) error {
	_, err := r.db.Exec(ctx, `UPDATE downloader_jobs SET status=$1, progress=$2, updated_at=NOW() WHERE id=$3`, status, progress, id)
	return err
}

func (r *Repository) UpdateProgress(ctx context.Context, id string, progress int, status Status) error {
	_, err := r.db.Exec(ctx, `UPDATE downloader_jobs SET progress=$1, status=$2, updated_at=NOW() WHERE id=$3`, progress, status, id)
	return err
}

func (r *Repository) MarkCompleted(ctx context.Context, id string, audioKey string, fileSize int64, durationSec int, title, artistName *string, coverURL *string, trackID *string) error {
	now := time.Now()
	_, err := r.db.Exec(ctx, `
		UPDATE downloader_jobs
		SET status='completed', progress=100, audio_key=$1, file_size=$2, duration_sec=$3,
		    title=COALESCE($4,title), artist_name=COALESCE($5,artist_name), cover_url=$6, track_id=$7,
		    completed_at=$8, updated_at=NOW()
		WHERE id=$9`,
		audioKey, fileSize, durationSec, title, artistName, coverURL, trackID, now, id)
	return err
}

func (r *Repository) MarkFailed(ctx context.Context, id string, errMsg string) error {
	_, err := r.db.Exec(ctx, `UPDATE downloader_jobs SET status='failed', error=$1, updated_at=NOW() WHERE id=$2`, errMsg, id)
	return err
}

func (r *Repository) Delete(ctx context.Context, id, userID string) error {
	_, err := r.db.Exec(ctx, `DELETE FROM downloader_jobs WHERE id=$1 AND user_id=$2`, id, userID)
	return err
}
