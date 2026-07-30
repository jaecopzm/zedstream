package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jaecopzm/zedstream/internal/auth"
	appMiddleware "github.com/jaecopzm/zedstream/pkg/middleware"
)

func makeSignedToken(secret, userID, role string, expiry time.Duration) string {
	claims := &auth.Claims{
		UserID: userID,
		Email:  "test@example.com",
		Role:   role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(expiry)),
			Issuer:    "zedstream",
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, _ := token.SignedString([]byte(secret))
	return signed
}

func TestAuthenticate_ValidToken_PassesThrough(t *testing.T) {
	svc := auth.NewService(nil, "test-secret", 15, 30)
	mw := appMiddleware.Authenticate(svc)

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		claims := appMiddleware.ClaimsFromContext(r.Context())
		if claims == nil {
			t.Error("expected claims in context, got nil")
		}
		if claims.UserID != "user-1" {
			t.Errorf("expected UserID=user-1, got %s", claims.UserID)
		}
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+makeSignedToken("test-secret", "user-1", "listener", 15*time.Minute))
	rr := httptest.NewRecorder()

	mw(next).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	if !called {
		t.Error("next handler was not called")
	}
}

func TestAuthenticate_MissingHeader_Returns401(t *testing.T) {
	svc := auth.NewService(nil, "test-secret", 15, 30)
	mw := appMiddleware.Authenticate(svc)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	mw(next).ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestAuthenticate_ExpiredToken_Returns401(t *testing.T) {
	svc := auth.NewService(nil, "test-secret", 15, 30)
	mw := appMiddleware.Authenticate(svc)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+makeSignedToken("test-secret", "user-1", "listener", -1*time.Minute))
	rr := httptest.NewRecorder()
	mw(next).ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestRequireRole_CorrectRole_PassesThrough(t *testing.T) {
	svc := auth.NewService(nil, "test-secret", 15, 30)
	authMW := appMiddleware.Authenticate(svc)
	roleMW := appMiddleware.RequireRole("artist")

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+makeSignedToken("test-secret", "user-1", "artist", 15*time.Minute))
	rr := httptest.NewRecorder()

	authMW(roleMW(next)).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	if !called {
		t.Error("next handler was not called")
	}
}

func TestRequireRole_WrongRole_Returns403(t *testing.T) {
	svc := auth.NewService(nil, "test-secret", 15, 30)
	authMW := appMiddleware.Authenticate(svc)
	roleMW := appMiddleware.RequireRole("admin")

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+makeSignedToken("test-secret", "user-1", "listener", 15*time.Minute))
	rr := httptest.NewRecorder()

	authMW(roleMW(next)).ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rr.Code)
	}
}
