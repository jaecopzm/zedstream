package artistclaim

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jaecopzm/zedstream/pkg/id"
)

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

func scanClaim(row interface{ Scan(dest ...any) error }) (*Claim, error) {
	c := &Claim{}
	var socialPlatform, socialPostURL, verificationCode, notes, reviewedBy, rejectionReason *string
	var reviewedAt *time.Time
	var documentKeys []string

	err := row.Scan(
		&c.ID, &c.ArtistID, &c.UserID, &c.Status, &c.Method,
		&socialPlatform, &socialPostURL, &verificationCode,
		&documentKeys, &notes,
		&reviewedBy, &reviewedAt, &rejectionReason,
		&c.CreatedAt, &c.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	c.SocialPlatform = socialPlatform
	c.SocialPostURL = socialPostURL
	c.VerificationCode = verificationCode
	c.DocumentKeys = documentKeys
	c.Notes = notes
	c.ReviewedBy = reviewedBy
	c.ReviewedAt = reviewedAt
	c.RejectionReason = rejectionReason
	return c, nil
}

func (r *Repository) Create(ctx context.Context, artistID, userID string, method VerificationMethod) (*Claim, error) {
	code := fmt.Sprintf("ZED-VRFY-%s", id.New()[:8])
	c := &Claim{}
	err := r.db.QueryRow(ctx, `
		INSERT INTO artist_claims (id, artist_id, user_id, method, verification_code)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, artist_id, user_id, status, method,
		          social_platform, social_post_url, verification_code,
		          document_keys, notes,
		          reviewed_by, reviewed_at, rejection_reason,
		          created_at, updated_at
	`, id.New(), artistID, userID, string(method), code).Scan(
		&c.ID, &c.ArtistID, &c.UserID, &c.Status, &c.Method,
		&c.SocialPlatform, &c.SocialPostURL, &c.VerificationCode,
		&c.DocumentKeys, &c.Notes,
		&c.ReviewedBy, &c.ReviewedAt, &c.RejectionReason,
		&c.CreatedAt, &c.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("create claim: %w", err)
	}
	return c, nil
}

func (r *Repository) GetByArtistID(ctx context.Context, artistID string) (*Claim, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, artist_id, user_id, status, method,
		       social_platform, social_post_url, verification_code,
		       document_keys, notes,
		       reviewed_by, reviewed_at, rejection_reason,
		       created_at, updated_at
		FROM artist_claims WHERE artist_id = $1
	`, artistID)
	return scanClaim(row)
}

func (r *Repository) GetByUserID(ctx context.Context, userID string) (*Claim, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, artist_id, user_id, status, method,
		       social_platform, social_post_url, verification_code,
		       document_keys, notes,
		       reviewed_by, reviewed_at, rejection_reason,
		       created_at, updated_at
		FROM artist_claims WHERE user_id = $1
	`, userID)
	return scanClaim(row)
}

func (r *Repository) GetByID(ctx context.Context, id string) (*Claim, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, artist_id, user_id, status, method,
		       social_platform, social_post_url, verification_code,
		       document_keys, notes,
		       reviewed_by, reviewed_at, rejection_reason,
		       created_at, updated_at
		FROM artist_claims WHERE id = $1
	`, id)
	return scanClaim(row)
}

func (r *Repository) ListByStatus(ctx context.Context, status ClaimStatus, limit, offset int) ([]*Claim, int, error) {
	var total int
	err := r.db.QueryRow(ctx, `SELECT count(*) FROM artist_claims WHERE status = $1`, string(status)).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("count claims: %w", err)
	}

	rows, err := r.db.Query(ctx, `
		SELECT id, artist_id, user_id, status, method,
		       social_platform, social_post_url, verification_code,
		       document_keys, notes,
		       reviewed_by, reviewed_at, rejection_reason,
		       created_at, updated_at
		FROM artist_claims WHERE status = $1
		ORDER BY created_at DESC LIMIT $2 OFFSET $3
	`, string(status), limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list claims: %w", err)
	}
	defer rows.Close()

	var claims []*Claim
	for rows.Next() {
		c, err := scanClaim(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scan claim: %w", err)
		}
		claims = append(claims, c)
	}
	return claims, total, nil
}

func (r *Repository) UpdateSocialVerification(ctx context.Context, claimID, platform, postURL string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE artist_claims
		SET social_platform = $2, social_post_url = $3, status = 'under_review', updated_at = NOW()
		WHERE id = $1
	`, claimID, platform, postURL)
	return err
}

func (r *Repository) UpdateDocuments(ctx context.Context, claimID string, docKeys []string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE artist_claims
		SET document_keys = $2, status = 'under_review', updated_at = NOW()
		WHERE id = $1
	`, claimID, docKeys)
	return err
}

func (r *Repository) Approve(ctx context.Context, claimID, reviewerID string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE artist_claims
		SET status = 'approved', reviewed_by = $2, reviewed_at = NOW(), updated_at = NOW()
		WHERE id = $1
	`, claimID, reviewerID)
	return err
}

func (r *Repository) Reject(ctx context.Context, claimID, reviewerID, reason string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE artist_claims
		SET status = 'rejected', reviewed_by = $2, reviewed_at = NOW(),
		    rejection_reason = $3, updated_at = NOW()
		WHERE id = $1
	`, claimID, reviewerID, reason)
	return err
}

func (r *Repository) ReassignArtist(ctx context.Context, artistID, newUserID string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE artists SET user_id = $2, updated_at = NOW() WHERE id = $1
	`, artistID, newUserID)
	return err
}

func (r *Repository) UpgradeUserRole(ctx context.Context, userID string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE users SET role = 'artist', updated_at = NOW() WHERE id = $1
	`, userID)
	return err
}

func (r *Repository) ArtistExists(ctx context.Context, artistID string) (bool, error) {
	var exists bool
	err := r.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM artists WHERE id = $1)`, artistID).Scan(&exists)
	return exists, err
}

func (r *Repository) UserHasClaim(ctx context.Context, userID string) (bool, error) {
	var exists bool
	err := r.db.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM artist_claims WHERE user_id = $1 AND status IN ('pending', 'under_review'))
	`, userID).Scan(&exists)
	return exists, err
}
