package blog

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jaecopzm/zedstream/internal/importer"
	"github.com/jaecopzm/zedstream/pkg/response"
)

// AssistWrite powers the "AI co-writer" panel on the admin New Post page.
// It reuses the provider chain (Gemini -> Groq -> NVIDIA via
// importer.GenerateCopy) and the same grounding rules as the daily agent:
// music facts come only from the DB, open-topic pieces must not invent
// claims about real living people. Nothing here publishes — it only returns
// proposal JSON for a human editor to accept, edit, or discard.
func (h *Handler) AssistWrite(w http.ResponseWriter, r *http.Request) {
	if !assistCooldown.allow() {
		response.TooManyRequests(w, "AI is still working — wait a few seconds and retry")
		return
	}

	var req assistRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.BadRequest(w, "invalid JSON body")
		return
	}
	req.Action = strings.ToLower(strings.TrimSpace(req.Action))
	req.TopicMode = strings.ToLower(strings.TrimSpace(req.TopicMode))
	req.PostType = strings.ToLower(strings.TrimSpace(req.PostType))
	req.Tone = strings.ToLower(strings.TrimSpace(req.Tone))
	req.Brief = strings.TrimSpace(req.Brief)

	if req.Action != "outline" && req.Action != "draft" && req.Action != "titles" {
		response.BadRequest(w, "action must be outline, draft, or titles")
		return
	}
	if req.TopicMode != "open" {
		req.TopicMode = "music"
	}
	if !validPostType(req.PostType) {
		req.PostType = "article"
	}
	if req.Tone == "" {
		req.Tone = "energetic"
	}
	toneLine, ok := assistTones[req.Tone]
	if !ok {
		response.BadRequest(w, "unknown tone")
		return
	}
	if len(req.Brief) < 10 {
		response.BadRequest(w, "brief is too short — describe the angle in a sentence or two")
		return
	}
	if len(req.Brief) > 2000 {
		response.BadRequest(w, "brief is too long (max 2000 characters)")
		return
	}
	if len(req.TrackIDs) > 10 || len(req.ArtistIDs) > 10 {
		response.BadRequest(w, "at most 10 tracks and 10 artists per request")
		return
	}
	target := req.TargetWords
	if target == 0 {
		target = assistDefaultWords(req.PostType)
	}
	if target < 150 {
		target = 150
	}
	if target > 900 {
		target = 900
	}

	ctx := r.Context()
	log.Printf("blog-assist: action=%s mode=%s type=%s tone=%s brief_chars=%d tracks=%d artists=%d",
		req.Action, req.TopicMode, req.PostType, req.Tone, len(req.Brief), len(req.TrackIDs), len(req.ArtistIDs))

	var (
		trackFacts  []velocityTrack
		artistNames []string
		warnings    []string
	)
	if req.TopicMode == "music" {
		trackFacts = h.assistTrackFacts(ctx, req.TrackIDs)
		artistNames = h.assistArtistNames(ctx, req.ArtistIDs)
		if len(req.TrackIDs) > 0 && len(trackFacts) == 0 {
			response.BadRequest(w, "none of the attached tracks exist")
			return
		}
		if len(trackFacts) == 0 && len(artistNames) == 0 {
			warnings = append(warnings, "No catalog tracks attached — song facts will be thin. Attach tracks for a grounded music story.")
		}
	} else {
		if looksLikeRumor(req.Brief) {
			warnings = append(warnings, "Brief reads like unverified gossip — the draft hedges its claims. Verify every allegation before publishing.")
		}
	}

	var (
		out assistResponse
		err error
	)
	switch req.Action {
	case "outline":
		out, err = h.assistOutline(ctx, req, trackFacts, artistNames, toneLine)
	case "titles":
		out, err = h.assistTitles(ctx, req, trackFacts, artistNames, toneLine)
	default:
		out, err = h.assistDraft(ctx, req, trackFacts, artistNames, toneLine, target)
	}
	if err != nil {
		response.InternalServerError(w, err.Error())
		return
	}

	// Suggest catalog tracks/artists for internal linking + the "Songs in this
	// story" box, so even open-topic SEO pieces link back into the library.
	if req.TopicMode == "open" || len(req.TrackIDs) == 0 {
		out.SuggestedTracks = h.relatedTracks(ctx, req.Brief, req.TrackIDs)
	}
	if req.TopicMode == "open" || len(req.ArtistIDs) == 0 {
		out.SuggestedArtists = h.relatedArtists(ctx, req.Brief, req.ArtistIDs)
	}
	out.Warnings = warnings
	response.OK(w, out)
}

type assistRequest struct {
	Action      string   `json:"action"`
	TopicMode   string   `json:"topic_mode"`
	PostType    string   `json:"post_type"`
	Brief       string   `json:"brief"`
	TrackIDs    []string `json:"track_ids"`
	ArtistIDs   []string `json:"artist_ids"`
	Tone        string   `json:"tone"`
	TargetWords int      `json:"target_words"`
}

type assistLinkSuggestion struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Artist string `json:"artist,omitempty"`
}

type assistResponse struct {
	Title            string                 `json:"title,omitempty"`
	Excerpt          string                 `json:"excerpt,omitempty"`
	Keywords         string                 `json:"keywords,omitempty"`
	Body             string                 `json:"body,omitempty"`
	Outline          []string               `json:"outline,omitempty"`
	Titles           []string               `json:"titles,omitempty"`
	SuggestedTracks  []assistLinkSuggestion `json:"suggested_tracks,omitempty"`
	SuggestedArtists []assistLinkSuggestion `json:"suggested_artists,omitempty"`
	Warnings         []string               `json:"warnings,omitempty"`
}

var assistTones = map[string]string{
	"energetic":    "Write with high energy and punch — short sentences, vivid verbs, fan-first excitement.",
	"bold":         "Write with a strong editorial voice and confident takes, but never invent facts.",
	"professional": "Write clean, measured music journalism — neutral, precise, no slang.",
	"playful":     "Write light and witty with gentle humor, while staying accurate and kind.",
}

func validPostType(t string) bool {
	switch t {
	case "article", "roundup", "spotlight", "chart", "profile":
		return true
	}
	return false
}

func assistDefaultWords(postType string) int {
	switch postType {
	case "roundup", "chart":
		return 550
	case "spotlight", "profile":
		return 400
	default:
		return 450
	}
}

// assistCooldown is a tiny guard so double-clicks don't burn model tokens.
var assistCooldown = &assistLimiter{minGap: 4 * time.Second}

type assistLimiter struct {
	mu     sync.Mutex
	last   time.Time
	minGap time.Duration
}

func (l *assistLimiter) allow() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if now.Sub(l.last) < l.minGap {
		return false
	}
	l.last = now
	return true
}

const assistMusicSystem = `You are an expert Zambian music journalist and SEO copywriter for the ZedBeatz Blog.
%s
Use ONLY the track/artist facts provided (titles, artists, genres, play counts). Do NOT invent release dates, quotes, chart positions, biographies, feuds, or events.
Body format is markdown-lite ONLY: ## subheadings, - bullet lists, **bold**, plain paragraphs. No URLs or links in the body (links are added automatically).
Return ONLY valid JSON, no markdown fences.`

const assistOpenSystem = `You are a Zambian entertainment journalist and SEO writer for the ZedBeatz Blog, covering music trends, industry buzz, culture, and lifestyle angles that pull search traffic.
%s
STRICT SAFETY RULES — never break these:
- Never invent quotes, dates, events, scandals, arrests, deaths, feuds, splits, pregnancies, or chart positions about real living people.
- If the brief contains an unverified rumor, hedge every claim ("reports claim…", "fans speculate…", "unconfirmed reports…") and add one line urging readers to treat it as unconfirmed.
- Prefer trend/explainer angles ("why X is taking over", "5 things to know") over asserting disputed facts. Stay non-defamatory and kind.
Body format is markdown-lite ONLY: ## subheadings, - bullet lists, **bold**, plain paragraphs. No URLs or links in the body (links are added automatically).
Return ONLY valid JSON, no markdown fences.`

func assistSystem(mode, toneLine string) string {
	if mode == "open" {
		return fmt.Sprintf(assistOpenSystem, toneLine)
	}
	return fmt.Sprintf(assistMusicSystem, toneLine)
}

func assistFactBlock(brief string, tracks []velocityTrack, artists []string, toneLine string) string {
	var sb strings.Builder
	sb.WriteString("Editor brief / angle:\n" + brief + "\n")
	if len(tracks) > 0 {
		sb.WriteString("\nVerified catalog facts (use only these for song details):\n" + trackContext(tracks) + "\n")
	}
	if len(artists) > 0 {
		sb.WriteString("\nVerified artists in focus: " + strings.Join(artists, ", ") + "\n")
	}
	sb.WriteString("\nStyle: " + toneLine + "\n")
	return sb.String()
}

func (h *Handler) assistOutline(ctx context.Context, req assistRequest, tracks []velocityTrack, artists []string, toneLine string) (assistResponse, error) {
	_ = ctx
	user := assistFactBlock(req.Brief, tracks, artists, toneLine) +
		fmt.Sprintf("\nReturn JSON: {\"titles\":[3 clickable headline options],\"outline\":[5-7 section headings with a 10-word summary each]}. Post type: %s.", req.PostType)
	raw, errs := importer.GenerateCopy(assistSystem(req.TopicMode, toneLine), user)
	if raw == "" {
		return assistResponse{}, fmt.Errorf("AI outline failed: %s", strings.Join(errs, " | "))
	}
	var doc struct {
		Titles  []string `json:"titles"`
		Outline []string `json:"outline"`
	}
	if err := json.Unmarshal([]byte(importer.ExtractJSONObject(raw)), &doc); err != nil || len(doc.Outline) == 0 {
		return assistResponse{}, fmt.Errorf("AI outline was unreadable — try a sharper brief")
	}
	return assistResponse{Titles: doc.Titles, Outline: doc.Outline}, nil
}

func (h *Handler) assistTitles(ctx context.Context, req assistRequest, tracks []velocityTrack, artists []string, toneLine string) (assistResponse, error) {
	_ = ctx
	user := assistFactBlock(req.Brief, tracks, artists, toneLine) +
		"\nReturn JSON: {\"titles\":[5 clickable SEO headlines, varied angles],\"excerpt\":\"1-2 sentence search snippet\",\"keywords\":\"comma, separated, SEO keywords\"}."
	raw, errs := importer.GenerateCopy(assistSystem(req.TopicMode, toneLine), user)
	if raw == "" {
		return assistResponse{}, fmt.Errorf("AI titles failed: %s", strings.Join(errs, " | "))
	}
	var doc struct {
		Titles   []string `json:"titles"`
		Excerpt  string   `json:"excerpt"`
		Keywords string   `json:"keywords"`
	}
	if err := json.Unmarshal([]byte(importer.ExtractJSONObject(raw)), &doc); err != nil || len(doc.Titles) == 0 {
		return assistResponse{}, fmt.Errorf("AI titles were unreadable — try again")
	}
	return assistResponse{Titles: doc.Titles, Excerpt: strings.TrimSpace(doc.Excerpt), Keywords: strings.TrimSpace(doc.Keywords)}, nil
}

func (h *Handler) assistDraft(ctx context.Context, req assistRequest, tracks []velocityTrack, artists []string, toneLine string, target int) (assistResponse, error) {
	_ = ctx
	user := assistFactBlock(req.Brief, tracks, artists, toneLine) +
		fmt.Sprintf("\nWrite the full post (aim ~%d words) with a ## intro, body sections, and a closing line telling readers to stream on ZedBeatz. Return JSON: {\"title\":\"...\",\"excerpt\":\"1-2 sentences\",\"keywords\":\"comma, separated\",\"body\":\"...\"}.", target)
	doc, errs, ok := generateDraftDoc(assistSystem(req.TopicMode, toneLine), user)
	if !ok {
		// One retry with an explicit repair nudge before giving up.
		doc, errs, ok = generateDraftDoc(assistSystem(req.TopicMode, toneLine),
			user+"\n\nYour previous output was invalid. Return ONLY the JSON object, no other text.")
	}
	if !ok {
		if len(errs) > 0 {
			return assistResponse{}, fmt.Errorf("AI draft failed: %s", strings.Join(errs, " | "))
		}
		return assistResponse{}, fmt.Errorf("AI draft was too thin or unreadable — sharpen the brief and retry")
	}
	return assistResponse{
		Title: strings.TrimSpace(doc.Title), Excerpt: strings.TrimSpace(doc.Excerpt),
		Keywords: strings.TrimSpace(doc.Keywords), Body: strings.TrimSpace(doc.Body),
	}, nil
}

// generateDraftDoc calls the model and enforces the daily agent's quality bar:
// a real title and a body of at least 400 characters.
func generateDraftDoc(system, user string) (draftDoc, []string, bool) {
	raw, errs := importer.GenerateCopy(system, user)
	if raw == "" {
		return draftDoc{}, errs, false
	}
	var doc draftDoc
	if err := json.Unmarshal([]byte(importer.ExtractJSONObject(raw)), &doc); err != nil {
		return draftDoc{}, errs, false
	}
	if strings.TrimSpace(doc.Title) == "" || len(strings.TrimSpace(doc.Body)) < 400 {
		return draftDoc{}, errs, false
	}
	return doc, nil, true
}

// assistTrackFacts resolves attached track ids to verified fact lines.
func (h *Handler) assistTrackFacts(ctx context.Context, ids []string) []velocityTrack {
	if len(ids) == 0 {
		return nil
	}
	rows, err := h.db.Query(ctx, `SELECT t.id::text, t.title,
		COALESCE(a.stage_name,''), COALESCE(al.title,''), COALESCE(g.name,'')
		FROM tracks t
		LEFT JOIN artists a ON a.id = t.artist_id
		LEFT JOIN albums al ON al.id = t.album_id
		LEFT JOIN genres g ON g.id = t.genre_id
		WHERE t.id::text = ANY($1)`, ids)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []velocityTrack
	for rows.Next() {
		var t velocityTrack
		if err := rows.Scan(&t.ID, &t.Title, &t.Artist, &t.Album, &t.Genre); err == nil {
			out = append(out, t)
		}
	}
	return out
}

func (h *Handler) assistArtistNames(ctx context.Context, ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	rows, err := h.db.Query(ctx, `SELECT stage_name FROM artists WHERE id::text = ANY($1)`, ids)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err == nil && name != "" {
			out = append(out, name)
		}
	}
	return out
}

var rumorHints = []string{"allegedly", "scandal", "beef", "diss", "arrest", "cheat", "affair", "leak", "expos", "fight", "slap", "dead", "died", "death", "divorce", "breakup", "pregnant", "secret baby", " Illuminati", "satan"}

func looksLikeRumor(brief string) bool {
	lower := " " + strings.ToLower(brief)
	for _, hint := range rumorHints {
		if strings.Contains(lower, strings.TrimSpace(hint)) {
			return true
		}
	}
	return false
}

var briefStopwords = map[string]bool{
	"music": true, "songs": true, "song": true, "zambia": true, "zambian": true,
	"video": true, "videos": true, "album": true, "albums": true, "artist": true,
	"artists": true, "track": true, "tracks": true, "about": true, "with": true,
	"from": true, "this": true, "that": true, "what": true, "when": true,
	"best": true, "fans": true, " draped": true, "their": true, "there": true,
	"latest": true, "trends": true, "trend": true, "gossip": true, "news": true,
	"story": true, "hottest": true,
}

// briefKeywords pulls naive search terms from a brief for catalog matching.
func briefKeywords(brief string, limit int) []string {
	lower := strings.ToLower(brief)
	var out []string
	seen := map[string]bool{}
	for _, raw := range strings.Fields(lower) {
		w := strings.Trim(raw, ".,;:!?\"'()[]{}-–—")
		if len(w) < 4 || seen[w] || briefStopwords[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func likeEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

// relatedTracks finds published catalog tracks matching the brief for internal linking.
func (h *Handler) relatedTracks(ctx context.Context, brief string, exclude []string) []assistLinkSuggestion {
	excluded := map[string]bool{}
	for _, id := range exclude {
		excluded[id] = true
	}
	seen := map[string]bool{}
	var out []assistLinkSuggestion
	for _, kw := range briefKeywords(brief, 6) {
		if len(out) >= 5 {
			break
		}
		rows, err := h.db.Query(ctx, `SELECT t.id::text, t.title, COALESCE(a.stage_name,'')
			FROM tracks t LEFT JOIN artists a ON a.id = t.artist_id
			WHERE t.status = 'published' AND (t.title ILIKE '%' || $1 || '%' ESCAPE '\' OR a.stage_name ILIKE '%' || $1 || '%' ESCAPE '\')
			ORDER BY t.play_count DESC LIMIT 3`, likeEscape(kw))
		if err != nil {
			continue
		}
		for rows.Next() {
			var s assistLinkSuggestion
			if err := rows.Scan(&s.ID, &s.Title, &s.Artist); err == nil && !seen[s.ID] && !excluded[s.ID] {
				seen[s.ID] = true
				out = append(out, s)
				if len(out) >= 5 {
					break
				}
			}
		}
		rows.Close()
	}
	return out
}

func (h *Handler) relatedArtists(ctx context.Context, brief string, exclude []string) []assistLinkSuggestion {
	excluded := map[string]bool{}
	for _, id := range exclude {
		excluded[id] = true
	}
	seen := map[string]bool{}
	var out []assistLinkSuggestion
	for _, kw := range briefKeywords(brief, 6) {
		if len(out) >= 5 {
			break
		}
		rows, err := h.db.Query(ctx, `SELECT id::text, stage_name FROM artists
			WHERE stage_name ILIKE '%' || $1 || '%' ESCAPE '\' LIMIT 3`, likeEscape(kw))
		if err != nil {
			continue
		}
		for rows.Next() {
			var s assistLinkSuggestion
			if err := rows.Scan(&s.ID, &s.Title); err == nil && !seen[s.ID] && !excluded[s.ID] {
				seen[s.ID] = true
				out = append(out, s)
				if len(out) >= 5 {
					break
				}
			}
		}
		rows.Close()
	}
	return out
}
