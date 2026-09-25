package downloader

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jaecopzm/zedstream/pkg/storage"
)

// Service orchestrates superfast downloads.
type Service struct {
	repo        *Repository
	db          *pgxpool.Pool
	storage     *storage.Client
	audioBucket string
	imageBucket string
	// worker pool
	sem     chan struct{}
	mu      sync.Mutex
	clients map[string]chan Job // jobID -> SSE clients
}

func NewService(db *pgxpool.Pool, store *storage.Client, audioBucket, imageBucket string, concurrency int) *Service {
	if concurrency <= 0 {
		concurrency = 4
	}
	return &Service{
		repo:        NewRepository(db),
		db:          db,
		storage:     store,
		audioBucket: audioBucket,
		imageBucket: imageBucket,
		sem:         make(chan struct{}, concurrency),
		clients:     make(map[string]chan Job),
	}
}

// yt-dlp progress regex: [download]  45.3% ...
var progressRe = regexp.MustCompile(`\[download\]\s+(\d+\.?\d*)%`)

type ytMetadata struct {
	Title      string `json:"title"`
	Artist     string `json:"artist"`
	Uploader   string `json:"uploader"`
	Duration   int    `json:"duration"`
	Thumbnail  string `json:"thumbnail"`
	ID         string `json:"id"`
	Extractor  string `json:"extractor"`
}

// Create enqueues a new job and starts processing async.
func (s *Service) Create(ctx context.Context, userID, url, quality string, title *string) (*Job, error) {
	if quality == "" {
		quality = "192"
	}
	// nano-ish id; use pgcrypto gen_random_uuid equivalent: time + random suffix
	id := newID()
	job := &Job{
		ID:       id,
		UserID:   userID,
		URL:      url,
		Title:    title,
		Quality:  quality,
		Status:   StatusQueued,
		Progress: 0,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := s.repo.Create(ctx, job); err != nil {
		return nil, err
	}
	go s.process(job.ID)
	return job, nil
}

func (s *Service) Get(ctx context.Context, id string) (*Job, error) {
	return s.repo.Get(ctx, id)
}

func (s *Service) List(ctx context.Context, userID string, limit, offset int) ([]*Job, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	return s.repo.ListByUser(ctx, userID, limit, offset)
}

func (s *Service) Delete(ctx context.Context, id, userID string) error {
	return s.repo.Delete(ctx, id, userID)
}

// Subscribe returns a channel that receives job updates.
func (s *Service) Subscribe(jobID string) (chan Job, func()) {
	ch := make(chan Job, 10)
	s.mu.Lock()
	s.clients[jobID] = ch
	s.mu.Unlock()
	return ch, func() {
		s.mu.Lock()
		delete(s.clients, jobID)
		close(ch)
		s.mu.Unlock()
	}
}

func (s *Service) broadcast(j *Job) {
	s.mu.Lock()
	ch, ok := s.clients[j.ID]
	s.mu.Unlock()
	if ok {
		select {
		case ch <- *j:
		default:
		}
	}
}

// process does the heavy lifting: yt-dlp -> ffmpeg -> R2 -> tracks row.
func (s *Service) process(jobID string) {
	s.sem <- struct{}{}
	defer func() { <-s.sem }()

	ctx := context.Background()
	job, err := s.repo.Get(ctx, jobID)
	if err != nil {
		slog.Error("downloader: get job failed", "id", jobID, "error", err)
		return
	}

	// Step 1: probe metadata with yt-dlp -j
	meta := s.probeMetadata(job.URL)
	if meta != nil {
		if job.Title == nil && meta.Title != "" {
			t := meta.Title
			job.Title = &t
		}
		if meta.Artist != "" || meta.Uploader != "" {
			a := meta.Artist
			if a == "" {
				a = meta.Uploader
			}
			job.ArtistName = &a
		}
		if meta.Duration > 0 {
			d := meta.Duration
			job.DurationSec = &d
		}
		if meta.Thumbnail != "" {
			c := meta.Thumbnail
			job.CoverURL = &c
		}
	}

	_ = s.repo.UpdateStatus(ctx, jobID, StatusDownloading, 5)
	job.Status = StatusDownloading
	job.Progress = 5
	s.broadcast(job)

	// Step 2: download to temp dir
	tmpDir, err := os.MkdirTemp("", "zed-dl-*")
	if err != nil {
		s.fail(jobID, err.Error())
		return
	}
	defer os.RemoveAll(tmpDir)

	// yt-dlp args for superfast mp3:
	// --concurrent-fragments 8 -> parallel DASH fragments (biggest speedup)
	// --extract-audio --audio-format mp3 --audio-quality <k>
	// --no-playlist --no-warnings
	outTemplate := filepath.Join(tmpDir, "%(title)s.%(ext)s")
	args := []string{
		"--extract-audio",
		"--audio-format", "mp3",
		"--audio-quality", qualityToYtDlp(job.Quality),
		"--concurrent-fragments", "8",
		"--no-playlist",
		"--no-warnings",
		"--newline", // progress per line
		"-o", outTemplate,
		job.URL,
	}

	cmd := exec.Command("yt-dlp", args...)
	// yt-dlp writes progress to stderr
	var stderr bytes.Buffer
	pr, pw := io.Pipe()
	cmd.Stdout = io.Discard
	cmd.Stderr = io.MultiWriter(&stderr, pw)

	if err := cmd.Start(); err != nil {
		// fallback error: yt-dlp not installed
		if strings.Contains(err.Error(), "executable file not found") {
			s.fail(jobID, "yt-dlp not installed on server. Install with: pip install yt-dlp && ensure ffmpeg is present")
			return
		}
		s.fail(jobID, err.Error())
		return
	}

	// Parse progress in background
	doneProgress := make(chan struct{})
	go func() {
		buf := make([]byte, 4096)
		partial := ""
		for {
			n, err := pr.Read(buf)
			if n > 0 {
				partial += string(buf[:n])
				lines := strings.Split(partial, "\n")
				partial = lines[len(lines)-1]
				for _, line := range lines[:len(lines)-1] {
					if m := progressRe.FindStringSubmatch(line); m != nil {
						if pct, err := strconv.ParseFloat(m[1], 64); err == nil {
							// map 0-100 download to 5-85 overall
							overall := 5 + int(pct*0.8)
							_ = s.repo.UpdateProgress(ctx, jobID, overall, StatusDownloading)
							job.Progress = overall
							s.broadcast(job)
						}
					}
				}
			}
			if err != nil {
				break
			}
		}
		close(doneProgress)
	}()

	err = cmd.Wait()
	pw.Close()
	<-doneProgress

	if err != nil {
		msg := stderr.String()
		if len(msg) > 800 {
			msg = msg[len(msg)-800:]
		}
		msg = strings.TrimSpace(msg)
		if msg == "" {
			msg = err.Error()
		}
		s.fail(jobID, msg)
		return
	}

	_ = s.repo.UpdateStatus(ctx, jobID, StatusUploading, 88)
	job.Status = StatusUploading
	job.Progress = 88
	s.broadcast(job)

	// Find downloaded mp3
	matches, _ := filepath.Glob(filepath.Join(tmpDir, "*.mp3"))
	if len(matches) == 0 {
		// maybe original ext was kept if conversion failed
		matches, _ = filepath.Glob(filepath.Join(tmpDir, "*.*"))
		if len(matches) == 0 {
			s.fail(jobID, "no output file produced by yt-dlp")
			return
		}
	}
	srcPath := matches[0]

	// Detect duration via ffprobe fallback if needed
	if job.DurationSec == nil {
		if d := probeDuration(srcPath); d > 0 {
			job.DurationSec = &d
		}
	}

	// Upload to R2
	file, err := os.Open(srcPath)
	if err != nil {
		s.fail(jobID, err.Error())
		return
	}
	defer file.Close()

	fi, _ := file.Stat()
	size := fi.Size()

	audioKey := fmt.Sprintf("downloader/%s/%s.mp3", job.UserID, jobID)
	if err := s.storage.UploadFile(ctx, s.audioBucket, audioKey, "audio/mpeg", file); err != nil {
		s.fail(jobID, "upload failed: "+err.Error())
		return
	}

	// Optionally create a track row for library integration (draft)
	var trackID *string
	if job.Title != nil {
		tid, err := s.createTrack(ctx, job, audioKey, size)
		if err == nil && tid != "" {
			trackID = &tid
		} else if err != nil {
			slog.Warn("downloader: create track failed", "error", err)
		}
	}

	dur := 0
	if job.DurationSec != nil {
		dur = *job.DurationSec
	}
	if err := s.repo.MarkCompleted(ctx, jobID, audioKey, size, dur, job.Title, job.ArtistName, job.CoverURL, trackID); err != nil {
		slog.Error("downloader: mark completed failed", "error", err)
	}

	job.Status = StatusCompleted
	job.Progress = 100
	job.AudioKey = &audioKey
	job.FileSize = &size
	job.TrackID = trackID
	job.CompletedAt = ptrTime(time.Now())
	s.broadcast(job)
	slog.Info("downloader: completed", "job", jobID, "key", audioKey, "size", size)
}

func (s *Service) fail(jobID, msg string) {
	_ = s.repo.MarkFailed(context.Background(), jobID, msg)
	if j, err := s.repo.Get(context.Background(), jobID); err == nil {
		s.broadcast(j)
	}
}

func (s *Service) probeMetadata(url string) *ytMetadata {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "yt-dlp", "-j", "--no-playlist", "--no-warnings", url)
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var m ytMetadata
	if err := json.Unmarshal(out, &m); err != nil {
		return nil
	}
	return &m
}

func probeDuration(path string) int {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", path)
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	f, _ := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	return int(f)
}

func (s *Service) createTrack(ctx context.Context, job *Job, audioKey string, fileSize int64) (string, error) {
	// Find artist_id for user; if user is artist, use it, else create track under a system artist?
	var artistID string
	err := s.db.QueryRow(ctx, `SELECT id FROM artists WHERE user_id=$1`, job.UserID).Scan(&artistID)
	if err != nil {
		// user is not an artist - don't auto-create track, just store job
		return "", nil
	}
	title := "Untitled"
	if job.Title != nil {
		title = *job.Title
	}
	dur := 0
	if job.DurationSec != nil {
		dur = *job.DurationSec
	}
	var genreID *string // leave null
	var coverURL *string = job.CoverURL
	var trackID string
	err = s.db.QueryRow(ctx, `
		INSERT INTO tracks (artist_id, title, duration_sec, genre_id, cover_url, audio_key, file_size, mime_type, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,'audio/mpeg','published')
		RETURNING id`, artistID, title, dur, genreID, coverURL, audioKey, fileSize).Scan(&trackID)
	return trackID, err
}

func qualityToYtDlp(q string) string {
	switch q {
	case "128":
		return "128K"
	case "192":
		return "192K"
	case "256":
		return "256K"
	case "320":
		return "0" // best
	default:
		return "192K"
	}
}

func newID() string {
	// short nanoid-like: 12 hex chars (good enough, matches existing 007 migration uses TEXT)
	b := make([]byte, 6)
	_, _ = RandRead(b)
	const chars = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	// simple fallback to time-based if crypto fails
	if b[0]==0 && b[1]==0 {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	out := make([]byte, 12)
	for i := range out {
		out[i] = chars[int(b[i%len(b)])%len(chars)]
	}
	return string(out)
}

func ptrTime(t time.Time) *time.Time { return &t }

// RandRead wraps crypto/rand without importing cycle - use os util
func RandRead(b []byte) (int, error) {
	f, err := os.Open("/dev/urandom")
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return io.ReadFull(f, b)
}
