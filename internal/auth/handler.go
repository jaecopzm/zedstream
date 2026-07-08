package auth

import (
	"encoding/json"
	"net/http"

	"github.com/jaecopzm/zedstream/pkg/response"
)

// Handler exposes HTTP endpoints for auth flows.
type Handler struct {
	authSvc   *Service
	oauthSvc  *OAuthService
}

// NewHandler creates a new auth HTTP handler.
func NewHandler(authSvc *Service, oauthSvc *OAuthService) *Handler {
	return &Handler{authSvc: authSvc, oauthSvc: oauthSvc}
}

// GoogleLogin redirects the user to Google's OAuth2 consent page.
//
// @Summary     Google OAuth2 login
// @Tags        auth
// @Router      /auth/google [get]
func (h *Handler) GoogleLogin(w http.ResponseWriter, r *http.Request) {
	state, err := GenerateState()
	if err != nil {
		response.InternalServerError(w, "failed to generate state")
		return
	}
	// In production, store state in a short-lived cookie for CSRF validation
	http.SetCookie(w, &http.Cookie{
		Name:     "oauth_state",
		Value:    state,
		HttpOnly: true,
		Path:     "/",
		MaxAge:   300,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, h.oauthSvc.GoogleAuthURL(state), http.StatusTemporaryRedirect)
}

// GoogleCallback handles Google OAuth2 callback and issues JWT tokens.
//
// @Summary     Google OAuth2 callback
// @Tags        auth
// @Router      /auth/google/callback [get]
func (h *Handler) GoogleCallback(w http.ResponseWriter, r *http.Request) {
	// Validate state
	stateCookie, err := r.Cookie("oauth_state")
	if err != nil || stateCookie.Value != r.URL.Query().Get("state") {
		response.BadRequest(w, "invalid oauth state")
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		response.BadRequest(w, "missing oauth code")
		return
	}

	pair, err := h.oauthSvc.HandleGoogleCallback(r.Context(), code)
	if err != nil {
		response.InternalServerError(w, "google oauth failed")
		return
	}

	response.OK(w, pair)
}

// FacebookLogin redirects the user to Facebook's OAuth2 consent page.
//
// @Summary     Facebook OAuth2 login
// @Tags        auth
// @Router      /auth/facebook [get]
func (h *Handler) FacebookLogin(w http.ResponseWriter, r *http.Request) {
	state, err := GenerateState()
	if err != nil {
		response.InternalServerError(w, "failed to generate state")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "oauth_state",
		Value:    state,
		HttpOnly: true,
		Path:     "/",
		MaxAge:   300,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, h.oauthSvc.FacebookAuthURL(state), http.StatusTemporaryRedirect)
}

// FacebookCallback handles Facebook OAuth2 callback and issues JWT tokens.
//
// @Summary     Facebook OAuth2 callback
// @Tags        auth
// @Router      /auth/facebook/callback [get]
func (h *Handler) FacebookCallback(w http.ResponseWriter, r *http.Request) {
	stateCookie, err := r.Cookie("oauth_state")
	if err != nil || stateCookie.Value != r.URL.Query().Get("state") {
		response.BadRequest(w, "invalid oauth state")
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		response.BadRequest(w, "missing oauth code")
		return
	}

	pair, err := h.oauthSvc.HandleFacebookCallback(r.Context(), code)
	if err != nil {
		response.InternalServerError(w, "facebook oauth failed")
		return
	}

	response.OK(w, pair)
}

// RefreshToken issues a new token pair using a refresh token.
//
// @Summary     Refresh access token
// @Tags        auth
// @Accept      json
// @Produce     json
// @Param       body body object{refresh_token=string} true "Refresh token"
// @Router      /auth/refresh [post]
func (h *Handler) RefreshToken(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.RefreshToken == "" {
		response.BadRequest(w, "refresh_token is required")
		return
	}

	pair, err := h.authSvc.RefreshTokens(r.Context(), body.RefreshToken)
	if err != nil {
		response.Unauthorized(w, err.Error())
		return
	}

	response.OK(w, pair)
}

// Logout revokes the provided refresh token.
//
// @Summary     Logout
// @Tags        auth
// @Accept      json
// @Router      /auth/logout [post]
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.RefreshToken == "" {
		response.BadRequest(w, "refresh_token is required")
		return
	}

	if err := h.authSvc.Logout(r.Context(), body.RefreshToken); err != nil {
		response.InternalServerError(w, "logout failed")
		return
	}

	response.NoContent(w)
}
