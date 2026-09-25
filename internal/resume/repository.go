package resume

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// Upsert saves a resume point. Positions under 5s or past 95% of the
// track count as "not really started / finished" and clear the point.
func (r *Repository) Upsert(ctx context.Context, userID, trackID string, positionSec, durationSec int) error {
	if positionSec < 5 || (durationSec > 0 && float64(positionSec)/float64(durationSec) > 0.95) {
		_, err := r.db.Exec(ctx,
			`DELETE FROM resume_points WHERE user_id=$1 AND track_id=$2`,
			userID, trackID,
		)
		return err
	}
	_, err := r.db.Exec(ctx, `
		INSERT INTO resume_points (user_id, track_id, position_sec, duration_sec, updated_at)
		VALUES ($1,$2,$3,$4,NOW())
		ON CONFLICT (user_id, track_id)
		DO UPDATE SET position_sec=EXCLUDED.position_sec,
		              duration_sec=EXCLUDED.duration_sec,
		              updated_at=NOW()`,
		userID, trackID, positionSec, durationSec,
	)
	return err
}

// List returns the most recently touched resume points with track metadata.
func (r *Repository) List(ctx context.Context, userID string, limit int) ([]ResumePoint, error) {
	rows, err := r.db.Query(ctx, `
		SELECT r.track_id, r.position_sec, r.duration_sec, r.updated_at,
		       t.title, t.artist_id, a.stage_name, t.cover_url
		FROM resume_points r
		JOIN tracks t ON t.id = r.track_id
		LEFT JOIN artists a ON a.id = t.artist_id
		WHERE r.user_id = $1
		ORDER BY r.updated_at DESC
		LIMIT $2`,
		userID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []ResumePoint{}
	for rows.Next() {
		var p ResumePoint
		p.UserID = userID
		if err := rows.Scan(
			&p.TrackID, &p.PositionSec, &p.DurationSec, &p.UpdatedAt,
			&p.Title, &p.ArtistID, &p.ArtistName, &p.CoverURL,
		); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Delete removes a single resume point.
func (r *Repository) Delete(ctx context.Context, userID, trackID string) error {
	_, err := r.db.Exec(ctx,
		`DELETE FROM resume_points WHERE user_id=$1 AND track_id=$2`,
		userID, trackID,
	)
	return err
}
