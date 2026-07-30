package importer

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jaecopzm/zedstream/pkg/id"
	"github.com/jaecopzm/zedstream/pkg/storage"
)

var spotifyURLRegexp = regexp.MustCompile(`open\.spotify\.com/(track|album|playlist)/([A-Za-z0-9]+)`)

type spotifyImage struct {
	URL string `json:"url"`
}

type ImportOptions struct {
	GenreID         *string
	OverrideArtist  string
	OverrideTitle   string
	FeaturedArtists string // comma-separated additional names to link as collaborators
	Publish         bool   // if false, tracks are created as draft
	Section         string
	Description     string
}

type Importer struct {
	db      *pgxpool.Pool
	spotify *spotifyClient
	store   *storage.Client
	audioBucket string
	imageBucket string
}

func New(db *pgxpool.Pool, spotifyID, spotifySecret string, store *storage.Client, audioBucket, imageBucket string) *Importer {
	return &Importer{
		db:      db,
		spotify: newSpotifyClient(spotifyID, spotifySecret),
		store:   store,
		audioBucket: audioBucket,
		imageBucket: imageBucket,
	}
}

// NewWithOwnPool creates an Importer with its own dedicated DB pool (max 3 conns)
// so bulk imports don't compete with the web API's connection pool.
func NewWithOwnPool(ctx context.Context, dsn, spotifyID, spotifySecret string, store *storage.Client, audioBucket, imageBucket string) (*Importer, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse import db config: %w", err)
	}
	cfg.MaxConns = 3
	cfg.MinConns = 1
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.MaxConnIdleTime = 5 * time.Minute
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create import pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping import pool: %w", err)
	}
	return &Importer{
		db:      pool,
		spotify: newSpotifyClient(spotifyID, spotifySecret),
		store:   store,
		audioBucket: audioBucket,
		imageBucket: imageBucket,
	}, nil
}

// ── Title / featured-artist parsing ────────────────────────────────────────

var featRegexp = regexp.MustCompile(`(?i)[(【\[{]?\s*(?:feat\.?|ft\.?|featuring)\s+([^)】\]}]+)[)】\]}]?`)

type parsedTitle struct {
	Clean  string
	FeatNames []string
}

func parseFeat(rawTitle string) parsedTitle {
	result := parsedTitle{Clean: rawTitle}

	m := featRegexp.FindStringSubmatch(rawTitle)
	if m == nil {
		return result
	}

	result.Clean = featRegexp.ReplaceAllString(rawTitle, "")
	result.Clean = strings.TrimSpace(result.Clean)
	result.Clean = strings.TrimSuffix(result.Clean, "-")
	result.Clean = strings.TrimSpace(result.Clean)

	raw := m[1]
	for _, sep := range []string{" & ", " and ", ","} {
		parts := strings.Split(raw, sep)
		if len(parts) > 1 {
			for _, p := range parts {
				p = strings.TrimSpace(p)
				if p != "" {
					result.FeatNames = append(result.FeatNames, p)
				}
			}
			return result
		}
	}
	result.FeatNames = append(result.FeatNames, strings.TrimSpace(raw))
	return result
}

// ── Public API ─────────────────────────────────────────────────────────────

func (imp *Importer) ImportURL(ctx context.Context, rawURL string) error {
	m := spotifyURLRegexp.FindStringSubmatch(rawURL)
	if m == nil {
		return fmt.Errorf("not a valid Spotify URL: %s", rawURL)
	}
	typ, id := m[1], m[2]

	switch typ {
	case "track":
		return imp.importTrack(ctx, id, ImportOptions{})
	case "album":
		return imp.importAlbum(ctx, id)
	case "playlist":
		return imp.importPlaylist(ctx, id)
	default:
		return fmt.Errorf("unsupported Spotify resource type: %s", typ)
	}
}

func (imp *Importer) ImportTrackSearch(ctx context.Context, query string) error {
	tracks, err := imp.spotify.SearchTracks(query, 1)
	if err != nil {
		return fmt.Errorf("search failed: %w", err)
	}
	if len(tracks) == 0 {
		return fmt.Errorf("no track found for query: %s", query)
	}
	return imp.importSpotifyTrack(ctx, &tracks[0], ImportOptions{})
}

func (imp *Importer) ImportTrackWithOptions(ctx context.Context, rawURL string, opts ImportOptions) error {
	m := spotifyURLRegexp.FindStringSubmatch(rawURL)
	if m != nil {
		// It's a full Spotify URL — extract the track ID
		return imp.importTrack(ctx, m[2], opts)
	}
	// Check if it looks like a bare Spotify ID (22 alphanumeric chars)
	if isSpotifyID(rawURL) {
		return imp.importTrack(ctx, rawURL, opts)
	}
	// Fall back to search by text query
	tracks, err := imp.spotify.SearchTracks(rawURL, 1)
	if err != nil {
		return fmt.Errorf("no track found: %w", err)
	}
	if len(tracks) == 0 {
		return fmt.Errorf("no track found for: %s", rawURL)
	}
	return imp.importSpotifyTrack(ctx, &tracks[0], opts)
}

func isSpotifyID(s string) bool {
	if len(s) != 22 {
		return false
	}
	for _, c := range s {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
			return false
		}
	}
	return true
}

func (imp *Importer) ImportSearchWithOptions(ctx context.Context, query string, opts ImportOptions) error {
	tracks, err := imp.spotify.SearchTracks(query, 1)
	if err != nil {
		return fmt.Errorf("search failed: %w", err)
	}
	if len(tracks) == 0 {
		return fmt.Errorf("no track found for: %s", query)
	}
	return imp.importSpotifyTrack(ctx, &tracks[0], opts)
}

// ── Internal ───────────────────────────────────────────────────────────────

func (imp *Importer) importTrack(ctx context.Context, spotifyID string, opts ImportOptions) error {
	st, err := imp.spotify.FetchTrack(spotifyID)
	if err != nil {
		return fmt.Errorf("fetch track: %w", err)
	}
	return imp.importSpotifyTrack(ctx, st, opts)
}

func (imp *Importer) importAlbum(ctx context.Context, spotifyID string) error {
	album, err := imp.spotify.FetchAlbum(spotifyID)
	if err != nil {
		return fmt.Errorf("fetch album: %w", err)
	}

	for i := range album.Tracks.Items {
		t := &album.Tracks.Items[i]
		t.Album.ID = album.ID
		t.Album.Name = album.Name
		t.Album.Images = album.Images
		t.Album.AlbumType = album.AlbumType
		t.Album.ReleaseDate = album.ReleaseDate
		if len(t.Artists) == 0 {
			t.Artists = album.Artists
		}
	}

	log.Printf("Importing album %q (%d tracks)...", album.Name, len(album.Tracks.Items))
	for i, t := range album.Tracks.Items {
		cp := t
		if err := imp.importSpotifyTrack(ctx, &cp, ImportOptions{}); err != nil {
			return fmt.Errorf("track %d (%s): %w", i+1, t.Name, err)
		}
	}
	return nil
}

func (imp *Importer) importPlaylist(ctx context.Context, spotifyID string) error {
	playlist, err := imp.spotify.FetchPlaylist(spotifyID)
	if err != nil {
		return fmt.Errorf("fetch playlist: %w", err)
	}

	items := playlist.Tracks.Items
	log.Printf("Importing playlist %q (%d tracks)...", playlist.Name, len(items))
	for i, item := range items {
		if item.Track.ID == "" {
			continue
		}
		cp := item.Track
		if err := imp.importSpotifyTrack(ctx, &cp, ImportOptions{}); err != nil {
			return fmt.Errorf("track %d (%s): %w", i+1, item.Track.Name, err)
		}
	}
	return nil
}

func (imp *Importer) importSpotifyTrack(ctx context.Context, st *SpotifyTrack, opts ImportOptions) error {
	artistName := st.Artists[0].Name
	isrc := st.ExternalIDs.ISRC
	durationSec := st.DurationMs / 1000

	parsed := parseFeat(st.Name)
	cleanTitle := parsed.Clean
	featNames := parsed.FeatNames

	title := cleanTitle
	if opts.OverrideTitle != "" {
		title = opts.OverrideTitle
	}
	if opts.OverrideArtist != "" {
		artistName = opts.OverrideArtist
	}

	log.Printf("→ %s - %s (ISRC: %s, %ds)", artistName, title, isrc, durationSec)

	artistSpotifyID := st.Artists[0].ID
	artistID, err := imp.findOrCreateArtist(ctx, artistName, artistSpotifyID)
	if err != nil {
		return fmt.Errorf("artist: %w", err)
	}

	var albumID *string
	var albumCoverURL string
	// Only create an album record for actual albums/EPs.
	// Spotify wraps every single in a "single"-type album with the same name as the track — skip those.
	isRealAlbum := st.Album.ID != "" && st.Album.Name != "" &&
		st.Album.AlbumType != "single" &&
		!strings.EqualFold(st.Album.Name, title)
	if isRealAlbum {
		aid, acov, err := imp.findOrCreateAlbum(ctx, artistID, st.Album.Name, st.Album.Images, st.Album.AlbumType)
		if err != nil {
			return fmt.Errorf("album: %w", err)
		}
		albumID = &aid
		albumCoverURL = acov
	}

	exists, err := imp.trackExists(ctx, artistID, title)
	if err != nil {
		return fmt.Errorf("check track: %w", err)
	}
	if exists {
		log.Printf("  ⏭ Skipping (already imported)")
		return nil
	}

	// Reuse the album cover for the track — avoids a duplicate upload
	coverURL := albumCoverURL
	if coverURL == "" {
		coverURL, err = imp.downloadAndUploadCover(ctx, st.Album.Images, artistID)
		if err != nil {
			log.Printf("  ⚠ Cover: %v", err)
		}
	}

	status := "published"
	if !opts.Publish {
		status = "draft"
	}

	description := opts.Description
	if description == "" {
		description = st.Album.Name
	}

	var trackID string
	if isrc == "" {
		trackID, err = imp.createTrackRecord(ctx, artistID, albumID, title, durationSec, coverURL, "", 0, "audio/mpeg", opts.GenreID, status, opts.Section, description)
		if err != nil {
			return err
		}
	} else {
		tmpDir, err := os.MkdirTemp("", "zedstream-import-*")
		if err != nil {
			return fmt.Errorf("temp dir: %w", err)
		}
		defer os.RemoveAll(tmpDir)

		audioPath, err := downloadAudio(isrc, tmpDir)
		if err != nil {
			return fmt.Errorf("download audio: %w", err)
		}

		audioKey, fileSize, mimeType, err := imp.uploadAudioToR2(ctx, audioPath, artistID, title)
		if err != nil {
			return fmt.Errorf("upload audio: %w", err)
		}

		trackID, err = imp.createTrackRecord(ctx, artistID, albumID, title, durationSec, coverURL, audioKey, fileSize, mimeType, opts.GenreID, status, opts.Section, description)
		if err != nil {
			return err
		}
	}

	// Add featured artists from Spotify data (e.g. from title feat. or extra artists)
	for _, featName := range featNames {
		if strings.EqualFold(featName, artistName) || strings.EqualFold(featName, strings.Split(artistName, " & ")[0]) {
			continue
		}
		featID, err := imp.findOrCreateArtist(ctx, featName, "")
		if err != nil {
			log.Printf("  ⚠ Could not create featured artist %q: %v", featName, err)
			continue
		}
		imp.addCollaborator(ctx, trackID, featID, featName)
	}

	// Add manually specified featured artists from opts
	if opts.FeaturedArtists != "" {
		for _, name := range strings.Split(opts.FeaturedArtists, ",") {
			name = strings.TrimSpace(name)
			if name == "" || strings.EqualFold(name, artistName) {
				continue
			}
			featID, err := imp.findOrCreateArtist(ctx, name, "")
			if err != nil {
				log.Printf("  ⚠ Could not create featured artist %q: %v", name, err)
				continue
			}
			imp.addCollaborator(ctx, trackID, featID, name)
		}
	}

	return nil
}

func (imp *Importer) addCollaborator(ctx context.Context, trackID, artistID, name string) {
	_, err := imp.db.Exec(ctx,
		`INSERT INTO track_collaborators (track_id, artist_id, role) VALUES ($1, $2, 'featured')
		 ON CONFLICT (track_id, artist_id) DO NOTHING`, trackID, artistID)
	if err != nil {
		log.Printf("  ⚠ Could not add collaborator %q: %v", name, err)
	} else {
		log.Printf("  ✓ Featured artist: %s", name)
	}
}

func (imp *Importer) findOrCreateArtist(ctx context.Context, name, spotifyArtistID string) (string, error) {
	var artistID string
	var existingPhoto *string
	err := imp.db.QueryRow(ctx, `SELECT id, photo_url FROM artists WHERE stage_name = $1`, name).Scan(&artistID, &existingPhoto)
	if err == nil {
		// Artist exists but has no photo — try to fetch one
		if existingPhoto == nil {
			imp.tryFetchArtistPhoto(ctx, name, spotifyArtistID, artistID)
		}
		return artistID, nil
	}

	// Fetch artist photo from Spotify
	artistImages := imp.fetchArtistImages(ctx, name, spotifyArtistID)
	photoURL := imp.uploadArtistPhoto(ctx, artistImages, name)

	tx, err := imp.db.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	slug := strings.ToLower(strings.ReplaceAll(name, " ", "-"))
	slug = regexp.MustCompile(`[^a-z0-9-]`).ReplaceAllString(slug, "")
	email := slug + "@imported.zedbeatz"
	providerID := "import:" + slug

	var userID string
	err = tx.QueryRow(ctx, `
		INSERT INTO users (id, email, name, role, provider, provider_id)
		VALUES ($1, $2, $3, 'artist', 'google', $4)
		RETURNING id
	`, id.New(), email, name, providerID).Scan(&userID)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") {
			_ = tx.QueryRow(ctx, `SELECT id FROM users WHERE email = $1`, email).Scan(&userID)
		} else {
			return "", fmt.Errorf("create user: %w", err)
		}
	}

	err = tx.QueryRow(ctx, `
		INSERT INTO artists (id, user_id, stage_name, photo_url, verified)
		VALUES ($1, $2, $3, $4, true)
		RETURNING id
	`, id.New(), userID, name, photoURL).Scan(&artistID)
	if err != nil {
		return "", fmt.Errorf("create artist: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit artist: %w", err)
	}

	log.Printf("  ✓ Created artist: %s (id: %s)", name, artistID)
	return artistID, nil
}

func (imp *Importer) fetchArtistImages(ctx context.Context, name, spotifyArtistID string) []spotifyImage {
	var artistImages []spotifyImage
	if spotifyArtistID != "" {
		if sa, err := imp.spotify.FetchArtist(spotifyArtistID); err == nil {
			artistImages = sa.Images
		} else {
			log.Printf("  ⚠ Could not fetch Spotify artist %q: %v", name, err)
		}
	} else {
		// No Spotify ID — try searching by name
		if sa, err := imp.spotify.SearchArtist(name); err == nil {
			artistImages = sa.Images
		}
	}
	return artistImages
}

func (imp *Importer) uploadArtistPhoto(ctx context.Context, images []spotifyImage, name string) *string {
	if len(images) == 0 {
		return nil
	}
	url, err := imp.downloadAndUploadArtistPhoto(ctx, images, name)
	if err != nil {
		log.Printf("  ⚠ Artist photo: %v", err)
		return nil
	}
	if url == "" {
		return nil
	}
	return &url
}

func (imp *Importer) tryFetchArtistPhoto(ctx context.Context, name, spotifyArtistID, artistID string) {
	images := imp.fetchArtistImages(ctx, name, spotifyArtistID)
	photoURL := imp.uploadArtistPhoto(ctx, images, name)
	if photoURL != nil {
		imp.db.Exec(ctx, `UPDATE artists SET photo_url = $1, updated_at = NOW() WHERE id = $2`, *photoURL, artistID)
	}
}

func (imp *Importer) findOrCreateAlbum(ctx context.Context, artistID, title string, images []spotifyImage, albumType string) (string, string, error) {
	var albumID string
	var existingCover *string
	err := imp.db.QueryRow(ctx, `SELECT id, cover_url FROM albums WHERE artist_id = $1 AND title = $2`, artistID, title).Scan(&albumID, &existingCover)
	if err == nil {
		coverURL := ""
		if existingCover != nil {
			coverURL = *existingCover
		}
		return albumID, coverURL, nil
	}

	albumTypeNorm := "album"
	switch albumType {
	case "single":
		albumTypeNorm = "single"
	case "ep":
		albumTypeNorm = "ep"
	case "compilation":
		albumTypeNorm = "album"
	}

	var coverURL *string
	if len(images) > 0 {
		cv, err := imp.downloadAndUploadCover(ctx, images, artistID)
		if err == nil && cv != "" {
			coverURL = &cv
		}
	}

	albumID = id.New()
	_, err = imp.db.Exec(ctx, `
		INSERT INTO albums (id, artist_id, title, cover_url, type, status, released_at)
		VALUES ($1, $2, $3, $4, $5, 'published', NOW())
	`, albumID, artistID, title, coverURL, albumTypeNorm)
	if err != nil {
		return "", "", fmt.Errorf("create album: %w", err)
	}

	log.Printf("  ✓ Created album: %s", title)
	cv := ""
	if coverURL != nil {
		cv = *coverURL
	}
	return albumID, cv, nil
}

func (imp *Importer) trackExists(ctx context.Context, artistID, title string) (bool, error) {
	var exists bool
	err := imp.db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM tracks WHERE artist_id = $1 AND LOWER(title) = LOWER($2))`,
		artistID, title,
	).Scan(&exists)
	return exists, err
}

func (imp *Importer) downloadAndUploadArtistPhoto(ctx context.Context, images []spotifyImage, artistName string) (string, error) {
	if len(images) == 0 {
		return "", nil
	}
	imageURL := images[0].URL

	tmpDir, err := os.MkdirTemp("", "zedstream-artist-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmpDir)

	photoPath, err := downloadCover(imageURL, tmpDir)
	if err != nil {
		return "", err
	}

	slug := strings.ToLower(strings.ReplaceAll(artistName, " ", "-"))
	slug = regexp.MustCompile(`[^a-z0-9-]`).ReplaceAllString(slug, "")
	key := fmt.Sprintf("artists/%s/photo_%s", slug, time.Now().Format("20060102150405"))

	f, err := os.Open(photoPath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	if err := imp.store.UploadFile(ctx, imp.imageBucket, key, "image/jpeg", f); err != nil {
		return "", fmt.Errorf("upload artist photo: %w", err)
	}

	photoURL := imp.store.PublicURL(key)
	log.Printf("  ✓ Artist photo uploaded: %s", photoURL)
	return photoURL, nil
}

func (imp *Importer) downloadAndUploadCover(ctx context.Context, images []spotifyImage, artistID string) (string, error) {
	if len(images) == 0 {
		return "", nil
	}
	imageURL := images[0].URL

	tmpDir, err := os.MkdirTemp("", "zedstream-cover-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmpDir)

	coverPath, err := downloadCover(imageURL, tmpDir)
	if err != nil {
		return "", err
	}

	key := fmt.Sprintf("covers/%s/cover_%s", artistID, time.Now().Format("20060102150405"))
	f, err := os.Open(coverPath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	if err := imp.store.UploadFile(ctx, imp.imageBucket, key, "image/jpeg", f); err != nil {
		return "", fmt.Errorf("upload cover: %w", err)
	}

	coverURL := imp.store.PublicURL(key)
	log.Printf("  ✓ Cover uploaded: %s", coverURL)
	return coverURL, nil
}

func (imp *Importer) uploadAudioToR2(ctx context.Context, filePath, artistID, title string) (string, int64, string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", 0, "", fmt.Errorf("open audio: %w", err)
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return "", 0, "", fmt.Errorf("stat audio: %w", err)
	}

	mimeType := "audio/mpeg"
	if strings.HasSuffix(filePath, ".flac") {
		mimeType = "audio/flac"
	} else if strings.HasSuffix(filePath, ".wav") {
		mimeType = "audio/wav"
	} else if strings.HasSuffix(filePath, ".ogg") {
		mimeType = "audio/ogg"
	} else if strings.HasSuffix(filePath, ".m4a") {
		mimeType = "audio/mp4"
	}

	slug := strings.ToLower(strings.ReplaceAll(title, " ", "-"))
	slug = regexp.MustCompile(`[^a-z0-9-]`).ReplaceAllString(slug, "")
	key := fmt.Sprintf("tracks/%s/%s_%d%s", artistID, slug, time.Now().Unix(), filepath.Ext(filePath))
	if err := imp.store.UploadFile(ctx, imp.audioBucket, key, mimeType, f); err != nil {
		return "", 0, "", fmt.Errorf("upload audio: %w", err)
	}

	log.Printf("  ✓ Audio uploaded (%.1f MB)", float64(stat.Size())/1024/1024)
	return key, stat.Size(), mimeType, nil
}

func (imp *Importer) createTrackRecord(ctx context.Context, artistID string, albumID *string, title string, durationSec int, coverURL, audioKey string, fileSize int64, mimeType string, genreID *string, status string, section string, description string) (string, error) {
	resolvedGenreID := genreID
	if genreID != nil && *genreID != "" {
		var foundID *string
		var name string
		err := imp.db.QueryRow(ctx,
			`SELECT id, name FROM genres WHERE id = $1 OR slug = $1`, *genreID,
		).Scan(&foundID, &name)
		if err != nil {
			log.Printf("  ⚠ Genre %q not found, skipping", *genreID)
			resolvedGenreID = nil
		} else {
			resolvedGenreID = foundID
		}
	}

	trackID := id.New()
	var desc *string
	if description != "" {
		desc = &description
	}
	_, err := imp.db.Exec(ctx, `
		INSERT INTO tracks (id, artist_id, album_id, title, duration_sec, cover_url, audio_key, file_size, mime_type, status, hls_status, released_at, genre_id, section, description)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'pending', NOW(), $11, $12, $13)
	`, trackID, artistID, albumID, title, durationSec, coverURL, audioKey, fileSize, mimeType, status, resolvedGenreID, section, desc)
	if err != nil {
		return "", fmt.Errorf("create track: %w", err)
	}

	parts := []string{title}
	if albumID != nil {
		parts = append(parts, "in album "+*albumID)
	}
	if resolvedGenreID != nil {
		parts = append(parts, "genre "+*resolvedGenreID)
	}
	log.Printf("  ✓ Track created: %s", strings.Join(parts, " "))
	return trackID, nil
}

// ClearAllData removes all data (social, tracks, albums, artists, artist users, imported users).
func (imp *Importer) ClearAllData(ctx context.Context) error {
	log.Print("Clearing all data...")
	tables := []string{
		"play_events", "likes", "track_collaborators", "album_collaborators",
		"track_comments", "playlist_tracks", "playlists", "follows", "messages",
		"message_recipients", "artist_claims", "refresh_tokens",
	}
	for _, t := range tables {
		if _, err := imp.db.Exec(ctx, "DELETE FROM "+t); err != nil {
			return fmt.Errorf("clear %s: %w", t, err)
		}
	}
	if _, err := imp.db.Exec(ctx, "DELETE FROM tracks"); err != nil {
		return fmt.Errorf("clear tracks: %w", err)
	}
	if _, err := imp.db.Exec(ctx, "DELETE FROM albums"); err != nil {
		return fmt.Errorf("clear albums: %w", err)
	}
	if _, err := imp.db.Exec(ctx, "DELETE FROM artists"); err != nil {
		return fmt.Errorf("clear artists: %w", err)
	}
	if _, err := imp.db.Exec(ctx, "DELETE FROM users WHERE role = 'artist'"); err != nil {
		return fmt.Errorf("clear artist users: %w", err)
	}
	log.Print("✓ All data cleared")
	return nil
}
