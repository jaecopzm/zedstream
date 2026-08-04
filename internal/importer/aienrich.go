package importer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
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
	Model       string          `json:"model"`
	Messages    []openAIMessage `json:"messages"`
	Temperature float64         `json:"temperature"`
	MaxTokens   int             `json:"max_tokens"`
}

type openAIResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func callGemini(apiKey, systemPrompt, userPrompt string) (string, error) {
	model := os.Getenv("GEMINI_MODEL")
	if model == "" {
		model = "gemini-2.5-flash"
	}
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", model, apiKey)

	combinedPrompt := systemPrompt + "\n\n" + userPrompt
	payload := map[string]any{
		"contents": []map[string]any{
			{
				"parts": []map[string]any{
					{"text": combinedPrompt},
				},
			},
		},
		"generationConfig": map[string]any{
			"temperature":      0.4,
			"maxOutputTokens":  2000,
			"responseMimeType": "application/json",
		},
	}
	jsonBody, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 45 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("gemini api error (status %d): %s", resp.StatusCode, string(body))
	}

	var res struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return "", err
	}
	if len(res.Candidates) == 0 || len(res.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("gemini returned no content")
	}
	return res.Candidates[0].Content.Parts[0].Text, nil
}

func callGroq(apiKey, apiURL, model, systemPrompt, userPrompt string) (string, error) {
	if apiURL == "" {
		apiURL = "https://api.groq.com/openai/v1"
	}
	if model == "" {
		model = "llama-3.3-70b-versatile"
	}

	body := openAIRequest{
		Model:       model,
		Temperature: 0.4,
		MaxTokens:   2000,
		Messages: []openAIMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
	}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", apiURL+"/chat/completions", bytes.NewReader(jsonBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: 45 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("groq api error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var openAIResp openAIResponse
	if err := json.Unmarshal(respBody, &openAIResp); err != nil {
		return "", err
	}
	if len(openAIResp.Choices) == 0 {
		return "", fmt.Errorf("groq returned no choices")
	}
	return openAIResp.Choices[0].Message.Content, nil
}

func (h *Handler) AIEnrich(w http.ResponseWriter, r *http.Request) {
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

	systemPrompt := `You are an expert Zambian music curator, cultural critic, and SEO copywriter for ZedBeatz. 
Your goal is to write vibrant, engaging, highly descriptive, and punchy SEO descriptions (2-3 sentences) that capture the true vibe, rhythm, storytelling, and cultural resonance of the track. Avoid generic boilerplate like "This is a great song by artist". Instead, highlight the groove, instrumentation, lyrical themes, and why music lovers should stream it.

For each track, also determine the most appropriate Zambian or African music genre from this list (or close variant):
- Zambian Hip Hop
- Kalindula
- Zambian Afrobeats
- Zambian Gospel
- Zambian R&B
- Zambian Dancehall
- Zambian Pop
- Amapiano
- Reggae
- Zed Oldies

Return ONLY valid JSON with this exact structure (no markdown, no backticks):
{"results":[{"index":0,"description":"...","genre":"..."}]}`

	userPrompt := "Generate rich SEO descriptions and accurate genre suggestions for these tracks:\n" + strings.Join(trackLines, "\n")

	var rawContent string
	var lastErr error

	// 1. Try Gemini first if GEMINI_API_KEY is available
	geminiKey := os.Getenv("GEMINI_API_KEY")
	if geminiKey == "" && strings.HasPrefix(os.Getenv("AI_PROVIDER"), "gemini") {
		geminiKey = os.Getenv("AI_API_KEY")
	}
	if geminiKey != "" {
		content, err := callGemini(geminiKey, systemPrompt, userPrompt)
		if err == nil {
			rawContent = content
		} else {
			lastErr = err
			log.Printf("  ⚠ Gemini enrich failed, falling back to Groq: %v", err)
		}
	}

	// 2. Fall back to Groq / OpenAI compatible API if Gemini wasn't used or failed
	if rawContent == "" {
		groqKey := os.Getenv("GROQ_API_KEY")
		if groqKey == "" {
			groqKey = os.Getenv("AI_API_KEY")
		}
		if groqKey == "" {
			if lastErr != nil {
				http.Error(w, fmt.Sprintf(`{"error":"AI enrichment failed (Gemini: %s, Groq API key not configured)"}`, lastErr.Error()), http.StatusServiceUnavailable)
			} else {
				http.Error(w, `{"error":"GEMINI_API_KEY or GROQ_API_KEY not configured"}`, http.StatusServiceUnavailable)
			}
			return
		}
		content, err := callGroq(groqKey, os.Getenv("AI_API_URL"), os.Getenv("AI_MODEL"), systemPrompt, userPrompt)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"AI enrichment failed: %s"}`, err.Error()), http.StatusInternalServerError)
			return
		}
		rawContent = content
	}

	content := strings.TrimSpace(rawContent)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	var enrichmentResp AIEnrichmentResponse
	if err := json.Unmarshal([]byte(content), &enrichmentResp); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"parse enrichment JSON: %s (raw: %s)"}`, err.Error(), content), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(enrichmentResp)
}
