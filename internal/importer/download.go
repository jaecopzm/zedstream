package importer

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var safeNameRegexp = regexp.MustCompile(`[^\w\s-]`)

// ipv4Client forces IPv4 to avoid hangs on hosts where IPv6 is unreachable.
var ipv4Client = &http.Client{
	Timeout: 120 * time.Second,
	Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext(ctx, "tcp4", addr)
		},
		TLSHandshakeTimeout: 10 * time.Second,
	},
}

// downloaderClient allows long fetches: the in-house downloader resolves the
// track, downloads and re-encodes audio server-side (up to ~12 min); typical
// tracks complete well under this.
var downloaderClient = &http.Client{
	Timeout: 10 * time.Minute,
	Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext(ctx, "tcp4", addr)
		},
		TLSHandshakeTimeout: 10 * time.Second,
	},
}

func safeFileName(s string) string {
	s = safeNameRegexp.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}

// downloaderBaseURL returns the in-house audio downloader (streamer-go) base URL.
// Same-box default; override with DOWNLOADER_URL.
func downloaderBaseURL() string {
	if v := strings.TrimSpace(os.Getenv("DOWNLOADER_URL")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "http://127.0.0.1:8081"
}

// downloadAudio fetches tagged MP3 audio for a track from the in-house
// downloader (/api/download). It resolves "artist - title" to the best
// duration-aware YouTube match, embeds cover art + ID3 tags, and serves
// audio/* — the same shape the old ISRC CDN returned.
func downloadAudio(title, artist, coverURL string, durationMs int, outputDir string) (string, error) {
	title = strings.TrimSpace(title)
	artist = strings.TrimSpace(artist)
	if title == "" || artist == "" {
		return "", fmt.Errorf("title and artist are required for audio download")
	}

	q := url.Values{}
	q.Set("q", artist+" - "+title)
	q.Set("format", "mp3")
	q.Set("title", title)
	q.Set("artist", artist)
	if strings.TrimSpace(coverURL) != "" {
		q.Set("cover", strings.TrimSpace(coverURL))
	}
	if durationMs > 0 {
		q.Set("duration_ms", strconv.Itoa(durationMs))
	}
	dlURL := downloaderBaseURL() + "/api/download?" + q.Encode()

	req, _ := http.NewRequest("GET", dlURL, nil)
	req.Header.Set("User-Agent", "ZedStream-Importer/1.0")

	resp, err := downloaderClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("downloader request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		if msg := strings.TrimSpace(string(detail)); msg != "" {
			return "", fmt.Errorf("downloader returned status %d (%s)", resp.StatusCode, msg)
		}
		return "", fmt.Errorf("downloader returned status %d", resp.StatusCode)
	}

	ct := resp.Header.Get("content-type")
	if !strings.HasPrefix(ct, "audio/") {
		return "", fmt.Errorf("downloader returned non-audio content-type: %s", ct)
	}

	ext := ".mp3"
	switch {
	case strings.Contains(ct, "flac"):
		ext = ".flac"
	case strings.Contains(ct, "wav"):
		ext = ".wav"
	case strings.Contains(ct, "ogg"):
		ext = ".ogg"
	case strings.Contains(ct, "m4a") || strings.Contains(ct, "mp4"):
		ext = ".m4a"
	}

	filename := fmt.Sprintf("%s - %s%s", safeFileName(artist), safeFileName(title), ext)
	filePath := filepath.Join(outputDir, filename)
	f, err := os.Create(filePath)
	if err != nil {
		return "", fmt.Errorf("create file: %w", err)
	}
	defer f.Close()

	written, err := io.Copy(f, resp.Body)
	if err != nil {
		os.Remove(filePath)
		return "", fmt.Errorf("download failed: %w", err)
	}

	if written < 100000 {
		os.Remove(filePath)
		return "", fmt.Errorf("downloaded file too small (%d bytes)", written)
	}

	return filePath, nil
}

func downloadCover(imageURL, outputDir string) (string, error) {
	if imageURL == "" {
		return "", nil
	}

	req, _ := http.NewRequest("GET", imageURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")

	resp, err := ipv4Client.Do(req)
	if err != nil {
		return "", fmt.Errorf("cover request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("cover returned status %d", resp.StatusCode)
	}

	ext := ".jpg"
	ct := resp.Header.Get("content-type")
	switch {
	case strings.Contains(ct, "png"):
		ext = ".png"
	case strings.Contains(ct, "webp"):
		ext = ".webp"
	}

	filePath := filepath.Join(outputDir, "cover"+ext)
	f, err := os.Create(filePath)
	if err != nil {
		return "", fmt.Errorf("create cover file: %w", err)
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		os.Remove(filePath)
		return "", fmt.Errorf("cover download failed: %w", err)
	}

	return filePath, nil
}
