package search

import (
	"context"
	"log/slog"
)

type Client struct{}

type TrackDocument struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	ArtistName  string `json:"artist_name"`
	Genre       string `json:"genre,omitempty"`
	DurationSec int    `json:"duration_sec"`
	PlayCount   int64  `json:"play_count"`
	CoverURL    string `json:"cover_url,omitempty"`
}

func NewClient(host, apiKey string) *Client {
	slog.Info("search client: meilisearch disabled", "host", host)
	return &Client{}
}

func (c *Client) SetupIndexes(ctx context.Context) error {
	return nil
}

func (c *Client) IndexTrack(ctx context.Context, doc TrackDocument) error {
	return nil
}

func (c *Client) DeleteTrack(ctx context.Context, id string) error {
	return nil
}

func (c *Client) SearchTracks(ctx context.Context, query string, limit int64) ([]TrackDocument, error) {
	return []TrackDocument{}, nil
}
