package config_test

import (
	"os"
	"testing"

	"github.com/jaecopzm/zedstream/pkg/config"
)

func TestLoad_WithAllRequiredVars(t *testing.T) {
	required := map[string]string{
		"DATABASE_URL":          "postgres://user:pass@localhost:5432/zedstream",
		"JWT_SECRET":            "test-secret-key",
		"GOOGLE_CLIENT_ID":      "google-id",
		"GOOGLE_CLIENT_SECRET":  "google-secret",
		"GOOGLE_REDIRECT_URL":   "http://localhost/callback",
		"FACEBOOK_CLIENT_ID":    "fb-id",
		"FACEBOOK_CLIENT_SECRET": "fb-secret",
		"FACEBOOK_REDIRECT_URL": "http://localhost/fb-callback",
		"R2_ACCOUNT_ID":         "r2-account",
		"R2_ACCESS_KEY_ID":      "r2-key",
		"R2_SECRET_ACCESS_KEY":  "r2-secret",
		"R2_PUBLIC_URL":         "https://pub.r2.dev",
	}

	for k, v := range required {
		t.Setenv(k, v)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if cfg.DatabaseURL != required["DATABASE_URL"] {
		t.Errorf("DatabaseURL mismatch: got %s", cfg.DatabaseURL)
	}

	if cfg.JWTAccessTTLMins != 15 {
		t.Errorf("expected default JWTAccessTTLMins=15, got %d", cfg.JWTAccessTTLMins)
	}

	if cfg.Port != "8080" {
		t.Errorf("expected default Port=8080, got %s", cfg.Port)
	}
}

func TestLoad_CustomPort(t *testing.T) {
	for _, k := range []string{
		"DATABASE_URL", "JWT_SECRET", "GOOGLE_CLIENT_ID", "GOOGLE_CLIENT_SECRET",
		"GOOGLE_REDIRECT_URL", "FACEBOOK_CLIENT_ID", "FACEBOOK_CLIENT_SECRET",
		"FACEBOOK_REDIRECT_URL", "R2_ACCOUNT_ID", "R2_ACCESS_KEY_ID",
		"R2_SECRET_ACCESS_KEY", "R2_PUBLIC_URL",
	} {
		t.Setenv(k, "test-value")
	}
	t.Setenv("PORT", "9090")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Port != "9090" {
		t.Errorf("expected Port=9090, got %s", cfg.Port)
	}
}

func TestLoad_MissingRequired_Panics(t *testing.T) {
	// Unset all env vars to test panic on missing required
	os.Clearenv()

	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for missing required env var, got none")
		}
	}()

	_, _ = config.Load()
}
