package artistclaim

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jaecopzm/zedstream/internal/auth"
	"github.com/jaecopzm/zedstream/pkg/middleware"
	"github.com/jaecopzm/zedstream/pkg/response"
)

type Handler struct {
	svc     *Service
	authSvc *auth.Service
}

func NewHandler(svc *Service, authSvc *auth.Service) *Handler {
	return &Handler{svc: svc, authSvc: authSvc}
}

// InitiateClaim starts the verification process for an artist profile.
//
//	@Summary     Initiate artist profile claim
//	@Tags        claims
//	@Security    BearerAuth
//	@Accept      json
//	@Produce     json
//	@Router      /artists/{id}/claim [post]
func (h *Handler) InitiateClaim(w http.ResponseWriter, r *http.Request) {
	artistID := chi.URLParam(r, "id")
	userID := middleware.UserIDFromContext(r.Context())

	var body InitiateClaimRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		response.BadRequest(w, "invalid request body")
		return
	}

	claim, err := h.svc.Initiate(r.Context(), artistID, userID, body.Method)
	if err != nil {
		switch {
		case errors.Is(err, ErrAlreadyClaimed):
			response.Conflict(w, "this artist profile already has a pending claim")
		case errors.Is(err, ErrUserHasPendingClaim):
			response.Conflict(w, "you already have a pending claim")
		case errors.Is(err, ErrArtistNotFound):
			response.NotFound(w, "artist not found")
		case errors.Is(err, ErrInvalidMethod):
			response.BadRequest(w, "invalid verification method; use 'social_media' or 'manual_review'")
		default:
			response.InternalServerError(w, "failed to initiate claim")
		}
		return
	}

	response.Created(w, claim)
}

// SubmitVerification submits evidence for verification (social post URL or documents).
//
//	@Summary     Submit verification evidence
//	@Tags        claims
//	@Security    BearerAuth
//	@Router      /claims/{id}/verify [post]
func (h *Handler) SubmitVerification(w http.ResponseWriter, r *http.Request) {
	claimID := chi.URLParam(r, "id")

	contentType := r.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "multipart/form-data") {
		h.submitManualReview(w, r, claimID)
	} else {
		h.submitSocialVerify(w, r, claimID)
	}
}

func (h *Handler) submitSocialVerify(w http.ResponseWriter, r *http.Request, claimID string) {
	var body SocialVerifyRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		response.BadRequest(w, "invalid request body")
		return
	}

	if body.Platform == "" || body.PostURL == "" {
		response.BadRequest(w, "platform and post_url are required")
		return
	}

	if err := h.svc.SubmitSocialVerification(r.Context(), claimID, body.Platform, body.PostURL); err != nil {
		switch {
		case errors.Is(err, ErrClaimNotFound):
			response.NotFound(w, "claim not found")
		case errors.Is(err, ErrNotPending):
			response.Conflict(w, "claim is not in pending status")
		case errors.Is(err, ErrSocialVerifyFailed):
			response.BadRequest(w, "could not verify the social post; ensure it contains the verification code and is publicly accessible")
		default:
			response.InternalServerError(w, "failed to submit verification")
		}
		return
	}

	response.OK(w, map[string]string{"status": "under_review", "message": "verification submitted for review"})
}

func (h *Handler) submitManualReview(w http.ResponseWriter, r *http.Request, claimID string) {
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		response.BadRequest(w, "failed to parse form data")
		return
	}

	files := r.MultipartForm.File["documents"]
	if len(files) == 0 {
		response.BadRequest(w, "at least one document file is required")
		return
	}

	var docs []DocumentFile
	for _, fh := range files {
		f, err := fh.Open()
		if err != nil {
			response.InternalServerError(w, "failed to read uploaded file")
			return
		}
		defer f.Close()
		docs = append(docs, DocumentFile{
			Reader:      f,
			Filename:    fh.Filename,
			ContentType: fh.Header.Get("Content-Type"),
		})
	}

	if err := h.svc.SubmitManualReview(r.Context(), claimID, docs); err != nil {
		switch {
		case errors.Is(err, ErrClaimNotFound):
			response.NotFound(w, "claim not found")
		case errors.Is(err, ErrNotPending):
			response.Conflict(w, "claim is not in pending status")
		default:
			response.InternalServerError(w, "failed to submit documents")
		}
		return
	}

	response.OK(w, map[string]string{"status": "under_review", "message": "documents submitted for review"})
}

// GetClaimStatus returns the current status of a claim for an artist.
//
//	@Summary     Get artist claim status
//	@Tags        claims
//	@Produce     json
//	@Router      /artists/{id}/claim [get]
func (h *Handler) GetClaimStatus(w http.ResponseWriter, r *http.Request) {
	artistID := chi.URLParam(r, "id")

	claim, err := h.svc.GetClaim(r.Context(), artistID)
	if err != nil || claim == nil {
		response.OK(w, map[string]any{"claimed": false})
		return
	}

	response.OK(w, map[string]any{
		"claimed": true,
		"claim":   claim,
	})
}

// ListClaims returns pending claims (admin only).
//
//	@Summary     List claims pending review
//	@Tags        admin
//	@Security    BearerAuth
//	@Produce     json
//	@Router      /admin/claims [get]
func (h *Handler) ListClaims(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 100 {
		limit = 20
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}

	claims, total, err := h.svc.ListPending(r.Context(), limit, offset)
	if err != nil {
		response.InternalServerError(w, "failed to list claims")
		return
	}

	if claims == nil {
		claims = []*Claim{}
	}

	response.OK(w, map[string]any{
		"claims": claims,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

// ReviewClaim approves or rejects a claim (admin only).
//
//	@Summary     Review an artist claim
//	@Tags        admin
//	@Security    BearerAuth
//	@Accept      json
//	@Produce     json
//	@Router      /admin/claims/{id}/review [post]
func (h *Handler) ReviewClaim(w http.ResponseWriter, r *http.Request) {
	claimID := chi.URLParam(r, "id")
	reviewerID := middleware.UserIDFromContext(r.Context())

	var body ReviewRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		response.BadRequest(w, "invalid request body")
		return
	}

	switch body.Action {
	case "approve":
		result, err := h.svc.Approve(r.Context(), claimID, reviewerID)
		if err != nil {
			switch {
			case errors.Is(err, ErrClaimNotFound):
				response.NotFound(w, "claim not found")
			case errors.Is(err, ErrNotPending):
				response.Conflict(w, "claim must be under review before approving")
			default:
				response.InternalServerError(w, "failed to approve claim")
			}
			return
		}

		tokenPair, err := h.authSvc.IssueTokenPair(r.Context(), result.UserID, result.Email, auth.RoleArtist)
		if err != nil {
			response.InternalServerError(w, "claim approved but failed to issue new token")
			return
		}

		response.OK(w, map[string]any{
			"status":      "approved",
			"message":     "artist claim approved",
			"tokens":      tokenPair,
			"artist_id":   result.ArtistID,
		})

	case "reject":
		reason := ""
		if body.Reason != nil {
			reason = *body.Reason
		}
		if reason == "" {
			response.BadRequest(w, "rejection reason is required")
			return
		}

		if err := h.svc.Reject(r.Context(), claimID, reviewerID, reason); err != nil {
			switch {
			case errors.Is(err, ErrClaimNotFound):
				response.NotFound(w, "claim not found")
			case errors.Is(err, ErrNotPending):
				response.Conflict(w, "claim cannot be rejected in its current state")
			default:
				response.InternalServerError(w, "failed to reject claim")
			}
			return
		}

		response.OK(w, map[string]string{
			"status":  "rejected",
			"message": "claim rejected",
		})

	default:
		response.BadRequest(w, "action must be 'approve' or 'reject'")
	}
}
