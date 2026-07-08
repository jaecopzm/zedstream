package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/facebook"
	"golang.org/x/oauth2/google"
)

// OAuthUser holds the normalized user info from an OAuth provider.
type OAuthUser struct {
	ProviderID string
	Email      string
	Name       string
	AvatarURL  string
	Provider   string
}

// OAuthService handles OAuth2 flows for Google and Facebook.
type OAuthService struct {
	db           *pgxpool.Pool
	authSvc      *Service
	googleCfg    *oauth2.Config
	facebookCfg  *oauth2.Config
	stateSecret  string
}

// NewOAuthService creates the OAuth2 service with provider configs.
func NewOAuthService(
	db *pgxpool.Pool,
	authSvc *Service,
	googleClientID, googleClientSecret, googleRedirect string,
	fbClientID, fbClientSecret, fbRedirect string,
) *OAuthService {
	return &OAuthService{
		db:      db,
		authSvc: authSvc,
		googleCfg: &oauth2.Config{
			ClientID:     googleClientID,
			ClientSecret: googleClientSecret,
			RedirectURL:  googleRedirect,
			Scopes:       []string{"openid", "email", "profile"},
			Endpoint:     google.Endpoint,
		},
		facebookCfg: &oauth2.Config{
			ClientID:     fbClientID,
			ClientSecret: fbClientSecret,
			RedirectURL:  fbRedirect,
			Scopes:       []string{"email", "public_profile"},
			Endpoint:     facebook.Endpoint,
		},
	}
}

// GoogleAuthURL returns the OAuth2 authorization URL for Google.
func (s *OAuthService) GoogleAuthURL(state string) string {
	return s.googleCfg.AuthCodeURL(state, oauth2.AccessTypeOnline)
}

// FacebookAuthURL returns the OAuth2 authorization URL for Facebook.
func (s *OAuthService) FacebookAuthURL(state string) string {
	return s.facebookCfg.AuthCodeURL(state, oauth2.AccessTypeOnline)
}

// HandleGoogleCallback exchanges the code for user info and upserts the user.
func (s *OAuthService) HandleGoogleCallback(ctx context.Context, code string) (*TokenPair, error) {
	token, err := s.googleCfg.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("exchange google code: %w", err)
	}

	client := s.googleCfg.Client(ctx, token)
	resp, err := client.Get("https://www.googleapis.com/oauth2/v3/userinfo")
	if err != nil {
		return nil, fmt.Errorf("fetch google user info: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var info struct {
		Sub     string `json:"sub"`
		Email   string `json:"email"`
		Name    string `json:"name"`
		Picture string `json:"picture"`
	}
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, fmt.Errorf("parse google user info: %w", err)
	}

	oauthUser := &OAuthUser{
		ProviderID: info.Sub,
		Email:      info.Email,
		Name:       info.Name,
		AvatarURL:  info.Picture,
		Provider:   "google",
	}

	return s.upsertUserAndIssueTokens(ctx, oauthUser)
}

// HandleFacebookCallback exchanges the code for user info and upserts the user.
func (s *OAuthService) HandleFacebookCallback(ctx context.Context, code string) (*TokenPair, error) {
	token, err := s.facebookCfg.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("exchange facebook code: %w", err)
	}

	client := s.facebookCfg.Client(ctx, token)
	resp, err := client.Get("https://graph.facebook.com/me?fields=id,name,email,picture")
	if err != nil {
		return nil, fmt.Errorf("fetch facebook user info: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var info struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Email   string `json:"email"`
		Picture struct {
			Data struct {
				URL string `json:"url"`
			} `json:"data"`
		} `json:"picture"`
	}
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, fmt.Errorf("parse facebook user info: %w", err)
	}

	oauthUser := &OAuthUser{
		ProviderID: info.ID,
		Email:      info.Email,
		Name:       info.Name,
		AvatarURL:  info.Picture.Data.URL,
		Provider:   "facebook",
	}

	return s.upsertUserAndIssueTokens(ctx, oauthUser)
}

// upsertUserAndIssueTokens creates or updates the user, then issues JWT tokens.
func (s *OAuthService) upsertUserAndIssueTokens(ctx context.Context, u *OAuthUser) (*TokenPair, error) {
	var userID, email, role string

	err := s.db.QueryRow(ctx, `
		INSERT INTO users (id, email, name, avatar_url, provider, provider_id)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (provider, provider_id) DO UPDATE
		    SET email      = EXCLUDED.email,
		        name       = EXCLUDED.name,
		        avatar_url = EXCLUDED.avatar_url,
		        updated_at = NOW()
		RETURNING id, email, role
	`,
		uuid.New().String(),
		u.Email,
		u.Name,
		u.AvatarURL,
		u.Provider,
		u.ProviderID,
	).Scan(&userID, &email, &role)
	if err != nil {
		return nil, fmt.Errorf("upsert user: %w", err)
	}

	return s.authSvc.IssueTokenPair(ctx, userID, email, role)
}

// GenerateState generates a random state string for OAuth2 CSRF protection.
func GenerateState() (string, error) {
	return generateSecureToken(16)
}

// FetchUserByID retrieves a user record by ID.
func FetchUserByID(ctx context.Context, db *pgxpool.Pool, userID string) (*UserRecord, error) {
	var u UserRecord
	err := db.QueryRow(ctx,
		`SELECT id, email, name, avatar_url, role, provider, created_at FROM users WHERE id = $1`,
		userID,
	).Scan(&u.ID, &u.Email, &u.Name, &u.AvatarURL, &u.Role, &u.Provider, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// UserRecord is a minimal user struct for context injection.
type UserRecord struct {
	ID        string
	Email     string
	Name      string
	AvatarURL string
	Role      string
	Provider  string
	CreatedAt interface{}
}
