// @title ZedBeatz API
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
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/joho/godotenv"

	"github.com/jaecopzm/zedstream/internal/admin"
	"github.com/jaecopzm/zedstream/internal/analytics"
	"github.com/jaecopzm/zedstream/internal/artist"
	"github.com/jaecopzm/zedstream/internal/artistclaim"
	"github.com/jaecopzm/zedstream/internal/auth"
	"github.com/jaecopzm/zedstream/internal/credits"
	"github.com/jaecopzm/zedstream/internal/importer"
	"github.com/jaecopzm/zedstream/internal/payments"
	"github.com/jaecopzm/zedstream/internal/music"
	"github.com/jaecopzm/zedstream/internal/radio"
	"github.com/jaecopzm/zedstream/internal/recommendations"
	"github.com/jaecopzm/zedstream/internal/scheduler"
	"github.com/jaecopzm/zedstream/internal/social"
	"github.com/jaecopzm/zedstream/internal/streaming"
	"github.com/jaecopzm/zedstream/pkg/config"
	"github.com/jaecopzm/zedstream/pkg/database"
	appMiddleware "github.com/jaecopzm/zedstream/pkg/middleware"
	"github.com/jaecopzm/zedstream/pkg/response"
	"github.com/jaecopzm/zedstream/pkg/search"
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
		PublicURLAudio:  cfg.R2PublicURLAudio,
		PublicURLImages: cfg.R2PublicURLImages,
	})
	if err != nil {
		logger.Error("failed to init storage client", "error", err)
		os.Exit(1)
	}
	logger.Info("storage client initialized")

	// ── Search ───────────────────────────────────────────────────────────────
	// TODO: Move to config
	searchClient := search.NewClient("http://localhost:7700", "")
	if err := searchClient.SetupIndexes(ctx); err != nil {
		logger.Error("failed to setup search indexes", "error", err)
	} else {
		logger.Info("search client initialized")
	}

	// ── Services & Handlers ──────────────────────────────────────────────────
	authSvc := auth.NewService(db, cfg.JWTSecret, cfg.JWTAccessTTLMins, cfg.JWTRefreshTTLDays)

	oauthSvc := auth.NewOAuthService(
		db, authSvc,
		cfg.GoogleClientID, cfg.GoogleClientSecret, cfg.GoogleRedirectURL,
		cfg.FacebookClientID, cfg.FacebookClientSecret, cfg.FacebookRedirectURL,
	)

	frontendURLs := strings.Split(cfg.FrontendURL, ",")
	// Allow dev origins like ngrok URLs without hardcoding
	if devOrigin := os.Getenv("ALLOWED_DEV_ORIGIN"); devOrigin != "" {
		for _, o := range strings.Split(devOrigin, ",") {
			o = strings.TrimSpace(o)
			if o != "" {
				frontendURLs = append(frontendURLs, o)
			}
		}
	}
	// Always allow localhost dev variants
	for _, fallback := range []string{"http://localhost:3000", "http://localhost:8080"} {
		has := false
		for _, u := range frontendURLs {
			if u == fallback {
				has = true
				break
			}
		}
		if !has {
			frontendURLs = append(frontendURLs, fallback)
		}
	}
	authHandler  := auth.NewHandler(authSvc, oauthSvc, frontendURLs...)
	creditsRepo        := credits.NewRepository(db)
	payClient          := payments.NewClient(cfg.MoneyUnifyAuthID, cfg.MoneyUnifyWebhookKey)
	creditsHandler     := credits.NewHandler(creditsRepo, payClient, cfg.FrontendURL)
	artistRepo        := artist.NewRepository(db)
	artistHandler     := artist.NewHandler(artistRepo, store, cfg.R2BucketImages, creditsRepo)
	musicRepo         := music.NewRepository(db)
	musicHandler      := music.NewHandler(musicRepo, store, searchClient, cfg.R2BucketAudio, cfg.R2BucketImages, creditsRepo)
	streamingHandler  := streaming.NewHandler(db, store, cfg.R2BucketAudio)
	socialHandler     := social.NewHandler(db, cfg.BaseURL)
	analyticsHandler  := analytics.NewHandler(db)
	recoHandler       := recommendations.NewHandler(db)
	radioHandler      := radio.NewHandler(db, store, cfg.R2BucketImages)

	// Artist claim verification
	claimRepo    := artistclaim.NewRepository(db)
	claimSvc     := artistclaim.NewService(claimRepo, store, cfg.R2BucketImages)
	claimHandler := artistclaim.NewHandler(claimSvc, authSvc)

	// Admin content management
	adminHandler := admin.NewHandler(musicRepo, artistRepo, store, db, cfg.R2BucketAudio, cfg.R2BucketImages)

	// Spotify importer (loads credentials from ~/projects/agent/.env or env vars)
	homeDir, _ := os.UserHomeDir()
	_ = godotenv.Load(homeDir + "/projects/agent/.env")
	importHandler := importer.NewHandler(func() *importer.Importer {
		imp, err := importer.NewWithOwnPool(context.Background(),
			cfg.DatabaseURL,
			os.Getenv("SPOTIFY_CLIENT_ID"),
			os.Getenv("SPOTIFY_CLIENT_SECRET"),
			store, cfg.R2BucketAudio, cfg.R2BucketImages,
		)
		if err != nil {
			fmt.Fprintf(os.Stderr, "import pool: %v\n", err)
			os.Exit(1)
		}
		return imp
	}())

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
	r.Use(appMiddleware.SecurityHeaders)
	r.Use(appMiddleware.RateLimit(
		cfg.RateLimitRequests,
		time.Duration(cfg.RateLimitWindowSeconds)*time.Second,
	))
	corsOpts := cors.Options{
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-Request-Id"},
		AllowCredentials: true,
		MaxAge:           300,
	}
	if cfg.AppEnv == "development" {
		corsOpts.AllowedOrigins = []string{"*"}
	} else {
		corsOpts.AllowedOrigins = frontendURLs
	}
	r.Use(cors.Handler(corsOpts))

	// Health check
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		response.OK(w, map[string]string{"status": "ok", "service": "zedbeatz"})
	})

	// Auth — mounted at root so OAuth redirects match
	r.Get("/auth/google", authHandler.GoogleLogin)
	r.Get("/auth/google/callback", authHandler.GoogleCallback)
	r.Get("/auth/facebook", authHandler.FacebookLogin)
	r.Get("/auth/facebook/callback", authHandler.FacebookCallback)
	r.Post("/auth/refresh", authHandler.RefreshToken)
	r.Post("/auth/logout", authHandler.Logout)
	if cfg.AppEnv != "production" {
		r.Post("/auth/dev-login", authHandler.DevLogin)
	}
	r.Post("/auth/register", authHandler.Register)
	r.Get("/auth/login", authHandler.LoginPage)
	r.Post("/auth/login", authHandler.Login)

	// ── Public routes ─────────────────────────────────────────────────────────
	r.Route("/api/v1", func(r chi.Router) {
		r.Use(appMiddleware.LimitBody(100 << 20)) // 100 MB limit (audio uploads can be large)

		// Webhooks (no auth — verified by signature if key is configured)
		r.Post("/webhooks/moneyunify", creditsHandler.WebhookHandler)

		// Auth (mirrored for /api/v1 prefix)
		r.Get("/auth/google", authHandler.GoogleLogin)
		r.Get("/auth/google/callback", authHandler.GoogleCallback)
		r.Get("/auth/facebook", authHandler.FacebookLogin)
		r.Get("/auth/facebook/callback", authHandler.FacebookCallback)
		r.Post("/auth/refresh", authHandler.RefreshToken)
		r.Post("/auth/logout", authHandler.Logout)
		if cfg.AppEnv != "production" {
			r.Post("/auth/dev-login", authHandler.DevLogin)
		}
		r.Post("/auth/register", authHandler.Register)
		r.Get("/auth/login", authHandler.LoginPage)
		r.Post("/auth/login", authHandler.Login)

		// Public music browsing
		r.Get("/tracks", musicHandler.ListTracks)
		r.Get("/tracks/search", musicHandler.SearchTracks)
		r.Get("/albums/search", musicHandler.SearchAlbums)
		r.Get("/artists/check-name", artistHandler.CheckName)
		r.Get("/artists/search", artistHandler.Search)
		r.Get("/genres", musicHandler.ListGenres)

		// Public albums listing (no id param)
		r.Get("/albums", musicHandler.ListAlbums)
		r.Get("/artists/featured", artistHandler.ListFeatured)

		// Radio (public)
		r.Get("/radio/stations", radioHandler.ListStations)
		r.With(appMiddleware.TryAuthenticate(authSvc)).Get("/radio/personalized", radioHandler.GetPersonalized)

		// Routes with UUID {id} params
		r.Group(func(r chi.Router) {
			r.Use(appMiddleware.RequireValidID("id"))

			r.Get("/genres/{id}/tracks", musicHandler.GetTracksByGenre)
			r.Get("/albums/{id}", musicHandler.GetAlbum)
			r.Get("/tracks/{id}", musicHandler.GetTrack)
			r.With(appMiddleware.TryAuthenticate(authSvc)).Get("/artists/{id}", artistHandler.GetByID)
			r.Get("/artists/{id}/tracks", musicHandler.GetArtistTracks)
			r.Get("/artists/{id}/collaborations", musicHandler.GetCollaboratorTracks)
			r.Get("/artists/{id}/albums", musicHandler.GetArtistAlbums)
			r.Get("/tracks/{id}/comments", socialHandler.GetComments)
			r.Get("/artists/{id}/claim", claimHandler.GetClaimStatus)
			r.Get("/tracks/{id}/stream", streamingHandler.StreamTrack)
		})

		// Radio station detail — no UUID validation because genre stations use slug IDs
		r.Get("/radio/stations/{id}", radioHandler.GetStation)

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
				r.Use(appMiddleware.RequireValidID("id"))

				// Streaming (play progress still requires auth)
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

				// Artist claim
				r.Post("/artists/{id}/claim", claimHandler.InitiateClaim)
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

			// Credits
			r.Get("/artists/me/credits", creditsHandler.GetMyBalance)
			r.Get("/artists/me/credits/transactions", creditsHandler.ListMyTransactions)
			r.Post("/artists/me/credits/purchase", creditsHandler.InitiatePurchase)
			r.Post("/artists/me/credits/verify-payment", creditsHandler.VerifyPayment)

				// Messaging
				r.Post("/artists/me/messages", analyticsHandler.BroadcastMessage)
				r.Get("/artists/me/messages", analyticsHandler.ListSentMessages)

				// Routes with UUID params
				r.Group(func(r chi.Router) {
					r.Use(appMiddleware.RequireValidID("id"))

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
			}) // end artist-only group

			// ── Claim verification (any auth user can submit) ──────────────
			r.Group(func(r chi.Router) {
				r.Use(appMiddleware.RequireValidID("id"))

				r.Post("/claims/{id}/verify", claimHandler.SubmitVerification)
			})

			// ── Admin-only routes ─────────────────────────────────────────────
			r.Group(func(r chi.Router) {
				r.Use(appMiddleware.RequireRole(auth.RoleAdmin))

				// Claims
				r.Get("/admin/claims", claimHandler.ListClaims)
				r.Group(func(r chi.Router) {
					r.Use(appMiddleware.RequireValidID("id"))
					r.Post("/admin/claims/{id}/review", claimHandler.ReviewClaim)
				})

		// Content management
		r.Get("/admin/artists", adminHandler.ListArtists)
		r.Group(func(r chi.Router) {
			r.Use(appMiddleware.RequireValidID("id"))
			r.Patch("/admin/artists/{id}", adminHandler.UpdateArtist)
			r.Delete("/admin/artists/{id}", adminHandler.DeleteArtist)
		})
		r.Get("/admin/albums", adminHandler.ListAlbums)
		r.Post("/admin/albums", adminHandler.CreateAlbum)
		r.Group(func(r chi.Router) {
			r.Use(appMiddleware.RequireValidID("id"))
			r.Patch("/admin/albums/{id}", adminHandler.UpdateAlbum)
			r.Delete("/admin/albums/{id}", adminHandler.DeleteAlbum)
		})
			r.Post("/admin/albums", adminHandler.CreateAlbum)
			r.Get("/admin/albums", adminHandler.ListAlbums)
			r.Get("/admin/tracks", adminHandler.ListTracks)
			r.Post("/admin/tracks", adminHandler.UploadTrack)
			r.Group(func(r chi.Router) {
				r.Use(appMiddleware.RequireValidID("id"))
				r.Patch("/admin/tracks/{id}", adminHandler.UpdateTrack)
			})

			// Spotify import (admin tools)
			r.Get("/admin/import/search", importHandler.SearchTracks)
			r.Post("/admin/import/track", importHandler.ImportTrack)
			r.Post("/admin/import/bulk", importHandler.BulkImport)
			r.Post("/admin/import/bulk/stream", importHandler.BulkImportStream)
			r.Post("/admin/import/album", importHandler.ImportAlbum)
			r.Post("/admin/import/playlist", importHandler.ImportPlaylist)
			r.Post("/admin/import/ai-enrich", importHandler.AIEnrich)

			// Radio stations (admin)
			r.Get("/admin/radio", radioHandler.ListAllStations)
			r.Post("/admin/radio", radioHandler.CreateStation)
			r.Group(func(r chi.Router) {
				r.Use(appMiddleware.RequireValidID("id"))
				r.Patch("/admin/radio/{id}", radioHandler.UpdateStation)
				r.Delete("/admin/radio/{id}", radioHandler.DeleteStation)
				r.Put("/admin/radio/{id}/tracks", radioHandler.SetStationTracks)
				r.Post("/admin/radio/{id}/tracks", radioHandler.AddStationTrack)
				r.Delete("/admin/radio/{id}/tracks/{trackId}", radioHandler.RemoveStationTrack)
			})

			// Credits admin
			r.Post("/admin/credits/grant", creditsHandler.AdminGrant)
			r.Post("/admin/credits/revoke", creditsHandler.AdminRevoke)
			r.Group(func(r chi.Router) {
				r.Use(appMiddleware.RequireValidID("id"))
				r.Get("/admin/credits/{id}", creditsHandler.ArtistBalanceFromURL)
			})
			})
		})
	})

	// ── Server ───────────────────────────────────────────────────────────────
	srv := &http.Server{
		Addr:         fmt.Sprintf(":%s", cfg.Port),
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 10 * time.Minute, // bulk imports can take several minutes
		IdleTimeout:  60 * time.Second,
	}

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		logger.Info("zedbeatz api starting", "port", cfg.Port, "env", cfg.AppEnv)
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
