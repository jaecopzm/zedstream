package recommendation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// Embedder generates vector embeddings for tracks to power the recommendation engine.
type Embedder struct {
	openAIKey string
}

// NewEmbedder initializes a new recommendation embedder.
func NewEmbedder(apiKey string) *Embedder {
	return &Embedder{openAIKey: apiKey}
}

// GenerateTrackEmbedding takes a track's metadata and returns a 1536-dimensional vector.
// This vector represents the semantic "vibe" and properties of the song.
func (e *Embedder) GenerateTrackEmbedding(ctx context.Context, title, artist, genre string) ([]float32, error) {
	// Combine the metadata into a single descriptive string
	metadata := fmt.Sprintf("Song Title: %s. Artist: %s. Genre: %s.", title, artist, genre)

	type openAIReq struct {
		Input string `json:"input"`
		Model string `json:"model"`
	}

	reqBody := openAIReq{
		Input: metadata,
		Model: "text-embedding-3-small",
	}
	bodyBytes, _ := json.Marshal(reqBody)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.openai.com/v1/embeddings", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.openAIKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embedding request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status from openai: %d", resp.StatusCode)
	}

	var res struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if len(res.Data) == 0 {
		return nil, fmt.Errorf("no embedding returned")
	}

	return res.Data[0].Embedding, nil
}
