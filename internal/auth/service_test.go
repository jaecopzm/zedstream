package auth_test

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jaecopzm/zedstream/internal/auth"
)

func TestValidateAccessToken_Valid(t *testing.T) {
	svc := auth.NewService(nil, "test-secret", 15, 30)

	claims := &auth.Claims{
		UserID: "user-123",
		Email:  "test@example.com",
		Role:   "listener",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "user-123",
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(15 * time.Minute)),
			Issuer:    "zedstream",
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte("test-secret"))
	if err != nil {
		t.Fatalf("failed to sign token: %v", err)
	}

	result, err := svc.ValidateAccessToken(signed)
	if err != nil {
		t.Fatalf("expected valid token, got error: %v", err)
	}
	if result.UserID != "user-123" {
		t.Errorf("expected UserID=user-123, got %s", result.UserID)
	}
	if result.Role != "listener" {
		t.Errorf("expected Role=listener, got %s", result.Role)
	}
}

func TestValidateAccessToken_Expired(t *testing.T) {
	svc := auth.NewService(nil, "test-secret", 15, 30)

	claims := &auth.Claims{
		UserID: "user-123",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-1 * time.Hour)),
			Issuer:    "zedstream",
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, _ := token.SignedString([]byte("test-secret"))

	_, err := svc.ValidateAccessToken(signed)
	if err == nil {
		t.Error("expected error for expired token, got nil")
	}
}

func TestValidateAccessToken_WrongSecret(t *testing.T) {
	svc := auth.NewService(nil, "correct-secret", 15, 30)

	claims := &auth.Claims{
		UserID: "user-123",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(15 * time.Minute)),
			Issuer:    "zedstream",
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, _ := token.SignedString([]byte("wrong-secret"))

	_, err := svc.ValidateAccessToken(signed)
	if err == nil {
		t.Error("expected error for wrong secret, got nil")
	}
}

func TestValidateAccessToken_MalformedToken(t *testing.T) {
	svc := auth.NewService(nil, "test-secret", 15, 30)

	_, err := svc.ValidateAccessToken("not.a.valid.jwt.token")
	if err == nil {
		t.Error("expected error for malformed token, got nil")
	}
}

func TestValidateAccessToken_EmptyToken(t *testing.T) {
	svc := auth.NewService(nil, "test-secret", 15, 30)

	_, err := svc.ValidateAccessToken("")
	if err == nil {
		t.Error("expected error for empty token, got nil")
	}
}
