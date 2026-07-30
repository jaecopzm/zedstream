package importer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type AIEnrichmentRequest struct {
	Tracks []AIEnrichmentTrack `json:"tracks"`
}

type AIEnrichmentTrack struct {
	Title  string `json:"title"`
	Artist string `json:"artist"`
	Album  string `json:"album"`
}

type AIEnrichmentResult struct {
	Index       int    `json:"index"`
	Description string `json:"description"`
	Genre       string `json:"genre"`
}

type AIEnrichmentResponse struct {
	Results []AIEnrichmentResult `json:"results"`
}

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIRequest struct {
	Model       string           `json:"model"`
	Messages    []openAIMessage  `json:"messages"`
	Temperature float64          `json:"temperature"`
	MaxTokens   int              `json:"max_tokens"`
}

type openAIResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func getAIEnv() (apiKey, apiURL, model string) {
	apiKey = os.Getenv("AI_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("GROQ_API_KEY")
	}
	apiURL = os.Getenv("AI_API_URL")
	model = os.Getenv("AI_MODEL")
	if apiURL == "" {
		apiURL = "https://api.groq.com/openai/v1"
	}
	if model == "" {
		model = "llama-3.3-70b-versatile"
	}
	return
}

func (h *Handler) AIEnrich(w http.ResponseWriter, r *http.Request) {
	apiKey, apiURL, model := getAIEnv()
	if apiKey == "" {
		http.Error(w, `{"error":"AI_API_KEY or GROQ_API_KEY not configured"}`, http.StatusServiceUnavailable)
		return
	}

	var req AIEnrichmentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON body"}`, http.StatusBadRequest)
		return
	}
	if len(req.Tracks) == 0 {
		http.Error(w, `{"error":"no tracks provided"}`, http.StatusBadRequest)
		return
	}

	trackLines := make([]string, len(req.Tracks))
	for i, t := range req.Tracks {
		line := fmt.Sprintf("%d. \"%s\" by %s", i, t.Title, t.Artist)
		if t.Album != "" {
			line += fmt.Sprintf(" (album: %s)", t.Album)
		}
		trackLines[i] = line
	}

	systemPrompt := `You are a music SEO specialist and genre expert for Zambian music. 
For each track provided, generate:
1. A compelling 1-2 sentence SEO description that would appear in search results and meta tags.
2. The most appropriate genre label (e.g. "Zambian Hip Hop", "Kalindula", "Zambian Afrobeats", "Zambian Gospel", "Zambian R&B", "Zambian Dancehall", "Zambian Pop", "Reggae", "Dancehall", "Amapiano", "Zed Oldies").

Return ONLY valid JSON with this exact structure (no markdown, no backticks):
{"results":[{"index":0,"description":"...","genre":"..."}]}`

	userPrompt := "Generate SEO descriptions and genre suggestions for these tracks:\n" + strings.Join(trackLines, "\n")

	body := openAIRequest{
		Model:       model,
		Temperature: 0.3,
		MaxTokens:   2000,
		Messages: []openAIMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"marshal: %s"}`, err.Error()), http.StatusInternalServerError)
		return
	}

	httpReq, err := http.NewRequest("POST", apiURL+"/chat/completions", bytes.NewReader(jsonBody))
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"create request: %s"}`, err.Error()), http.StatusInternalServerError)
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: 60 * time.Second}
	httpResp, err := client.Do(httpReq)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"AI request failed: %s"}`, err.Error()), http.StatusInternalServerError)
		return
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"read response: %s"}`, err.Error()), http.StatusInternalServerError)
		return
	}

	if httpResp.StatusCode != 200 {
		http.Error(w, fmt.Sprintf(`{"error":"AI API error (status %d): %s"}`, httpResp.StatusCode, string(respBody)), http.StatusInternalServerError)
		return
	}

	var openAIResp openAIResponse
	if err := json.Unmarshal(respBody, &openAIResp); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"parse AI response: %s"}`, err.Error()), http.StatusInternalServerError)
		return
	}

	if len(openAIResp.Choices) == 0 {
		http.Error(w, `{"error":"AI returned no choices"}`, http.StatusInternalServerError)
		return
	}

	content := strings.TrimSpace(openAIResp.Choices[0].Message.Content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	var enrichmentResp AIEnrichmentResponse
	if err := json.Unmarshal([]byte(content), &enrichmentResp); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"parse enrichment JSON: %s"}`, err.Error()), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(enrichmentResp)
}
