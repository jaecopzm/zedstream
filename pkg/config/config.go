package config

import (
	"fmt"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

// Config holds all application configuration loaded from environment variables.
type Config struct {
	AppEnv  string
	Port    string
	BaseURL string

	FrontendURL string

	DatabaseURL string

	JWTSecret          string
	JWTAccessTTLMins   int
	JWTRefreshTTLDays  int

	GoogleClientID       string
	GoogleClientSecret   string
	GoogleRedirectURL    string

	FacebookClientID       string
	FacebookClientSecret   string
	FacebookRedirectURL    string

	R2AccountID       string
	R2AccessKeyID     string
	R2SecretAccessKey string
	R2BucketAudio     string
	R2BucketImages    string
	R2PublicURLAudio  string
	R2PublicURLImages string

	RateLimitRequests       int
	RateLimitWindowSeconds  int

	MoneyUnifyAuthID     string
	MoneyUnifyWebhookURL string
	MoneyUnifyWebhookKey string
}

// Load reads configuration from environment variables, optionally loading a .env file.
func Load() (*Config, error) {
	// Load .env if present; ignore error (file may not exist in production)
	_ = godotenv.Load()

	cfg := &Config{
		AppEnv:  getEnv("APP_ENV", "development"),
		Port:    getEnv("PORT", "8080"),
		BaseURL: getEnv("BASE_URL", "http://localhost:8080"),

		FrontendURL: getEnv("FRONTEND_URL", "http://localhost:3000"),

		DatabaseURL: mustGetEnv("DATABASE_URL"),

		JWTSecret:         mustGetEnv("JWT_SECRET"),
		JWTAccessTTLMins:  getEnvInt("JWT_ACCESS_TTL_MINUTES", 15),
		JWTRefreshTTLDays: getEnvInt("JWT_REFRESH_TTL_DAYS", 30),

		GoogleClientID:     mustGetEnv("GOOGLE_CLIENT_ID"),
		GoogleClientSecret: mustGetEnv("GOOGLE_CLIENT_SECRET"),
		GoogleRedirectURL:  mustGetEnv("GOOGLE_REDIRECT_URL"),

		FacebookClientID:     mustGetEnv("FACEBOOK_CLIENT_ID"),
		FacebookClientSecret: mustGetEnv("FACEBOOK_CLIENT_SECRET"),
		FacebookRedirectURL:  mustGetEnv("FACEBOOK_REDIRECT_URL"),

		R2AccountID:       mustGetEnv("R2_ACCOUNT_ID"),
		R2AccessKeyID:     mustGetEnv("R2_ACCESS_KEY_ID"),
		R2SecretAccessKey: mustGetEnv("R2_SECRET_ACCESS_KEY"),
		R2BucketAudio:     getEnv("R2_BUCKET_AUDIO", "zedstream-audio"),
		R2BucketImages:    getEnv("R2_BUCKET_IMAGES", "zedstream-images"),
		R2PublicURLAudio:  mustGetEnv("R2_PUBLIC_URL_AUDIO"),
		R2PublicURLImages: mustGetEnv("R2_PUBLIC_URL_IMAGES"),

		RateLimitRequests:      getEnvInt("RATE_LIMIT_REQUESTS", 100),
		RateLimitWindowSeconds: getEnvInt("RATE_LIMIT_WINDOW_SECONDS", 60),

		MoneyUnifyAuthID:     getEnv("MONEYUNIFY_AUTH_ID", ""),
		MoneyUnifyWebhookURL: getEnv("MONEYUNIFY_WEBHOOK_URL", ""),
		MoneyUnifyWebhookKey: getEnv("MONEYUNIFY_WEBHOOK_KEY", ""),
	}

	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func mustGetEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		panic(fmt.Sprintf("required environment variable %q is not set", key))
	}
	return v
}

func getEnvInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

