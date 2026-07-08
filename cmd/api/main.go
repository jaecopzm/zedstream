// @title ZedStream API
// @version 1.0
// @description Zambian music streaming platform API
// @host localhost:8080
// @BasePath /api/v1
// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"

	"github.com/jaecopzm/zedstream/internal/analytics"
	"github.com/jaecopzm/zedstream/internal/artist"
	"github.com/jaecopzm/zedstream/internal/auth"
	"github.com/jaecopzm/zedstream/internal/music"
	"github.com/jaecopzm/zedstream/internal/recommendations"
	"github.com/jaecopzm/zedstream/internal/scheduler"
	"github.com/jaecopzm/zedstream/internal/social"
	"github.com/jaecopzm/zedstream/internal/streaming"
	"github.com/jaecopzm/zedstream/pkg/config"
	"github.com/jaecopzm/zedstream/pkg/database"
	appMiddleware "github.com/jaecopzm/zedstream/pkg/middleware"
	"github.com/jaecopzm/zedstream/pkg/response"
	"github.com/jaecopzm/zedstream/pkg/storage"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	// ── Config ──────────────────────────────────────────────────────────────
	cfg, err := config.Load()
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	// ── Database ─────────────────────────────────────────────────────────────
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	db, err := database.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer db.Close()
	logger.Info("database connected")

	// ── Storage ──────────────────────────────────────────────────────────────
	store, err := storage.New(storage.Config{
		AccountID:       cfg.R2AccountID,
		AccessKeyID:     cfg.R2AccessKeyID,
		SecretAccessKey: cfg.R2SecretAccessKey,
		PublicURL:       cfg.R2PublicURL,
	})
	if err != nil {
		logger.Error("failed to init storage client", "error", err)
		os.Exit(1)
	}
	logger.Info("storage client initialized")

	// ── Services & Handlers ──────────────────────────────────────────────────
	authSvc := auth.NewService(db, cfg.JWTSecret, cfg.JWTAccessTTLMins, cfg.JWTRefreshTTLDays)

	oauthSvc := auth.NewOAuthService(
		db, authSvc,
		cfg.GoogleClientID, cfg.GoogleClientSecret, cfg.GoogleRedirectURL,
		cfg.FacebookClientID, cfg.FacebookClientSecret, cfg.FacebookRedirectURL,
	)

	authHandler       := auth.NewHandler(authSvc, oauthSvc)
	artistRepo        := artist.NewRepository(db)
	artistHandler     := artist.NewHandler(artistRepo, store, cfg.R2BucketImages)
	musicRepo         := music.NewRepository(db)
	musicHandler      := music.NewHandler(musicRepo, store, cfg.R2BucketAudio, cfg.R2BucketImages)
	streamingHandler  := streaming.NewHandler(db, store, cfg.R2BucketAudio)
	socialHandler     := social.NewHandler(db, cfg.BaseURL)
	analyticsHandler  := analytics.NewHandler(db, cfg.RevenueRatePerStream)
	recoHandler       := recommendations.NewHandler(db)

	// ── Scheduler ────────────────────────────────────────────────────────────
	bgCtx, bgCancel := context.WithCancel(context.Background())
	defer bgCancel()
	sched := scheduler.New(db, logger)
	sched.Start(bgCtx)

	// ── Router ───────────────────────────────────────────────────────────────
	r := chi.NewRouter()

	// Global middleware
	r.Use(chimiddleware.RequestID)
	r.Use(chimiddleware.RealIP)
	r.Use(chimiddleware.Recoverer)
	r.Use(appMiddleware.Logger(logger))
	r.Use(appMiddleware.RateLimit(
		cfg.RateLimitRequests,
		time.Duration(cfg.RateLimitWindowSeconds)*time.Second,
	))
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"https://*", "http://*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-Request-Id"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	// Health check
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		response.OK(w, map[string]string{"status": "ok", "service": "zedstream"})
	})

	// ── Public routes ─────────────────────────────────────────────────────────
	r.Route("/api/v1", func(r chi.Router) {
		r.Use(appMiddleware.LimitBody(1 << 20)) // 1 MB limit for JSON endpoints

		// Auth
		r.Get("/auth/google", authHandler.GoogleLogin)
		r.Get("/auth/google/callback", authHandler.GoogleCallback)
		r.Get("/auth/facebook", authHandler.FacebookLogin)
		r.Get("/auth/facebook/callback", authHandler.FacebookCallback)
		r.Post("/auth/refresh", authHandler.RefreshToken)
		r.Post("/auth/logout", authHandler.Logout)

		// Public music browsing
		r.Get("/tracks", musicHandler.ListTracks)
		r.Get("/tracks/search", musicHandler.SearchTracks)
		r.Get("/genres", musicHandler.ListGenres)

		// Routes with UUID {id} params
		r.Group(func(r chi.Router) {
			r.Use(appMiddleware.RequireValidUUID("id"))

			r.Get("/genres/{id}/tracks", musicHandler.GetTracksByGenre)
			r.Get("/albums/{id}", musicHandler.GetAlbum)
			r.Get("/artists/{id}", artistHandler.GetByID)
			r.Get("/tracks/{id}/comments", socialHandler.GetComments)
		})

		// ── Authenticated routes ───────────────────────────────────────────────
		r.Group(func(r chi.Router) {
			r.Use(appMiddleware.Authenticate(authSvc))
			r.Use(appMiddleware.RateLimitByUser(
				cfg.RateLimitRequests*2,
				time.Duration(cfg.RateLimitWindowSeconds)*time.Second,
			))

			// Artist registration (any authenticated user)
			r.Post("/artists/register", artistHandler.Register)

			// Routes with UUID {id} params
			r.Group(func(r chi.Router) {
				r.Use(appMiddleware.RequireValidUUID("id"))

				// Streaming
				r.Get("/tracks/{id}/stream", streamingHandler.StreamTrack)
				r.Post("/tracks/{id}/play", streamingHandler.RecordPlayProgress)

				// Social
				r.Post("/artists/{id}/follow", socialHandler.FollowArtist)
				r.Delete("/artists/{id}/follow", socialHandler.UnfollowArtist)
				r.Post("/tracks/{id}/like", socialHandler.LikeTrack)
				r.Delete("/tracks/{id}/like", socialHandler.UnlikeTrack)
				r.Get("/tracks/{id}/share", socialHandler.ShareTrack)

				// Comments (write)
				r.Post("/tracks/{id}/comments", socialHandler.AddComment)
				r.Delete("/tracks/{id}/comments/{commentId}", socialHandler.DeleteComment)

				// Playlists
				r.Put("/playlists/{id}", socialHandler.UpdatePlaylist)
				r.Post("/playlists/{id}/tracks", socialHandler.AddTrackToPlaylist)

				// Radio
				r.Get("/tracks/{id}/radio", recoHandler.GetRadio)
			})

			// Me (no UUID params)
			r.Get("/me/playlists", socialHandler.GetMyPlaylists)
			r.Get("/me/likes", socialHandler.GetMyLikes)
			r.Get("/me/history", socialHandler.GetHistory)
			r.Get("/me/messages", socialHandler.GetMyMessages)
			r.Get("/me/recommendations", recoHandler.GetRecommendations)

			// Playlists without UUID (create)
			r.Post("/playlists", socialHandler.CreatePlaylist)

			// ── Artist-only routes ───────────────────────────────────────────────
			r.Group(func(r chi.Router) {
				r.Use(appMiddleware.RequireRole(auth.RoleArtist, auth.RoleAdmin))
				r.Use(appMiddleware.ArtistContext())

				// Profile
				r.Get("/artists/me", artistHandler.GetMe)
				r.Put("/artists/me", artistHandler.UpdateMe)

				// Tracks (no UUID)
				r.Post("/artists/me/tracks", musicHandler.UploadTrack)
				r.Get("/artists/me/tracks", musicHandler.ListMyTracks)

				// Albums (no UUID)
				r.Post("/artists/me/albums", musicHandler.CreateAlbum)

				// Analytics
				r.Get("/artists/me/analytics/overview", analyticsHandler.Overview)
				r.Get("/artists/me/analytics/tracks", analyticsHandler.TopTracks)
				r.Get("/artists/me/analytics/trends", analyticsHandler.Trends)
				r.Get("/artists/me/analytics/revenue", analyticsHandler.Revenue)

				// Messaging
				r.Post("/artists/me/messages", analyticsHandler.BroadcastMessage)

				// Routes with UUID params
				r.Group(func(r chi.Router) {
					r.Use(appMiddleware.RequireValidUUID("id"))

					// Tracks
					r.Put("/artists/me/tracks/{id}", musicHandler.UpdateTrack)
					r.Delete("/artists/me/tracks/{id}", musicHandler.DeleteTrack)
					r.Put("/artists/me/tracks/{id}/schedule", musicHandler.ScheduleTrack)
					r.Post("/artists/me/tracks/{id}/collaborators", musicHandler.AddCollaborator)
					r.Delete("/artists/me/tracks/{id}/collaborators/{artistId}", musicHandler.RemoveCollaborator)

					// Albums
					r.Post("/artists/me/albums/{id}/tracks", musicHandler.AddTrackToAlbum)
					r.Delete("/artists/me/albums/{id}/tracks/{trackId}", musicHandler.RemoveTrackFromAlbum)
				})
			})
		})
	})

	// ── Server ───────────────────────────────────────────────────────────────
	srv := &http.Server{
		Addr:         fmt.Sprintf(":%s", cfg.Port),
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		logger.Info("zedstream api starting", "port", cfg.Port, "env", cfg.AppEnv)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	<-quit
	logger.Info("shutting down server...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("server forced to shutdown", "error", err)
	}

	bgCancel()
	logger.Info("server stopped")
}
