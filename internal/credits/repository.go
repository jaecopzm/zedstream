package credits

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Constants that govern the credit economy.
const (
	// FreeCreditsOnSignup is the number of credits auto-granted when an artist registers.
	FreeCreditsOnSignup = 5
	// PricePerCreditZMW is the base price per credit in Zambian Kwacha.
	PricePerCreditZMW = 30
)

// CreditPrice computes the total ZMW price for a given number of credits,
// applying bulk discounts: 10+ at K28/credit, 20+ at K25/credit, 50+ at K20/credit.
func CreditPrice(credits int) int {
	switch {
	case credits >= 50:
		return credits * 20
	case credits >= 20:
		return credits * 25
	case credits >= 10:
		return credits * 28
	default:
		return credits * PricePerCreditZMW
	}
}

// Transaction types (stored in credit_transactions.type).
const (
	TypeFreeGrant    = "free_grant"
	TypeAdminGrant   = "admin_grant"
	TypeAdminRevoke  = "admin_revoke"
	TypePurchase     = "purchase"
	TypeDeduction    = "deduction"
	TypeRefund       = "refund"
)

// ErrInsufficientCredits is returned when a deduction would result in a negative balance.
var ErrInsufficientCredits = errors.New("insufficient credits")

// Balance represents an artist's credit balance and lifetime counters.
type Balance struct {
	ArtistID           string    `json:"artist_id"`
	Balance            int       `json:"balance"`
	LifetimeGranted    int       `json:"lifetime_granted"`
	LifetimePurchased  int       `json:"lifetime_purchased"`
	LifetimeSpent      int       `json:"lifetime_spent"`
	LifetimeRefunded   int       `json:"lifetime_refunded"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// Transaction is a single ledger entry.
type Transaction struct {
	ID          string    `json:"id"`
	ArtistID    string    `json:"artist_id"`
	Amount      int       `json:"amount"`
	Type        string    `json:"type"`
	Description *string   `json:"description,omitempty"`
	ReferenceID *string   `json:"reference_id,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// Repository provides credit operations against Postgres.
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository creates a repository.
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// GetBalance returns the artist's credit balance, or a zero-row if none exists yet.
func (r *Repository) GetBalance(ctx context.Context, artistID string) (*Balance, error) {
	b := &Balance{}
	err := r.db.QueryRow(ctx, `
		SELECT artist_id, balance, lifetime_granted, lifetime_purchased, lifetime_spent, lifetime_refunded, created_at, updated_at
		FROM artist_credits WHERE artist_id = $1
	`, artistID).Scan(&b.ArtistID, &b.Balance, &b.LifetimeGranted, &b.LifetimePurchased, &b.LifetimeSpent, &b.LifetimeRefunded, &b.CreatedAt, &b.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return &Balance{
			ArtistID:  artistID,
			Balance:   0,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get credit balance: %w", err)
	}
	return b, nil
}

// GrantCredits adds credits to the artist's balance and records a ledger entry.
// `txType` should be one of TypeFreeGrant, TypeAdminGrant, TypePurchase.
func (r *Repository) GrantCredits(ctx context.Context, artistID string, amount int, txType, description string) (*Balance, error) {
	if amount <= 0 {
		return r.GetBalance(ctx, artistID)
	}
	var b Balance
	err := r.db.QueryRow(ctx, `
		SELECT apply_credit_transaction($1, $2, $3, $4, NULL)
	`, artistID, amount, txType, description).Scan(&b.Balance)
	if err != nil {
		return nil, fmt.Errorf("grant credits: %w", err)
	}
	return r.GetBalance(ctx, artistID)
}

// RevokeCredits removes credits from an artist (admin action). Returns the new balance.
// Will error if the deduction would make the balance negative.
func (r *Repository) RevokeCredits(ctx context.Context, artistID string, amount int, description string) (*Balance, error) {
	if amount <= 0 {
		return r.GetBalance(ctx, artistID)
	}
	var newBalance int
	err := r.db.QueryRow(ctx, `
		SELECT apply_credit_transaction($1, $2, $3, $4, NULL)
	`, artistID, -amount, TypeAdminRevoke, description).Scan(&newBalance)
	if err != nil {
		return nil, fmt.Errorf("revoke credits: %w", err)
	}
	return r.GetBalance(ctx, artistID)
}

// DeductForTrack atomically deducts 1 credit for a track upload.
// Returns ErrInsufficientCredits if the artist doesn't have enough credits.
func (r *Repository) DeductForTrack(ctx context.Context, artistID, trackID string) error {
	var newBalance int
	err := r.db.QueryRow(ctx, `
		SELECT apply_credit_transaction($1, -1, $2, $3, $4)
	`, artistID, TypeDeduction, "track upload", trackID).Scan(&newBalance)
	if err != nil {
		// apply_credit_transaction raises an exception with "insufficient credits" in the message
		// when the resulting balance would be negative.
		if containsInsufficient(err) {
			return ErrInsufficientCredits
		}
		return fmt.Errorf("deduct credit: %w", err)
	}
	return nil
}

// RefundForTrack refunds 1 credit for a deleted track.
func (r *Repository) RefundForTrack(ctx context.Context, artistID, trackID string) {
	_, err := r.GrantCredits(ctx, artistID, 1, TypeRefund, "track deleted")
	if err != nil {
		slog.Warn("refund credit failed", "artist_id", artistID, "track_id", trackID, "error", err)
	}
}

// ListTransactions returns the most recent ledger entries for an artist.
func (r *Repository) ListTransactions(ctx context.Context, artistID string, limit int) ([]Transaction, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := r.db.Query(ctx, `
		SELECT id, artist_id, amount, type, description, reference_id, created_at
		FROM credit_transactions
		WHERE artist_id = $1
		ORDER BY created_at DESC
		LIMIT $2
	`, artistID, limit)
	if err != nil {
		return nil, fmt.Errorf("list credit transactions: %w", err)
	}
	defer rows.Close()

	var txs []Transaction
	for rows.Next() {
		t := Transaction{}
		if err := rows.Scan(&t.ID, &t.ArtistID, &t.Amount, &t.Type, &t.Description, &t.ReferenceID, &t.CreatedAt); err != nil {
			continue
		}
		txs = append(txs, t)
	}
	return txs, nil
}

// containsInsufficient checks whether an error message indicates the balance went negative.
// We use this rather than SENTINEL-matching because Postgres propagates the RAISE EXCEPTION
// as a message string.
func containsInsufficient(err error) bool {
	if err == nil {
		return false
	}
	return containsFold(err.Error(), "insufficient credits")
}

// ── Payment intents ───────────────────────────────────────────────────────

// PaymentIntent represents a credit purchase payment tracked in the database.
type PaymentIntent struct {
	ID                   string  `json:"id"`
	ArtistID             string  `json:"artist_id"`
	CreditAmount         int     `json:"credit_amount"`
	ZMWAmount            int     `json:"zmw_amount"`
	Currency             string  `json:"currency"`
	Status               string  `json:"status"`
	MoneyUnifyTxRef      string  `json:"moneyunify_tx_ref"`
	MoneyUnifyTxID       *string `json:"moneyunify_transaction_id,omitempty"`
	CreatedAt            string  `json:"created_at"`
	UpdatedAt            string  `json:"updated_at"`
	CompletedAt          *string `json:"completed_at,omitempty"`
}

// CreatePaymentIntent records a pending payment in the database.
func (r *Repository) CreatePaymentIntent(ctx context.Context, artistID string, creditAmount, zmwAmount int, txRef, transactionID string) (*PaymentIntent, error) {
	var p PaymentIntent
	err := r.db.QueryRow(ctx, `
		INSERT INTO credit_payments (artist_id, credit_amount, zmw_amount, moneyunify_tx_ref, moneyunify_transaction_id)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, artist_id, credit_amount, zmw_amount, currency, status, moneyunify_tx_ref, moneyunify_transaction_id, created_at, updated_at, completed_at
	`, artistID, creditAmount, zmwAmount, txRef, transactionID).Scan(
		&p.ID, &p.ArtistID, &p.CreditAmount, &p.ZMWAmount, &p.Currency, &p.Status,
		&p.MoneyUnifyTxRef, &p.MoneyUnifyTxID, &p.CreatedAt, &p.UpdatedAt, &p.CompletedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("create payment intent: %w", err)
	}
	return &p, nil
}

// UpdatePaymentStatus marks a payment as completed or failed, optionally storing the transaction id.
func (r *Repository) UpdatePaymentStatus(ctx context.Context, txRef, status, transactionID string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE credit_payments
		SET status = $1,
		    moneyunify_transaction_id = COALESCE(NULLIF($2, ''), moneyunify_transaction_id),
		    updated_at = now(),
		    completed_at = CASE WHEN $3 THEN now() ELSE completed_at END
		WHERE moneyunify_tx_ref = $4
	`, status, transactionID, status == "completed", txRef)
	if err != nil {
		return fmt.Errorf("update payment status: %w", err)
	}
	return nil
}

// GetPaymentByTxRef retrieves a payment intent by its transaction reference.
func (r *Repository) GetPaymentByTxRef(ctx context.Context, txRef string) (*PaymentIntent, error) {
	var p PaymentIntent
	err := r.db.QueryRow(ctx, `
		SELECT id, artist_id, credit_amount, zmw_amount, currency, status, moneyunify_tx_ref, moneyunify_transaction_id, created_at, updated_at, completed_at
		FROM credit_payments WHERE moneyunify_tx_ref = $1
	`, txRef).Scan(
		&p.ID, &p.ArtistID, &p.CreditAmount, &p.ZMWAmount, &p.Currency, &p.Status,
		&p.MoneyUnifyTxRef, &p.MoneyUnifyTxID, &p.CreatedAt, &p.UpdatedAt, &p.CompletedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("get payment by tx_ref: %w", err)
	}
	return &p, nil
}

func containsFold(s, substr string) bool {
	if len(substr) == 0 {
		return true
	}
	if len(s) < len(substr) {
		return false
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		match := true
		for j := 0; j < len(substr); j++ {
			c1, c2 := s[i+j], substr[j]
			if c1 >= 'A' && c1 <= 'Z' {
				c1 += 32
			}
			if c2 >= 'A' && c2 <= 'Z' {
				c2 += 32
			}
			if c1 != c2 {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
