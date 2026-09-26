package blog

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/jaecopzm/zedstream/internal/importer"
)

// velocityTrack is one trending track with the context the drafter may use.
// Only DB-backed facts — the model must not invent releases, dates, or numbers.
type velocityTrack struct {
	ID         string
	Title      string
	Artist     string
	Album      string
	Genre      string
	Plays      int
	ReleasedAt *time.Time
}

type draftDoc struct {
	Title    string `json:"title"`
	Excerpt  string `json:"excerpt"`
	Keywords string `json:"keywords"`
	Body     string `json:"body"`
}

// RunDailyAgent creates AI draft posts (status=review, never published) from
// this week's listening trends and new releases. Called by the scheduler once
// a day. Caps at 4 drafts per run. Set BLOG_AGENT_ENABLED=false to disable.
func (h *Handler) RunDailyAgent(ctx context.Context) {
	if os.Getenv("BLOG_AGENT_ENABLED") == "false" {
		return
	}
	log.Print("blog-agent: daily run starting")
	created := 0

	// 1. Weekly roundup (at most one per 6 days, needs a real trend week).
	if created < 4 && h.roundupDue(ctx) {
		top := h.topVelocity(ctx, 10)
		if h.weekPlays(ctx) >= 10 && len(top) >= 5 {
			if h.draftRoundup(ctx, top) {
				created++
			}
		} else {
			log.Print("blog-agent: quiet week, skipping roundup")
		}
	}

	// 2. All-time chart (monthly; fires immediately on first run and proves
	// the loop even in quiet weeks when velocity data is thin).
	if created < 4 && h.chartDue(ctx) {
		if top := h.topAllTime(ctx, 10); len(top) >= 10 {
			if h.draftChart(ctx, top) {
				created++
			}
		}
	}

	// 3. Spotlights for fresh releases with no published coverage.
	for _, t := range h.uncoveredReleases(ctx, 3) {
		if created >= 4 {
			break
		}
		if h.draftSpotlight(ctx, t) {
			created++
		}
	}

	log.Printf("blog-agent: daily run done, %d draft(s) queued for review", created)
}

func (h *Handler) roundupDue(ctx context.Context) bool {
	var count int
	_ = h.db.QueryRow(ctx, `SELECT COUNT(*) FROM posts
		WHERE post_type = 'roundup' AND status = 'published'
		AND published_at > NOW() - INTERVAL '6 days'`).Scan(&count)
	return count == 0
}

func (h *Handler) weekPlays(ctx context.Context) int {
	var n int
	_ = h.db.QueryRow(ctx, `SELECT COUNT(*) FROM play_events WHERE played_at > NOW() - INTERVAL '7 days'`).Scan(&n)
	return n
}

// topVelocity returns the most-played published tracks of the last 7 days.
func (h *Handler) topVelocity(ctx context.Context, limit int) []velocityTrack {
	rows, err := h.db.Query(ctx, fmt.Sprintf(`SELECT t.id::text, t.title,
		COALESCE(a.stage_name,''), COALESCE(al.title,''), COALESCE(g.name,''), COUNT(e.id)
		FROM play_events e
		JOIN tracks t ON t.id = e.track_id
		LEFT JOIN artists a ON a.id = t.artist_id
		LEFT JOIN albums al ON al.id = t.album_id
		LEFT JOIN genres g ON g.id = t.genre_id
		WHERE e.played_at > NOW() - INTERVAL '7 days' AND t.status = 'published'
		GROUP BY t.id, t.title, a.stage_name, al.title, g.name
		ORDER BY COUNT(e.id) DESC LIMIT %d`, limit))
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []velocityTrack
	for rows.Next() {
		var t velocityTrack
		if err := rows.Scan(&t.ID, &t.Title, &t.Artist, &t.Album, &t.Genre, &t.Plays); err == nil {
			out = append(out, t)
		}
	}
	return out
}

func (h *Handler) chartDue(ctx context.Context) bool {
	var count int
	_ = h.db.QueryRow(ctx, `SELECT COUNT(*) FROM posts
		WHERE post_type = 'chart' AND status = 'published'
		AND published_at > NOW() - INTERVAL '30 days'`).Scan(&count)
	return count == 0
}

// topAllTime returns the most-played published tracks of all time.
func (h *Handler) topAllTime(ctx context.Context, limit int) []velocityTrack {
	rows, err := h.db.Query(ctx, fmt.Sprintf(`SELECT t.id::text, t.title,
		COALESCE(a.stage_name,''), COALESCE(al.title,''), COALESCE(g.name,''), t.play_count
		FROM tracks t
		LEFT JOIN artists a ON a.id = t.artist_id
		LEFT JOIN albums al ON al.id = t.album_id
		LEFT JOIN genres g ON g.id = t.genre_id
		WHERE t.status = 'published' AND t.play_count > 0
		ORDER BY t.play_count DESC LIMIT %d`, limit))
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []velocityTrack
	for rows.Next() {
		var t velocityTrack
		if err := rows.Scan(&t.ID, &t.Title, &t.Artist, &t.Album, &t.Genre, &t.Plays); err == nil {
			out = append(out, t)
		}
	}
	return out
}

// uncoveredReleases returns tracks released in the last 7 days with no
// published post linking them yet.
func (h *Handler) uncoveredReleases(ctx context.Context, limit int) []velocityTrack {
	if limit <= 0 {
		return nil
	}
	rows, err := h.db.Query(ctx, fmt.Sprintf(`SELECT t.id::text, t.title,
		COALESCE(a.stage_name,''), COALESCE(al.title,''), COALESCE(g.name,''), t.released_at
		FROM tracks t
		LEFT JOIN artists a ON a.id = t.artist_id
		LEFT JOIN albums al ON al.id = t.album_id
		LEFT JOIN genres g ON g.id = t.genre_id
		WHERE t.status = 'published' AND t.released_at > NOW() - INTERVAL '7 days'
		AND NOT EXISTS (SELECT 1 FROM posts p WHERE p.status = 'published' AND t.id::text = ANY(p.linked_track_ids))
		ORDER BY t.released_at DESC LIMIT %d`, limit))
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []velocityTrack
	for rows.Next() {
		var t velocityTrack
		if err := rows.Scan(&t.ID, &t.Title, &t.Artist, &t.Album, &t.Genre, &t.ReleasedAt); err == nil {
			out = append(out, t)
		}
	}
	return out
}

const draftSystem = `You are an expert Zambian music journalist and SEO copywriter for the ZedBeatz Blog.
Write vibrant, specific, human-sounding articles. Never use generic filler like "in the vibrant world of music".
Use ONLY the track facts provided (titles, artists, genres, play counts). Do NOT invent release dates, quotes, chart positions, biographies, or events.
Body format is markdown-lite ONLY: ## subheadings, - bullet lists, **bold**, plain paragraphs. No URLs or links in the body (links are added automatically).
Return ONLY valid JSON, no markdown fences: {"title":"...","excerpt":"... (1-2 sentences)","keywords":"comma, separated","body":"..."}`

func trackContext(ts []velocityTrack) string {
	lines := make([]string, len(ts))
	for i, t := range ts {
		extra := ""
		if t.Album != "" {
			extra += fmt.Sprintf(", album %s", t.Album)
		}
		if t.Genre != "" {
			extra += fmt.Sprintf(", genre %s", t.Genre)
		}
		if t.Plays > 0 {
			extra += fmt.Sprintf(", %d plays this week", t.Plays)
		}
		lines[i] = fmt.Sprintf("%d. \"%s\" by %s%s", i+1, t.Title, t.Artist, extra)
	}
	return strings.Join(lines, "\n")
}

func (h *Handler) saveDraft(ctx context.Context, postType string, doc draftDoc, trackIDs, artistIDs []string) bool {
	if strings.TrimSpace(doc.Title) == "" || len(strings.TrimSpace(doc.Body)) < 400 {
		log.Printf("blog-agent: discarding thin draft (title=%q body=%d chars)", doc.Title, len(doc.Body))
		return false
	}
	slug := h.uniqueSlug(ctx, Slugify(doc.Title), "")
	keywords := strings.TrimSpace(doc.Keywords)
	if keywords == "" {
		keywords = "zambian music, zed music, new zambian songs"
	}
	_, err := h.db.Exec(ctx, `INSERT INTO posts
		(slug, title, excerpt, body, status, post_type, linked_track_ids, linked_artist_ids, keywords)
		VALUES ($1,$2,$3,$4,'review',$5,$6,$7,$8)`,
		slug, strings.TrimSpace(doc.Title), strings.TrimSpace(doc.Excerpt),
		strings.TrimSpace(doc.Body), postType, trackIDs, artistIDs, keywords)
	if err != nil {
		log.Printf("blog-agent: save draft failed: %v", err)
		return false
	}
	log.Printf("blog-agent: draft queued for review: %q (%s)", doc.Title, slug)
	return true
}

func (h *Handler) draftRoundup(ctx context.Context, top []velocityTrack) bool {
	week := time.Now().Format("2 January 2006")
	user := fmt.Sprintf("Write this week's roundup post for the week of %s.ZB chart week, hottest first:\n%s\n\nTitle must start with the week angle (e.g. \"10 Hottest Zambian Songs This Week\"). Aim 500-700 words with a ## intro, one short section per song group, and a closing line telling readers to stream on ZedBeatz.",
		week, trackContext(top))
	raw, errs := importer.GenerateCopy(draftSystem, user)
	if raw == "" {
		log.Printf("blog-agent: roundup generation failed: %s", strings.Join(errs, " | "))
		return false
	}
	var doc draftDoc
	if err := json.Unmarshal([]byte(importer.ExtractJSONObject(raw)), &doc); err != nil {
		log.Printf("blog-agent: roundup parse failed: %v", err)
		return false
	}
	ids := make([]string, len(top))
	artistSet := map[string]bool{}
	var artistIDs []string
	for i, t := range top {
		ids[i] = t.ID
	}
	// Resolve artist ids for the linked tracks.
	rows, err := h.db.Query(ctx, `SELECT DISTINCT artist_id::text FROM tracks WHERE id::text = ANY($1)`, ids)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var aid string
			if err := rows.Scan(&aid); err == nil && !artistSet[aid] {
				artistSet[aid] = true
				artistIDs = append(artistIDs, aid)
			}
		}
	}
	return h.saveDraft(ctx, "roundup", doc, ids, artistIDs)
}

func (h *Handler) draftChart(ctx context.Context, top []velocityTrack) bool {
	user := fmt.Sprintf("Write a chart post counting down the 10 most-played songs on ZedBeatz of all time, most-played first:\n%s\n\nTitle format: \"Top 10 Most Played Zambian Songs on ZedBeatz\". Aim 450-600 words: ## intro on what Zambia is streaming, then ## 10–6 and ## 5–1 countdown sections with one line per song on why it resonates, closing with a stream call-to-action. Present play counts as \"most-played\" framing, never exact numbers the reader can't verify — use the order given.",
		trackContext(top))
	raw, errs := importer.GenerateCopy(draftSystem, user)
	if raw == "" {
		log.Printf("blog-agent: chart generation failed: %s", strings.Join(errs, " | "))
		return false
	}
	var doc draftDoc
	if err := json.Unmarshal([]byte(importer.ExtractJSONObject(raw)), &doc); err != nil {
		log.Printf("blog-agent: chart parse failed: %v", err)
		return false
	}
	ids := make([]string, len(top))
	for i, t := range top {
		ids[i] = t.ID
	}
	artistSet := map[string]bool{}
	var artistIDs []string
	rows, err := h.db.Query(ctx, `SELECT DISTINCT artist_id::text FROM tracks WHERE id::text = ANY($1)`, ids)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var aid string
			if err := rows.Scan(&aid); err == nil && !artistSet[aid] {
				artistSet[aid] = true
				artistIDs = append(artistIDs, aid)
			}
		}
	}
	return h.saveDraft(ctx, "chart", doc, ids, artistIDs)
}

func (h *Handler) draftSpotlight(ctx context.Context, t velocityTrack) bool {
	user := fmt.Sprintf("Write a new-release spotlight post (300-450 words) for this track:\n%s\n\nTitle format: \"Artist – Title\" with a hook, e.g. why fans should press play today. Sections: ## The song, ## Why it matters, plus a closing stream call-to-action.",
		trackContext([]velocityTrack{t}))
	raw, errs := importer.GenerateCopy(draftSystem, user)
	if raw == "" {
		log.Printf("blog-agent: spotlight generation failed for %q: %s", t.Title, strings.Join(errs, " | "))
		return false
	}
	var doc draftDoc
	if err := json.Unmarshal([]byte(importer.ExtractJSONObject(raw)), &doc); err != nil {
		log.Printf("blog-agent: spotlight parse failed for %q: %v", t.Title, err)
		return false
	}
	var artistIDs []string
	var aid string
	if err := h.db.QueryRow(ctx, `SELECT artist_id::text FROM tracks WHERE id::text = $1`, t.ID).Scan(&aid); err == nil && aid != "" {
		artistIDs = append(artistIDs, aid)
	}
	return h.saveDraft(ctx, "spotlight", doc, []string{t.ID}, artistIDs)
}
