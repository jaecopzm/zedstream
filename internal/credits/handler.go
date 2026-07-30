package credits

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jaecopzm/zedstream/internal/payments"
	"github.com/jaecopzm/zedstream/pkg/middleware"
	"github.com/jaecopzm/zedstream/pkg/response"
)

// Handler exposes credit HTTP endpoints.
type Handler struct {
	repo        *Repository
	payClient   *payments.Client
	frontendURL string
}

// NewHandler creates a new credits handler.
func NewHandler(repo *Repository, payClient *payments.Client, frontendURL string) *Handler {
	return &Handler{
		repo:        repo,
		payClient:   payClient,
		frontendURL: frontendURL,
	}
}

// GetMyBalance returns the authenticated artist's credit balance and lifetime stats.
//
// @Summary     Get my credit balance
// @Tags        credits
// @Security    BearerAuth
// @Router      /artists/me/credits [get]
func (h *Handler) GetMyBalance(w http.ResponseWriter, r *http.Request) {
	artistID := middleware.ArtistIDFromContext(r.Context())
	bal, err := h.repo.GetBalance(r.Context(), artistID)
	if err != nil {
		response.InternalServerError(w, "failed to fetch credit balance")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	txs, err := h.repo.ListTransactions(r.Context(), artistID, limit)
	if err != nil {
		response.InternalServerError(w, "failed to fetch credit history")
		return
	}
	if txs == nil {
		txs = []Transaction{}
	}
	response.OK(w, map[string]any{
		"balance":      bal,
		"transactions": txs,
		"price_per_credit_zmw": PricePerCreditZMW,
	})
}

// ListMyTransactions returns the artist's credit ledger.
//
// @Summary     List my credit transactions
// @Tags        credits
// @Security    BearerAuth
// @Param       limit query int false "max 200, default 50"
// @Router      /artists/me/credits/transactions [get]
func (h *Handler) ListMyTransactions(w http.ResponseWriter, r *http.Request) {
	artistID := middleware.ArtistIDFromContext(r.Context())
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	txs, err := h.repo.ListTransactions(r.Context(), artistID, limit)
	if err != nil {
		response.InternalServerError(w, "failed to fetch credit history")
		return
	}
	if txs == nil {
		txs = []Transaction{}
	}
	response.OK(w, map[string]any{"transactions": txs})
}

// InitiatePurchase creates a MoneyUnify USSD payment request for buying credits.
//
// @Summary     Initiate credit purchase
// @Tags        credits
// @Security    BearerAuth
// @Router      /artists/me/credits/purchase [post]
func (h *Handler) InitiatePurchase(w http.ResponseWriter, r *http.Request) {
	artistID := middleware.ArtistIDFromContext(r.Context())

	var body struct {
		Amount      int    `json:"amount"`
		PhoneNumber string `json:"phone_number"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Amount <= 0 {
		response.BadRequest(w, "amount (positive integer) is required")
		return
	}
	if body.PhoneNumber == "" {
		response.BadRequest(w, "phone_number is required for mobile money payment")
		return
	}

	totalZMW := CreditPrice(body.Amount)
	txRef := fmt.Sprintf("ZS-CR-%s-%d-%d", artistID[:8], body.Amount, time.Now().UnixMilli())

	transactionID, err := h.payClient.RequestPayment(r.Context(), &payments.PaymentRequest{
		Phone:      body.PhoneNumber,
		Amount:     totalZMW,
		TxRef:      txRef,
		WebhookURL: h.frontendURL + "/api/v1/webhooks/moneyunify",
	})
	if err != nil {
		slog.Error("moneyunify request payment failed", "artist_id", artistID, "error", err)
		response.InternalServerError(w, "failed to initiate payment")
		return
	}

	payment, err := h.repo.CreatePaymentIntent(r.Context(), artistID, body.Amount, totalZMW, txRef, transactionID)
	if err != nil {
		slog.Error("create payment intent failed", "artist_id", artistID, "error", err)
		response.InternalServerError(w, "failed to record payment")
		return
	}

	response.OK(w, map[string]any{
		"status":            "pending",
		"message":           "Check your phone for the USSD payment prompt and enter your PIN to approve.",
		"payment_id":        payment.ID,
		"tx_ref":            txRef,
		"transaction_id":    transactionID,
		"amount":            body.Amount,
		"total_zmw":         totalZMW,
	})
}

// AdminGrant lets an admin grant credits to any artist.
//
// @Summary     Grant credits to an artist (admin)
// @Tags        admin, credits
// @Security    BearerAuth
// @Router      /admin/credits/grant [post]
func (h *Handler) AdminGrant(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ArtistID string `json:"artist_id"`
		Amount   int    `json:"amount"`
		Reason   string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ArtistID == "" || body.Amount <= 0 {
		response.BadRequest(w, "artist_id and positive amount are required")
		return
	}
	desc := strings.TrimSpace(body.Reason)
	if desc == "" {
		desc = "admin grant"
	}
	bal, err := h.repo.GrantCredits(r.Context(), body.ArtistID, body.Amount, TypeAdminGrant, desc)
	if err != nil {
		response.InternalServerError(w, "failed to grant credits")
		return
	}
	response.OK(w, map[string]any{"balance": bal})
}

// AdminRevoke lets an admin remove credits from an artist.
//
// @Summary     Revoke credits from an artist (admin)
// @Tags        admin, credits
// @Security    BearerAuth
// @Router      /admin/credits/revoke [post]
func (h *Handler) AdminRevoke(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ArtistID string `json:"artist_id"`
		Amount   int    `json:"amount"`
		Reason   string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ArtistID == "" || body.Amount <= 0 {
		response.BadRequest(w, "artist_id and positive amount are required")
		return
	}
	desc := strings.TrimSpace(body.Reason)
	if desc == "" {
		desc = "admin revoke"
	}
	bal, err := h.repo.RevokeCredits(r.Context(), body.ArtistID, body.Amount, desc)
	if err != nil {
		response.BadRequest(w, "failed to revoke credits (insufficient balance?)")
		return
	}
	response.OK(w, map[string]any{"balance": bal})
}

// ArtistBalanceFromURL is a helper for fetching a specific artist's balance (admin view).
//
// @Summary     Get an artist's credit balance (admin)
// @Tags        admin, credits
// @Security    BearerAuth
// @Param       id path string true "Artist ID"
// @Router      /admin/credits/{id} [get]
func (h *Handler) ArtistBalanceFromURL(w http.ResponseWriter, r *http.Request) {
	artistID := chi.URLParam(r, "id")
	bal, err := h.repo.GetBalance(r.Context(), artistID)
	if err != nil {
		response.InternalServerError(w, "failed to fetch balance")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	txs, _ := h.repo.ListTransactions(r.Context(), artistID, limit)
	if txs == nil {
		txs = []Transaction{}
	}
	response.OK(w, map[string]any{"balance": bal, "transactions": txs})
}

// VerifyPayment is called from the frontend after the user approves the USSD prompt.
func (h *Handler) VerifyPayment(w http.ResponseWriter, r *http.Request) {
	artistID := middleware.ArtistIDFromContext(r.Context())

	var body struct {
		TxRef         string `json:"tx_ref"`
		TransactionID string `json:"transaction_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.TxRef == "" || body.TransactionID == "" {
		response.BadRequest(w, "tx_ref and transaction_id are required")
		return
	}

	payment, err := h.repo.GetPaymentByTxRef(r.Context(), body.TxRef)
	if err != nil {
		response.BadRequest(w, "payment not found")
		return
	}
	if payment.ArtistID != artistID {
		response.Forbidden(w, "payment does not belong to you")
		return
	}
	if payment.Status == "completed" {
		response.OK(w, map[string]any{"status": "completed", "message": "credits already granted"})
		return
	}

	muStatus, muAmount, muCurrency, err := h.payClient.VerifyTransaction(r.Context(), body.TransactionID)
	if err != nil {
		response.InternalServerError(w, "payment verification failed")
		return
	}

	if muStatus != "successful" {
		h.repo.UpdatePaymentStatus(r.Context(), body.TxRef, "failed", body.TransactionID)
		response.BadRequest(w, "payment was not successful")
		return
	}

	if muAmount != payment.ZMWAmount || muCurrency != "ZMW" {
		h.repo.UpdatePaymentStatus(r.Context(), body.TxRef, "failed", body.TransactionID)
		response.BadRequest(w, "payment amount mismatch")
		return
	}

	if err := h.repo.UpdatePaymentStatus(r.Context(), body.TxRef, "completed", body.TransactionID); err != nil {
		slog.Error("update payment status failed", "tx_ref", body.TxRef, "error", err)
	}

	bal, err := h.repo.GrantCredits(r.Context(), artistID, payment.CreditAmount, TypePurchase, "moneyunify payment "+body.TxRef)
	if err != nil {
		response.InternalServerError(w, "payment verified but credit grant failed — contact support")
		return
	}

	response.OK(w, map[string]any{
		"status":          "completed",
		"credits_granted": payment.CreditAmount,
		"new_balance":     bal.Balance,
	})
}

// WebhookHandler receives MoneyUnify payment status updates.
func (h *Handler) WebhookHandler(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		slog.Error("moneyunify webhook read body failed", "error", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	var evt payments.WebhookPayload
	if err := json.Unmarshal(body, &evt); err != nil {
		slog.Error("moneyunify webhook parse failed", "error", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	transactionID := fmt.Sprintf("%d", evt.Data.ID)
	txRef := evt.Data.TxRef

	payment, err := h.repo.GetPaymentByTxRef(r.Context(), txRef)
	if err != nil {
		slog.Warn("moneyunify webhook unknown tx_ref", "tx_ref", txRef)
		w.WriteHeader(http.StatusOK)
		return
	}

	if payment.Status == "completed" {
		w.WriteHeader(http.StatusOK)
		return
	}

	if evt.Data.Status != "successful" {
		h.repo.UpdatePaymentStatus(r.Context(), txRef, "failed", transactionID)
		w.WriteHeader(http.StatusOK)
		return
	}

	muStatus, muAmount, muCurrency, err := h.payClient.VerifyTransaction(r.Context(), transactionID)
	if err != nil {
		slog.Error("moneyunify webhook verify failed", "tx_ref", txRef, "error", err)
		w.WriteHeader(http.StatusOK)
		return
	}

	if muStatus != "successful" || muAmount != payment.ZMWAmount || muCurrency != "ZMW" {
		h.repo.UpdatePaymentStatus(r.Context(), txRef, "failed", transactionID)
		w.WriteHeader(http.StatusOK)
		return
	}

	if err := h.repo.UpdatePaymentStatus(r.Context(), txRef, "completed", transactionID); err != nil {
		slog.Error("moneyunify webhook update status failed", "tx_ref", txRef, "error", err)
	}

	_, err = h.repo.GrantCredits(r.Context(), payment.ArtistID, payment.CreditAmount, TypePurchase, "moneyunify payment "+txRef)
	if err != nil {
		slog.Error("moneyunify webhook grant credits failed", "artist_id", payment.ArtistID, "tx_ref", txRef, "error", err)
	}

	w.WriteHeader(http.StatusOK)
}
