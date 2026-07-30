package auth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/jaecopzm/zedstream/pkg/response"
)

// Handler exposes HTTP endpoints for auth flows.
type Handler struct {
	authSvc   *Service
	oauthSvc  *OAuthService

	frontendURLs []string
}

// NewHandler creates a new auth HTTP handler.
func NewHandler(authSvc *Service, oauthSvc *OAuthService, frontendURLs ...string) *Handler {
	allowed := frontendURLs
	if len(allowed) == 0 {
		allowed = []string{"http://localhost:3000"}
	}
	return &Handler{
		authSvc:      authSvc,
		oauthSvc:     oauthSvc,
		frontendURLs: allowed,
	}
}

func (h *Handler) isAllowedOrigin(origin string) bool {
	for _, u := range h.frontendURLs {
		if origin == u {
			return true
		}
	}
	return false
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
	http.SetCookie(w, &http.Cookie{
		Name:     "oauth_state",
		Value:    state,
		HttpOnly: true,
		Secure:   true,
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

	frontendURL := h.frontendURLs[0]
	if origin := r.Header.Get("Origin"); origin != "" && h.isAllowedOrigin(origin) {
		frontendURL = origin
	}
	q := url.Values{
		"access_token":  {pair.AccessToken},
		"refresh_token": {pair.RefreshToken},
		"expires_in":    {fmt.Sprint(pair.ExpiresIn)},
	}
	http.Redirect(w, r, frontendURL+"/auth/callback?"+q.Encode(), http.StatusTemporaryRedirect)
}

// DevLogin bypasses OAuth and issues a token for a test user (for development).
func (h *Handler) DevLogin(w http.ResponseWriter, r *http.Request) {
	pair, err := h.oauthSvc.HandleDevLogin(r.Context())
	if err != nil {
		response.InternalServerError(w, "failed to generate dev tokens")
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
		Secure:   true,
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

	frontendURL := h.frontendURLs[0]
	if origin := r.Header.Get("Origin"); origin != "" && h.isAllowedOrigin(origin) {
		frontendURL = origin
	}
	q := url.Values{
		"access_token":  {pair.AccessToken},
		"refresh_token": {pair.RefreshToken},
		"expires_in":    {fmt.Sprint(pair.ExpiresIn)},
	}
	http.Redirect(w, r, frontendURL+"/auth/callback?"+q.Encode(), http.StatusTemporaryRedirect)
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

// Register creates a new user with email and password.
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		response.BadRequest(w, "invalid request body")
		return
	}
	if body.Email == "" || body.Password == "" {
		response.BadRequest(w, "email and password are required")
		return
	}
	if len(body.Password) < 8 {
		response.BadRequest(w, "password must be at least 8 characters")
		return
	}

	pair, err := h.authSvc.RegisterWithEmail(r.Context(), body.Email, body.Password)
	if err != nil {
		response.BadRequest(w, err.Error())
		return
	}

	response.OK(w, pair)
}

// Login authenticates with email and password.
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		response.BadRequest(w, "invalid request body")
		return
	}
	if body.Email == "" || body.Password == "" {
		response.BadRequest(w, "email and password are required")
		return
	}

	pair, err := h.authSvc.LoginWithEmail(r.Context(), body.Email, body.Password)
	if err != nil {
		response.Unauthorized(w, err.Error())
		return
	}

	response.OK(w, pair)
}

// LoginPage redirects GET /auth/login to the frontend login page.
func (h *Handler) LoginPage(w http.ResponseWriter, r *http.Request) {
	target := "/"
	if len(h.frontendURLs) > 0 {
		u, _ := url.Parse(h.frontendURLs[0])
		target = u.Path
		if target == "" {
			target = "/"
		}
	}
	http.Redirect(w, r, target, http.StatusTemporaryRedirect)
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
