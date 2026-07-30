package importer

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const spotifyAPIBase = "https://api.spotify.com/v1"

type spotifyClient struct {
	clientID     string
	clientSecret string
	http         *http.Client

	mu       sync.Mutex
	token    string
	expiresAt time.Time
}

func newSpotifyClient(clientID, clientSecret string) *spotifyClient {
	// Force IPv4 — IPv6 is unreachable on many hosts and Go's dialer
	// tries both in parallel, causing 15s+ hangs when IPv6 fails silently.
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialer.DialContext(ctx, "tcp4", addr)
		},
		TLSHandshakeTimeout: 10 * time.Second,
	}
	return &spotifyClient{
		clientID:     clientID,
		clientSecret: clientSecret,
		http:         &http.Client{Timeout: 30 * time.Second, Transport: transport},
	}
}

func (s *spotifyClient) getToken() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.token != "" && time.Now().Before(s.expiresAt) {
		return s.token, nil
	}

	creds := base64.StdEncoding.EncodeToString([]byte(s.clientID + ":" + s.clientSecret))
	req, _ := http.NewRequest("POST", "https://accounts.spotify.com/api/token",
		strings.NewReader("grant_type=client_credentials"))
	req.Header.Set("Authorization", "Basic "+creds)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("spotify token request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("spotify token error: %s", string(body))
	}

	var tr struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tr); err != nil {
		return "", fmt.Errorf("spotify token parse: %w", err)
	}

	s.token = tr.AccessToken
	s.expiresAt = time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	return s.token, nil
}

func (s *spotifyClient) do(path string, params url.Values) ([]byte, error) {
	token, err := s.getToken()
	if err != nil {
		return nil, err
	}

	u := spotifyAPIBase + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}

	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := s.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("spotify request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == 401 {
		s.mu.Lock()
		s.token = ""
		s.mu.Unlock()
		return s.do(path, params)
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("spotify error %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}

type SpotifyTrack struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	DurationMs int    `json:"duration_ms"`
	Artists    []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"artists"`
	Album struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		ReleaseDate string `json:"release_date"`
		Images      []spotifyImage `json:"images"`
		AlbumType string `json:"album_type"`
	} `json:"album"`
	ExternalIDs struct {
		ISRC string `json:"isrc"`
	} `json:"external_ids"`
}

type SpotifyAlbum struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	AlbumType   string `json:"album_type"`
	ReleaseDate string `json:"release_date"`
	Images      []spotifyImage `json:"images"`
	Artists []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"artists"`
	Tracks struct {
		Items []SpotifyTrack `json:"items"`
		Next  *string        `json:"next"`
	} `json:"tracks"`
}

type SpotifyPlaylist struct {
	Name   string         `json:"name"`
	Images []spotifyImage `json:"images"`
	Tracks struct {
		Items []struct {
			Track SpotifyTrack `json:"track"`
		} `json:"items"`
		Next *string `json:"next"`
	} `json:"tracks"`
}

func (s *spotifyClient) FetchTrack(id string) (*SpotifyTrack, error) {
	body, err := s.do("/tracks/"+id, nil)
	if err != nil {
		return nil, err
	}
	var t SpotifyTrack
	if err := json.Unmarshal(body, &t); err != nil {
		return nil, fmt.Errorf("parse track: %w", err)
	}
	return &t, nil
}

type SpotifyArtist struct {
	ID     string         `json:"id"`
	Name   string         `json:"name"`
	Images []spotifyImage `json:"images"`
	Genres []string       `json:"genres"`
}

func (s *spotifyClient) FetchArtist(id string) (*SpotifyArtist, error) {
	body, err := s.do("/artists/"+id, nil)
	if err != nil {
		return nil, err
	}
	var a SpotifyArtist
	if err := json.Unmarshal(body, &a); err != nil {
		return nil, fmt.Errorf("parse artist: %w", err)
	}
	return &a, nil
}

func (s *spotifyClient) FetchAlbum(id string) (*SpotifyAlbum, error) {
	body, err := s.do("/albums/"+id, nil)
	if err != nil {
		return nil, err
	}
	var a SpotifyAlbum
	if err := json.Unmarshal(body, &a); err != nil {
		return nil, fmt.Errorf("parse album: %w", err)
	}
	return &a, nil
}

func (s *spotifyClient) FetchPlaylist(id string) (*SpotifyPlaylist, error) {
	body, err := s.do("/playlists/"+id, url.Values{"limit": {"50"}})
	if err != nil {
		return nil, err
	}
	var p SpotifyPlaylist
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("parse playlist: %w", err)
	}
	return &p, nil
}

func (s *spotifyClient) SearchArtist(query string) (*SpotifyArtist, error) {
	body, err := s.do("/search", url.Values{"q": {query}, "type": {"artist"}, "limit": {"1"}})
	if err != nil {
		return nil, err
	}
	var sr struct {
		Artists struct {
			Items []SpotifyArtist `json:"items"`
		} `json:"artists"`
	}
	if err := json.Unmarshal(body, &sr); err != nil {
		return nil, fmt.Errorf("parse artist search: %w", err)
	}
	if len(sr.Artists.Items) == 0 {
		return nil, fmt.Errorf("no artist found for: %s", query)
	}
	return &sr.Artists.Items[0], nil
}

func (s *spotifyClient) SearchTracks(query string, limit int) ([]SpotifyTrack, error) {
	body, err := s.do("/search", url.Values{"q": {query}, "type": {"track"}, "limit": {fmt.Sprintf("%d", limit)}})
	if err != nil {
		return nil, err
	}
	var sr struct {
		Tracks struct {
			Items []SpotifyTrack `json:"items"`
		} `json:"tracks"`
	}
	if err := json.Unmarshal(body, &sr); err != nil {
		return nil, fmt.Errorf("parse search: %w", err)
	}
	return sr.Tracks.Items, nil
}
