package artistclaim

import "time"

type ClaimStatus string

const (
	ClaimStatusPending    ClaimStatus = "pending"
	ClaimStatusUnderReview ClaimStatus = "under_review"
	ClaimStatusApproved   ClaimStatus = "approved"
	ClaimStatusRejected   ClaimStatus = "rejected"
)

type VerificationMethod string

const (
	MethodSocialMedia VerificationMethod = "social_media"
	MethodManualReview VerificationMethod = "manual_review"
)

type Claim struct {
	ID               string              `json:"id"`
	ArtistID         string              `json:"artist_id"`
	UserID           string              `json:"user_id"`
	Status           ClaimStatus         `json:"status"`
	Method           VerificationMethod  `json:"method"`
	SocialPlatform   *string             `json:"social_platform,omitempty"`
	SocialPostURL    *string             `json:"social_post_url,omitempty"`
	VerificationCode *string             `json:"verification_code,omitempty"`
	DocumentKeys     []string            `json:"document_keys,omitempty"`
	Notes            *string             `json:"notes,omitempty"`
	ReviewedBy       *string             `json:"reviewed_by,omitempty"`
	ReviewedAt       *time.Time          `json:"reviewed_at,omitempty"`
	RejectionReason  *string             `json:"rejection_reason,omitempty"`
	CreatedAt        time.Time           `json:"created_at"`
	UpdatedAt        time.Time           `json:"updated_at"`
}

type InitiateClaimRequest struct {
	Method VerificationMethod `json:"method"`
}

type SocialVerifyRequest struct {
	Platform string `json:"platform"`
	PostURL  string `json:"post_url"`
}

type ReviewRequest struct {
	Action  string  `json:"action"` // "approve" or "reject"
	Reason  *string `json:"reason,omitempty"`
}
