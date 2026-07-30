package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"
	"github.com/jaecopzm/zedstream/internal/importer"
	"github.com/jaecopzm/zedstream/pkg/config"
	"github.com/jaecopzm/zedstream/pkg/database"
	"github.com/jaecopzm/zedstream/pkg/storage"
)

func main() {
	clear := flag.Bool("clear", false, "Clear all existing data before import")
	url := flag.String("url", "", "Spotify URL to import (track/album/playlist)")
	search := flag.String("search", "", "Search Spotify and import best match")
	genreID := flag.String("genre", "", "Genre ID to assign (run SELECT id,name FROM genres)")
	overrideArtist := flag.String("artist", "", "Override the primary artist name")
	overrideTitle := flag.String("title", "", "Override the track title")
	agentEnv := flag.String("agent-env", os.Getenv("HOME")+"/projects/agent/.env", "Path to agent .env with Spotify credentials")
	flag.Parse()

	// Load ZedStream config
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Load Spotify credentials from agent .env
	_ = godotenv.Load(*agentEnv)
	spotifyID := os.Getenv("SPOTIFY_CLIENT_ID")
	spotifySecret := os.Getenv("SPOTIFY_CLIENT_SECRET")
	if spotifyID == "" || spotifySecret == "" {
		log.Fatalf("SPOTIFY_CLIENT_ID and SPOTIFY_CLIENT_SECRET must be set (check %s)", *agentEnv)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	db, err := database.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("Database connection: %v", err)
	}
	defer db.Close()

	store, err := storage.New(storage.Config{
		AccountID:       cfg.R2AccountID,
		AccessKeyID:     cfg.R2AccessKeyID,
		SecretAccessKey: cfg.R2SecretAccessKey,
		PublicURLAudio:  cfg.R2PublicURLAudio,
		PublicURLImages: cfg.R2PublicURLImages,
	})
	if err != nil {
		log.Fatalf("Storage client: %v", err)
	}

	imp := importer.New(db, spotifyID, spotifySecret, store, cfg.R2BucketAudio, cfg.R2BucketImages)

	if *clear {
		if err := imp.ClearAllData(ctx); err != nil {
			log.Fatalf("Clear data: %v", err)
		}
	}

	opts := importer.ImportOptions{
		GenreID:        nil,
		OverrideArtist: *overrideArtist,
		OverrideTitle:  *overrideTitle,
	}
	if *genreID != "" {
		opts.GenreID = genreID
	}

	imports := 0
	if *url != "" {
		if err := imp.ImportTrackWithOptions(ctx, *url, opts); err != nil {
			log.Printf("ERROR: %v", err)
			os.Exit(1)
		}
		imports++
	}

	if *search != "" {
		if err := imp.ImportSearchWithOptions(ctx, *search, opts); err != nil {
			log.Printf("ERROR: %v", err)
			os.Exit(1)
		}
		imports++
	}

	if flag.NArg() > 0 {
		for _, arg := range flag.Args() {
			if err := imp.ImportTrackWithOptions(ctx, arg, opts); err != nil {
				log.Printf("ERROR: %v", err)
				os.Exit(1)
			}
			imports++
		}
	}

	if imports == 0 && !*clear {
		fmt.Println(`Usage: go run cmd/importer/main.go [flags] [spotify URLs...]

Flags:
  -clear           Clear all existing data before importing
  -url <URL>       Import a single Spotify URL
  -search <q>      Search Spotify and import best match
  -genre <id>      Genre ID to assign (run: psql -c 'SELECT id,name FROM genres')
  -artist <name>   Override the primary artist name
  -title <name>    Override the track title
  -agent-env       Path to agent/.env with Spotify credentials (default: ~/projects/agent/.env)

Examples:
  go run cmd/importer/main.go -clear
  go run cmd/importer/main.go -url https://open.spotify.com/track/abc123
  go run cmd/importer/main.go -url https://open.spotify.com/album/xyz789
  go run cmd/importer/main.go -clear -url <album-url>
  go run cmd/importer/main.go -search "Yo Maps Aweah" -genre <genre_id>
  go run cmd/importer/main.go -search "Finally" -artist "Yo Maps" -title "Finally"
  go run cmd/importer/main.go <url1> <url2>`)
	}
}
