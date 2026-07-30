package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jaecopzm/zedstream/pkg/id"
	"golang.org/x/crypto/bcrypt"
)

// Role constants
const (
	RoleListener = "listener"
	RoleArtist   = "artist"
	RoleAdmin    = "admin"
)

// Claims holds the JWT payload.
type Claims struct {
	UserID   string `json:"user_id"`
	Email    string `json:"email"`
	Role     string `json:"role"`
	ArtistID string `json:"artist_id,omitempty"`
	jwt.RegisteredClaims
}

// TokenPair holds both access and refresh tokens.
type TokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"` // seconds
}

// Service handles token creation, validation and refresh.
type Service struct {
	db             *pgxpool.Pool
	jwtSecret      []byte
	accessTTL      time.Duration
	refreshTTLDays int
}

// NewService creates a new auth service.
func NewService(db *pgxpool.Pool, jwtSecret string, accessTTLMins, refreshTTLDays int) *Service {
	return &Service{
		db:             db,
		jwtSecret:      []byte(jwtSecret),
		accessTTL:      time.Duration(accessTTLMins) * time.Minute,
		refreshTTLDays: refreshTTLDays,
	}
}

// IssueTokenPair generates an access + refresh token pair for the given user.
func (s *Service) IssueTokenPair(ctx context.Context, userID, email, role string) (*TokenPair, error) {
	now := time.Now()

	var artistID string
	if role == RoleArtist || role == RoleAdmin {
		_ = s.db.QueryRow(ctx,
			`SELECT id FROM artists WHERE user_id = $1`, userID,
		).Scan(&artistID)
	}

	claims := &Claims{
		UserID:   userID,
		Email:    email,
		Role:     role,
		ArtistID: artistID,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(s.accessTTL)),
			Issuer:    "zedstream",
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	accessToken, err := token.SignedString(s.jwtSecret)
	if err != nil {
		return nil, fmt.Errorf("sign access token: %w", err)
	}

	// Generate a secure random refresh token
	rawToken, err := generateSecureToken(32)
	if err != nil {
		return nil, fmt.Errorf("generate refresh token: %w", err)
	}

	tokenHash := hashToken(rawToken)
	expiresAt := now.AddDate(0, 0, s.refreshTTLDays)

	_, err = s.db.Exec(ctx,
		`INSERT INTO refresh_tokens (id, user_id, token_hash, expires_at)
		 VALUES ($1, $2, $3, $4)`,
		id.New(), userID, tokenHash, expiresAt,
	)
	if err != nil {
		return nil, fmt.Errorf("store refresh token: %w", err)
	}

	return &TokenPair{
		AccessToken:  accessToken,
		RefreshToken: rawToken,
		ExpiresIn:    int(s.accessTTL.Seconds()),
	}, nil
}

// ValidateAccessToken parses and validates a JWT access token.
func (s *Service) ValidateAccessToken(tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return s.jwtSecret, nil
	})
	if err != nil {
		return nil, fmt.Errorf("parse token: %w", err)
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid token claims")
	}

	return claims, nil
}

// RefreshTokens rotates the refresh token and issues a new token pair.
func (s *Service) RefreshTokens(ctx context.Context, rawRefreshToken string) (*TokenPair, error) {
	tokenHash := hashToken(rawRefreshToken)

	var (
		tokenID   string
		userID    string
		expiresAt time.Time
		revoked   bool
	)

	err := s.db.QueryRow(ctx,
		`SELECT id, user_id, expires_at, revoked FROM refresh_tokens WHERE token_hash = $1`,
		tokenHash,
	).Scan(&tokenID, &userID, &expiresAt, &revoked)
	if err != nil {
		return nil, errors.New("refresh token not found")
	}

	if revoked {
		return nil, errors.New("refresh token has been revoked")
	}

	if time.Now().After(expiresAt) {
		return nil, errors.New("refresh token has expired")
	}

	// Revoke old token (rotation)
	_, err = s.db.Exec(ctx,
		`UPDATE refresh_tokens SET revoked = TRUE WHERE id = $1`, tokenID,
	)
	if err != nil {
		return nil, fmt.Errorf("revoke old refresh token: %w", err)
	}

	// Fetch user info for new token
	var email, role string
	err = s.db.QueryRow(ctx,
		`SELECT email, role FROM users WHERE id = $1`, userID,
	).Scan(&email, &role)
	if err != nil {
		return nil, fmt.Errorf("fetch user: %w", err)
	}

	return s.IssueTokenPair(ctx, userID, email, role)
}

// Logout revokes the given refresh token.
func (s *Service) Logout(ctx context.Context, rawRefreshToken string) error {
	tokenHash := hashToken(rawRefreshToken)
	_, err := s.db.Exec(ctx,
		`UPDATE refresh_tokens SET revoked = TRUE WHERE token_hash = $1`, tokenHash,
	)
	return err
}

// RegisterWithEmail creates a new user with email/password and issues a token pair.
func (s *Service) RegisterWithEmail(ctx context.Context, email, password string) (*TokenPair, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	var userID string
	err = s.db.QueryRow(ctx,
		`INSERT INTO users (email, name, role, provider, provider_id, password_hash)
		 VALUES ($1, $1, 'listener', 'email', $1, $2)
		 RETURNING id`,
		email, string(hash),
	).Scan(&userID)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, errors.New("an account with this email already exists")
		}
		return nil, fmt.Errorf("insert user: %w", err)
	}

	return s.IssueTokenPair(ctx, userID, email, RoleListener)
}

// LoginWithEmail verifies email/password and issues a token pair.
func (s *Service) LoginWithEmail(ctx context.Context, email, password string) (*TokenPair, error) {
	var userID, passwordHash, role string
	err := s.db.QueryRow(ctx,
		`SELECT id, COALESCE(password_hash, ''), role FROM users WHERE email = $1`,
		email,
	).Scan(&userID, &passwordHash, &role)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, errors.New("invalid email or password")
		}
		return nil, fmt.Errorf("query user: %w", err)
	}

	if passwordHash == "" {
		return nil, errors.New("this account uses social login; sign in with Google or Facebook")
	}

	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password)); err != nil {
		return nil, errors.New("invalid email or password")
	}

	return s.IssueTokenPair(ctx, userID, email, role)
}

// isUniqueViolation checks if a pgx error is a unique constraint violation.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// generateSecureToken creates a cryptographically random hex token.
func generateSecureToken(length int) (string, error) {
	b := make([]byte, length)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// hashToken returns the SHA-256 hex hash of a token string.
func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
