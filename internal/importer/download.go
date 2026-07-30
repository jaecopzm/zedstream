package importer

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const cdnBase = "https://cdn-spotify.zm.io.vn/download"

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

func safeFileName(s string) string {
	s = safeNameRegexp.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}

func downloadAudio(isrc, outputDir string) (string, error) {
	if isrc == "" {
		return "", fmt.Errorf("ISRC is required for CDN download")
	}

	cdnURL := fmt.Sprintf("%s/isrc/%s", cdnBase, isrc)

	req, _ := http.NewRequest("GET", cdnURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 Chrome/120.0.0.0 Safari/537.36")

	resp, err := ipv4Client.Do(req)
	if err != nil {
		return "", fmt.Errorf("cdn request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("cdn returned status %d", resp.StatusCode)
	}

	ct := resp.Header.Get("content-type")
	if !strings.HasPrefix(ct, "audio/") {
		return "", fmt.Errorf("cdn returned non-audio content-type: %s", ct)
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

	filename := fmt.Sprintf("track_%s%s", isrc, ext)
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
