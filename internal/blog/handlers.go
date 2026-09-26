package blog

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jaecopzm/zedstream/pkg/response"
)

// ── Public ─────────────────────────────────────────────────────────────────

// ListPublished returns published posts, newest first (for /blog hub + sitemap).
func (h *Handler) ListPublished(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 100 {
		limit = 20
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	postType := strings.TrimSpace(r.URL.Query().Get("type"))

	query := `SELECT ` + postColumns + ` FROM posts WHERE status = 'published'`
	args := []any{}
	if postType != "" {
		query += ` AND post_type = $1`
		args = append(args, postType)
	}
	query += fmt.Sprintf(` ORDER BY published_at DESC NULLS LAST, created_at DESC LIMIT %d OFFSET %d`, limit, offset)

	rows, err := h.db.Query(r.Context(), query, args...)
	if err != nil {
		response.InternalServerError(w, "failed to fetch posts")
		return
	}
	defer rows.Close()

	posts := []*Post{}
	for rows.Next() {
		p, err := scanPost(rows.Scan)
		if err != nil {
			continue
		}
		p.Body = "" // list view: no body payload
		posts = append(posts, p)
	}
	response.OK(w, map[string]any{"posts": posts})
}

// GetBySlug returns one published post with linked track/artist cards.
func (h *Handler) GetBySlug(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	if slug == "" {
		response.BadRequest(w, "slug is required")
		return
	}
	p, err := scanPost(h.db.QueryRow(r.Context(),
		`SELECT `+postColumns+` FROM posts WHERE slug = $1 AND status = 'published'`, slug,
	).Scan)
	if err != nil {
		response.NotFound(w, "post not found")
		return
	}
	h.attachLinked(r.Context(), p)
	response.OK(w, p)
}

// SitemapFeed returns lightweight slug/timestamp rows for published posts.
func (h *Handler) SitemapFeed(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query(r.Context(),
		`SELECT slug, COALESCE(published_at, updated_at) FROM posts WHERE status = 'published' ORDER BY published_at DESC`)
	if err != nil {
		response.InternalServerError(w, "failed to fetch post sitemap")
		return
	}
	defer rows.Close()
	type entry struct {
		Slug      string    `json:"slug"`
		UpdatedAt time.Time `json:"updated_at"`
	}
	out := []entry{}
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.Slug, &e.UpdatedAt); err == nil {
			out = append(out, e)
		}
	}
	response.OK(w, map[string]any{"posts": out})
}

// ── Admin ──────────────────────────────────────────────────────────────────

// AdminList returns all posts regardless of status (review queue).
func (h *Handler) AdminList(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 200 {
		limit = 50
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	status := strings.TrimSpace(r.URL.Query().Get("status"))

	query := `SELECT ` + postColumns + ` FROM posts`
	args := []any{}
	if status != "" {
		query += ` WHERE status = $1`
		args = append(args, status)
	}
	query += fmt.Sprintf(` ORDER BY updated_at DESC LIMIT %d OFFSET %d`, limit, offset)

	rows, err := h.db.Query(r.Context(), query, args...)
	if err != nil {
		response.InternalServerError(w, "failed to fetch posts")
		return
	}
	defer rows.Close()

	posts := []*Post{}
	for rows.Next() {
		p, err := scanPost(rows.Scan)
		if err != nil {
			continue
		}
		posts = append(posts, p)
	}
	response.OK(w, map[string]any{"posts": posts})
}

type postPayload struct {
	Title           *string  `json:"title"`
	Slug            *string  `json:"slug"`
	Excerpt         *string  `json:"excerpt"`
	Body            *string  `json:"body"`
	CoverURL        *string  `json:"cover_url"`
	Status          *string  `json:"status"`
	PostType        *string  `json:"post_type"`
	LinkedTrackIDs  []string `json:"linked_track_ids"`
	LinkedArtistIDs []string `json:"linked_artist_ids"`
	Keywords        *string  `json:"keywords"`
	ScheduledAt     *string  `json:"scheduled_at"`
}

// CreatePost creates a draft/review post. Accepts JSON or multipart (for cover upload).
func (h *Handler) CreatePost(w http.ResponseWriter, r *http.Request) {
	var p postPayload
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/form-data") {
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			response.BadRequest(w, "invalid multipart body")
			return
		}
		str := func(k string) *string {
			if v := strings.TrimSpace(r.FormValue(k)); v != "" {
				return &v
			}
			return nil
		}
		p.Title, p.Slug, p.Excerpt, p.Body = str("title"), str("slug"), str("excerpt"), str("body")
		p.CoverURL, p.Status, p.PostType = str("cover_url"), str("status"), str("post_type")
		p.Keywords = str("keywords")
		if v := strings.TrimSpace(r.FormValue("scheduled_at")); v != "" {
			p.ScheduledAt = &v
		}
		if v := strings.TrimSpace(r.FormValue("linked_track_ids")); v != "" {
			_ = json.Unmarshal([]byte(v), &p.LinkedTrackIDs)
		}
		if v := strings.TrimSpace(r.FormValue("linked_artist_ids")); v != "" {
			_ = json.Unmarshal([]byte(v), &p.LinkedArtistIDs)
		}
	} else {
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			response.BadRequest(w, "invalid JSON body")
			return
		}
	}

	title := ""
	if p.Title != nil {
		title = strings.TrimSpace(*p.Title)
	}
	if title == "" {
		response.BadRequest(w, "title is required")
		return
	}
	slug := ""
	if p.Slug != nil {
		slug = Slugify(*p.Slug)
	}
	if slug == "" {
		slug = Slugify(title)
	}
	slug = h.uniqueSlug(r.Context(), slug, "")

	status := "draft"
	if p.Status != nil && ValidStatus(strings.TrimSpace(*p.Status)) {
		status = strings.TrimSpace(*p.Status)
	}
	postType := "article"
	if p.PostType != nil && ValidType(strings.TrimSpace(*p.PostType)) {
		postType = strings.TrimSpace(*p.PostType)
	}

	var publishedAt *time.Time
	if status == "published" {
		now := time.Now()
		publishedAt = &now
	}
	var scheduledAt *time.Time
	if p.ScheduledAt != nil {
		if t, err := time.Parse(time.RFC3339, *p.ScheduledAt); err == nil {
			scheduledAt = &t
		}
	}

	excerpt, body, keywords := "", "", ""
	if p.Excerpt != nil {
		excerpt = *p.Excerpt
	}
	if p.Body != nil {
		body = *p.Body
	}
	if p.Keywords != nil {
		keywords = *p.Keywords
	}

	var id string
	err := h.db.QueryRow(r.Context(), `INSERT INTO posts
		(slug, title, excerpt, body, cover_url, status, post_type, linked_track_ids, linked_artist_ids, keywords, scheduled_at, published_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING id::text`,
		slug, title, excerpt, body, p.CoverURL, status, postType,
		p.LinkedTrackIDs, p.LinkedArtistIDs, keywords, scheduledAt, publishedAt).Scan(&id)
	if err != nil {
		response.InternalServerError(w, "failed to create post")
		return
	}

	// Optional featured image in the same multipart request.
	if strings.HasPrefix(ct, "multipart/form-data") {
		if url, uerr := h.uploadCover(r, id); uerr == nil && url != "" {
			_, _ = h.db.Exec(r.Context(), `UPDATE posts SET cover_url = $2, updated_at = NOW() WHERE id::text = $1`, id, url)
		}
	}

	created, err := scanPost(h.db.QueryRow(r.Context(),
		`SELECT `+postColumns+` FROM posts WHERE id::text = $1`, id).Scan)
	if err != nil {
		response.InternalServerError(w, "failed to load created post")
		return
	}
	response.OK(w, created)
}

// UpdatePost patches a post; publishing sets published_at. Multipart supported for cover.
func (h *Handler) UpdatePost(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		response.BadRequest(w, "post id is required")
		return
	}
	existing, err := scanPost(h.db.QueryRow(r.Context(),
		`SELECT `+postColumns+` FROM posts WHERE id::text = $1`, id).Scan)
	if err != nil {
		response.NotFound(w, "post not found")
		return
	}

	var p postPayload
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/form-data") {
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			response.BadRequest(w, "invalid multipart body")
			return
		}
		str := func(k string) *string {
			if v := r.FormValue(k); v != "" {
				return &v
			}
			return nil
		}
		// Empty string means "clear this field" for excerpt/body/keywords/cover.
		if _, ok := r.Form["title"]; ok {
			p.Title = str("title")
		}
		if _, ok := r.Form["slug"]; ok {
			p.Slug = str("slug")
		}
		if _, ok := r.Form["excerpt"]; ok {
			v := r.FormValue("excerpt")
			p.Excerpt = &v
		}
		if _, ok := r.Form["body"]; ok {
			v := r.FormValue("body")
			p.Body = &v
		}
		if _, ok := r.Form["cover_url"]; ok {
			v := r.FormValue("cover_url")
			p.CoverURL = &v
		}
		if _, ok := r.Form["status"]; ok {
			p.Status = str("status")
		}
		if _, ok := r.Form["post_type"]; ok {
			p.PostType = str("post_type")
		}
		if _, ok := r.Form["keywords"]; ok {
			v := r.FormValue("keywords")
			p.Keywords = &v
		}
		if _, ok := r.Form["scheduled_at"]; ok {
			v := r.FormValue("scheduled_at")
			p.ScheduledAt = &v
		}
		if v := strings.TrimSpace(r.FormValue("linked_track_ids")); v != "" {
			_ = json.Unmarshal([]byte(v), &p.LinkedTrackIDs)
		} else if _, ok := r.Form["linked_track_ids"]; ok {
			p.LinkedTrackIDs = []string{}
		}
		if v := strings.TrimSpace(r.FormValue("linked_artist_ids")); v != "" {
			_ = json.Unmarshal([]byte(v), &p.LinkedArtistIDs)
		} else if _, ok := r.Form["linked_artist_ids"]; ok {
			p.LinkedArtistIDs = []string{}
		}
	} else {
		var raw map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			response.BadRequest(w, "invalid JSON body")
			return
		}
		if v, ok := raw["title"]; ok {
			var s string
			_ = json.Unmarshal(v, &s)
			p.Title = &s
		}
		if v, ok := raw["slug"]; ok {
			var s string
			_ = json.Unmarshal(v, &s)
			p.Slug = &s
		}
		if v, ok := raw["excerpt"]; ok {
			var s string
			_ = json.Unmarshal(v, &s)
			p.Excerpt = &s
		}
		if v, ok := raw["body"]; ok {
			var s string
			_ = json.Unmarshal(v, &s)
			p.Body = &s
		}
		if v, ok := raw["cover_url"]; ok {
			if string(v) == "null" {
				s := ""
				p.CoverURL = &s
			} else {
				var s string
				_ = json.Unmarshal(v, &s)
				p.CoverURL = &s
			}
		}
		if v, ok := raw["status"]; ok {
			var s string
			_ = json.Unmarshal(v, &s)
			p.Status = &s
		}
		if v, ok := raw["post_type"]; ok {
			var s string
			_ = json.Unmarshal(v, &s)
			p.PostType = &s
		}
		if v, ok := raw["keywords"]; ok {
			var s string
			_ = json.Unmarshal(v, &s)
			p.Keywords = &s
		}
		if v, ok := raw["scheduled_at"]; ok {
			if string(v) == "null" {
				s := ""
				p.ScheduledAt = &s
			} else {
				var s string
				_ = json.Unmarshal(v, &s)
				p.ScheduledAt = &s
			}
		}
		if v, ok := raw["linked_track_ids"]; ok {
			_ = json.Unmarshal(v, &p.LinkedTrackIDs)
		}
		if v, ok := raw["linked_artist_ids"]; ok {
			_ = json.Unmarshal(v, &p.LinkedArtistIDs)
		}
	}

	title := existing.Title
	if p.Title != nil && strings.TrimSpace(*p.Title) != "" {
		title = strings.TrimSpace(*p.Title)
	}
	slug := existing.Slug
	if p.Slug != nil {
		if s := Slugify(*p.Slug); s != "" {
			slug = h.uniqueSlug(r.Context(), s, id)
		}
	}
	if p.Title != nil && p.Slug == nil {
		// Title changed without explicit slug: keep existing slug (stable URLs).
		slug = existing.Slug
	}
	excerpt, body, keywords := existing.Excerpt, existing.Body, existing.Keywords
	if p.Excerpt != nil {
		excerpt = *p.Excerpt
	}
	if p.Body != nil {
		body = *p.Body
	}
	if p.Keywords != nil {
		keywords = *p.Keywords
	}
	cover := existing.CoverURL
	if p.CoverURL != nil {
		if *p.CoverURL == "" {
			cover = nil
		} else {
			cover = p.CoverURL
		}
	}
	status := existing.Status
	if p.Status != nil && ValidStatus(strings.TrimSpace(*p.Status)) {
		status = strings.TrimSpace(*p.Status)
	}
	postType := existing.PostType
	if p.PostType != nil && ValidType(strings.TrimSpace(*p.PostType)) {
		postType = strings.TrimSpace(*p.PostType)
	}
	tracks := existing.LinkedTrackIDs
	if p.LinkedTrackIDs != nil {
		tracks = p.LinkedTrackIDs
	}
	artists := existing.LinkedArtistIDs
	if p.LinkedArtistIDs != nil {
		artists = p.LinkedArtistIDs
	}
	publishedAt := existing.PublishedAt
	if status == "published" && publishedAt == nil {
		now := time.Now()
		publishedAt = &now
	}
	var scheduledAt = existing.ScheduledAt
	if p.ScheduledAt != nil {
		if *p.ScheduledAt == "" {
			scheduledAt = nil
		} else if t, err := time.Parse(time.RFC3339, *p.ScheduledAt); err == nil {
			scheduledAt = &t
		}
	}

	// Featured image upload wins over cover_url field when both present.
	if strings.HasPrefix(ct, "multipart/form-data") {
		if url, uerr := h.uploadCover(r, id); uerr == nil && url != "" {
			cover = &url
		}
	}

	_, err = h.db.Exec(r.Context(), `UPDATE posts SET
		slug=$2, title=$3, excerpt=$4, body=$5, cover_url=$6, status=$7, post_type=$8,
		linked_track_ids=$9, linked_artist_ids=$10, keywords=$11, scheduled_at=$12,
		published_at=$13, updated_at=NOW() WHERE id::text=$1`,
		id, slug, title, excerpt, body, cover, status, postType,
		tracks, artists, keywords, scheduledAt, publishedAt)
	if err != nil {
		response.InternalServerError(w, "failed to update post")
		return
	}

	updated, err := scanPost(h.db.QueryRow(r.Context(),
		`SELECT `+postColumns+` FROM posts WHERE id::text = $1`, id).Scan)
	if err != nil {
		response.InternalServerError(w, "failed to load updated post")
		return
	}
	response.OK(w, updated)
}

// DeletePost removes a post.
func (h *Handler) DeletePost(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		response.BadRequest(w, "post id is required")
		return
	}
	res, err := h.db.Exec(r.Context(), `DELETE FROM posts WHERE id::text = $1`, id)
	if err != nil {
		response.InternalServerError(w, "failed to delete post")
		return
	}
	if res.RowsAffected() == 0 {
		response.NotFound(w, "post not found")
		return
	}
	response.OK(w, map[string]any{"deleted": true})
}

// uploadCover stores a multipart "cover" image in R2 and returns its public URL.
func (h *Handler) uploadCover(r *http.Request, postID string) (string, error) {
	file, header, err := r.FormFile("cover")
	if err != nil {
		return "", err
	}
	defer file.Close()
	contentType := header.Header.Get("Content-Type")
	if !strings.HasPrefix(contentType, "image/") {
		return "", fmt.Errorf("cover must be an image")
	}
	ext := extensionFromMime(contentType)
	if ext == "" {
		return "", fmt.Errorf("unsupported image type")
	}
	key := fmt.Sprintf("blog/%s/cover%s", postID, ext)
	if err := h.storage.UploadFile(r.Context(), h.imageBucket, key, contentType, file); err != nil {
		return "", err
	}
	return h.storage.PublicURL(key), nil
}
