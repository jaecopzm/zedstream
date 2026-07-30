package artistclaim

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jaecopzm/zedstream/pkg/storage"
)

type Service struct {
	repo    *Repository
	storage *storage.Client
	bucket  string
}

func NewService(repo *Repository, store *storage.Client, docBucket string) *Service {
	return &Service{
		repo:    repo,
		storage: store,
		bucket:  docBucket,
	}
}

var (
	ErrAlreadyClaimed      = errors.New("artist already has a pending claim")
	ErrUserHasPendingClaim = errors.New("user already has a pending claim")
	ErrArtistNotFound      = errors.New("artist not found")
	ErrClaimNotFound       = errors.New("claim not found")
	ErrInvalidMethod       = errors.New("invalid verification method")
	ErrNotPending          = errors.New("claim is not in pending status")
	ErrSocialVerifyFailed  = errors.New("social media verification failed")
)

func (s *Service) Initiate(ctx context.Context, artistID, userID string, method VerificationMethod) (*Claim, error) {
	if method != MethodSocialMedia && method != MethodManualReview {
		return nil, ErrInvalidMethod
	}

	exists, err := s.repo.ArtistExists(ctx, artistID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrArtistNotFound
	}

	existing, _ := s.repo.GetByArtistID(ctx, artistID)
	if existing != nil {
		return nil, ErrAlreadyClaimed
	}

	hasPending, err := s.repo.UserHasClaim(ctx, userID)
	if err != nil {
		return nil, err
	}
	if hasPending {
		return nil, ErrUserHasPendingClaim
	}

	return s.repo.Create(ctx, artistID, userID, method)
}

func (s *Service) SubmitSocialVerification(ctx context.Context, claimID, platform, postURL string) error {
	claim, err := s.repo.GetByID(ctx, claimID)
	if err != nil {
		return ErrClaimNotFound
	}
	if claim.Status != ClaimStatusPending {
		return ErrNotPending
	}
	if claim.Method != MethodSocialMedia {
		return errors.New("claim was not initiated with social media method")
	}

	if claim.VerificationCode == nil {
		return errors.New("no verification code found on claim")
	}

	verified, err := verifySocialPost(platform, postURL, *claim.VerificationCode)
	if err != nil {
		slog.Warn("social verification fetch failed", "claim_id", claimID, "error", err)
		return ErrSocialVerifyFailed
	}
	if !verified {
		return ErrSocialVerifyFailed
	}

	return s.repo.UpdateSocialVerification(ctx, claimID, platform, postURL)
}

type DocumentFile struct {
	Reader      io.Reader
	Filename    string
	ContentType string
}

func (s *Service) SubmitManualReview(ctx context.Context, claimID string, files []DocumentFile) error {
	claim, err := s.repo.GetByID(ctx, claimID)
	if err != nil {
		return ErrClaimNotFound
	}
	if claim.Status != ClaimStatusPending {
		return ErrNotPending
	}
	if claim.Method != MethodManualReview {
		return errors.New("claim was not initiated with manual review method")
	}

	var keys []string
	for _, f := range files {
		key := fmt.Sprintf("claims/%s/%s", claimID, f.Filename)
		if err := s.storage.UploadFile(ctx, s.bucket, key, f.ContentType, f.Reader); err != nil {
			return fmt.Errorf("upload document: %w", err)
		}
		keys = append(keys, key)
	}

	return s.repo.UpdateDocuments(ctx, claimID, keys)
}

type ApproveResult struct {
	Claim    *Claim
	UserID   string
	Email    string
	ArtistID string
}

func (s *Service) Approve(ctx context.Context, claimID, reviewerID string) (*ApproveResult, error) {
	claim, err := s.repo.GetByID(ctx, claimID)
	if err != nil {
		return nil, ErrClaimNotFound
	}
	if claim.Status != ClaimStatusUnderReview {
		return nil, ErrNotPending
	}

	if err := s.repo.Approve(ctx, claimID, reviewerID); err != nil {
		return nil, fmt.Errorf("approve claim: %w", err)
	}

	if err := s.repo.ReassignArtist(ctx, claim.ArtistID, claim.UserID); err != nil {
		slog.Error("failed to reassign artist", "claim_id", claimID, "artist_id", claim.ArtistID, "error", err)
		return nil, fmt.Errorf("reassign artist: %w", err)
	}

	if err := s.repo.UpgradeUserRole(ctx, claim.UserID); err != nil {
		slog.Error("failed to upgrade user role", "claim_id", claimID, "user_id", claim.UserID, "error", err)
		return nil, fmt.Errorf("upgrade user role: %w", err)
	}

	// Fetch claiming user's email for JWT re-issuance
	var email string
	_ = s.repo.db.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, claim.UserID).Scan(&email)

	updated, _ := s.repo.GetByID(ctx, claimID)
	return &ApproveResult{
		Claim:    updated,
		UserID:   claim.UserID,
		Email:    email,
		ArtistID: claim.ArtistID,
	}, nil
}

func (s *Service) Reject(ctx context.Context, claimID, reviewerID, reason string) error {
	claim, err := s.repo.GetByID(ctx, claimID)
	if err != nil {
		return ErrClaimNotFound
	}
	if claim.Status != ClaimStatusUnderReview && claim.Status != ClaimStatusPending {
		return ErrNotPending
	}
	return s.repo.Reject(ctx, claimID, reviewerID, reason)
}

func (s *Service) GetClaim(ctx context.Context, artistID string) (*Claim, error) {
	return s.repo.GetByArtistID(ctx, artistID)
}

func (s *Service) ListPending(ctx context.Context, limit, offset int) ([]*Claim, int, error) {
	return s.repo.ListByStatus(ctx, ClaimStatusUnderReview, limit, offset)
}

func verifySocialPost(platform, postURL, expectedCode string) (bool, error) {
	client := &http.Client{Timeout: 10 * time.Second}

	req, err := http.NewRequest("GET", postURL, nil)
	if err != nil {
		return false, err
	}
		req.Header.Set("User-Agent", "ZedBeatz-Verification/1.0 (Artist Claim Bot)")

	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("post returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return false, err
	}

	return strings.Contains(string(body), expectedCode), nil
}
