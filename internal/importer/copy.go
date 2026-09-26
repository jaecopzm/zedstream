package importer

import (
	"log"
	"os"
	"strings"
)

// GenerateCopy runs the provider chain (Gemini -> Groq -> NVIDIA) and returns
// the first successful raw model output. Shared by AI enrichment and the blog
// agent so every AI feature rides out per-provider free-tier rate limits.
// Returns ("", errs) when no provider is configured or all fail.
func GenerateCopy(systemPrompt, userPrompt string) (string, []string) {
	var rawContent string
	var errs []string

	type openAIProvider struct {
		name       string
		keyEnv     string
		urlEnv     string
		urlDefault string
		modelEnv   string
		model      string
	}
	providers := []openAIProvider{
		{name: "groq", keyEnv: "GROQ_API_KEY", urlEnv: "AI_API_URL", urlDefault: "https://api.groq.com/openai/v1", modelEnv: "AI_MODEL", model: "openai/gpt-oss-20b"},
		{name: "nvidia", keyEnv: "NVIDIA_API_KEY", urlEnv: "NVIDIA_API_URL", urlDefault: "https://integrate.api.nvidia.com/v1", modelEnv: "NVIDIA_MODEL", model: "nvidia/nemotron-3.5-lightning-30b-a3b"},
	}

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
			errs = append(errs, "gemini: "+err.Error())
			log.Printf("  ⚠ Gemini copy failed, falling back: %v", err)
		}
	}

	// 2. Fall through OpenAI-compatible providers (Groq, NVIDIA, ...) until one works
	if rawContent == "" {
		for _, p := range providers {
			key := os.Getenv(p.keyEnv)
			if key == "" && p.name == "groq" {
				key = os.Getenv("AI_API_KEY")
			}
			if key == "" {
				continue
			}
			apiURL := os.Getenv(p.urlEnv)
			if apiURL == "" {
				apiURL = p.urlDefault
			}
			model := os.Getenv(p.modelEnv)
			if model == "" {
				model = p.model
			}
			content, err := callGroq(key, apiURL, model, systemPrompt, userPrompt)
			if err == nil {
				rawContent = content
				break
			}
			errs = append(errs, p.name+": "+err.Error())
			log.Printf("  ⚠ %s copy failed, trying next provider: %v", p.name, err)
		}
	}

	return rawContent, errs
}

// ExtractJSONObject trims fences and thinking text, returning the outermost
// {...} JSON object substring (or the input unchanged when none found).
func ExtractJSONObject(raw string) string {
	content := strings.TrimSpace(raw)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)
	if i, j := strings.Index(content, "{"), strings.LastIndex(content, "}"); i >= 0 && j > i {
		content = content[i : j+1]
	}
	return content
}
