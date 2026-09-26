package blog

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jaecopzm/zedstream/pkg/storage"
)

// Post is a blog article for SEO (news, roundups, charts, profiles).
type Post struct {
	ID              string       `json:"id"`
	Slug            string       `json:"slug"`
	Title           string       `json:"title"`
	Excerpt         string       `json:"excerpt"`
	Body            string       `json:"body"`
	CoverURL        *string      `json:"cover_url,omitempty"`
	Status          string       `json:"status"`
	PostType        string       `json:"post_type"`
	LinkedTrackIDs  []string     `json:"linked_track_ids"`
	LinkedArtistIDs []string     `json:"linked_artist_ids"`
	Keywords        string       `json:"keywords"`
	ScheduledAt     *time.Time   `json:"scheduled_at,omitempty"`
	PublishedAt     *time.Time   `json:"published_at,omitempty"`
	CreatedAt       time.Time    `json:"created_at"`
	UpdatedAt       time.Time    `json:"updated_at"`
	// Embedded for public readers (related blocks, JSON-LD).
	Tracks  []LinkedTrack  `json:"tracks,omitempty"`
	Artists []LinkedArtist `json:"artists,omitempty"`
}

// LinkedTrack is a minimal track card embedded in a post response.
type LinkedTrack struct {
	ID         string  `json:"id"`
	Title      string  `json:"title"`
	ArtistName string  `json:"artist_name"`
	CoverURL   *string `json:"cover_url,omitempty"`
}

// LinkedArtist is a minimal artist card embedded in a post response.
type LinkedArtist struct {
	ID        string  `json:"id"`
	StageName string  `json:"stage_name"`
	PhotoURL  *string `json:"photo_url,omitempty"`
}

// Handler serves public blog reads and admin CRUD.
type Handler struct {
	db          *pgxpool.Pool
	storage     *storage.Client
	imageBucket string
}

// NewHandler wires the blog handler.
func NewHandler(db *pgxpool.Pool, store *storage.Client, imageBucket string) *Handler {
	return &Handler{db: db, storage: store, imageBucket: imageBucket}
}

const postColumns = `id, slug, title, excerpt, body, cover_url, status, post_type,
	linked_track_ids, linked_artist_ids, keywords, scheduled_at, published_at, created_at, updated_at`

func scanPost(scan func(dest ...any) error) (*Post, error) {
	var p Post
	if err := scan(&p.ID, &p.Slug, &p.Title, &p.Excerpt, &p.Body, &p.CoverURL, &p.Status,
		&p.PostType, &p.LinkedTrackIDs, &p.LinkedArtistIDs, &p.Keywords, &p.ScheduledAt,
		&p.PublishedAt, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, err
	}
	if p.LinkedTrackIDs == nil {
		p.LinkedTrackIDs = []string{}
	}
	if p.LinkedArtistIDs == nil {
		p.LinkedArtistIDs = []string{}
	}
	return &p, nil
}

// Slugify converts a title into a URL-friendly slug.
func Slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "&", "and")
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		case r == ' ', r == '-', r == '_', r == '+':
			if !prevDash && b.Len() > 0 {
				b.WriteRune('-')
				prevDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = fmt.Sprintf("post-%d", time.Now().Unix())
	}
	return out
}

func extensionFromMime(mime string) string {
	switch mime {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	default:
		return ""
	}
}

// ValidStatus reports whether s is a legal post status.
func ValidStatus(s string) bool {
	return s == "draft" || s == "review" || s == "published"
}

// ValidType reports whether s is a legal post type.
func ValidType(s string) bool {
	return s == "article" || s == "roundup" || s == "spotlight" || s == "chart" || s == "profile"
}

// uniqueSlug returns base or base-2, base-3... until free (excluding excludeID).
func (h *Handler) uniqueSlug(ctx context.Context, base, excludeID string) string {
	var count int
	_ = h.db.QueryRow(ctx, `SELECT COUNT(*) FROM posts WHERE slug = $1 AND ($2 = '' OR id::text <> $2)`, base, excludeID).Scan(&count)
	if count == 0 {
		return base
	}
	for i := 2; i < 1000; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		_ = h.db.QueryRow(ctx, `SELECT COUNT(*) FROM posts WHERE slug = $1 AND ($2 = '' OR id::text <> $2)`, candidate, excludeID).Scan(&count)
		if count == 0 {
			return candidate
		}
	}
	return fmt.Sprintf("%s-%d", base, time.Now().Unix())
}

// attachLinked loads track/artist cards for a post's linked ids.
func (h *Handler) attachLinked(ctx context.Context, p *Post) {
	if len(p.LinkedTrackIDs) > 0 {
		rows, err := h.db.Query(ctx, `SELECT t.id::text, t.title, COALESCE(a.stage_name,''), t.cover_url
			FROM tracks t LEFT JOIN artists a ON a.id = t.artist_id
			WHERE t.id::text = ANY($1) LIMIT 20`, p.LinkedTrackIDs)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var t LinkedTrack
				if err := rows.Scan(&t.ID, &t.Title, &t.ArtistName, &t.CoverURL); err == nil {
					p.Tracks = append(p.Tracks, t)
				}
			}
		}
	}
	if len(p.LinkedArtistIDs) > 0 {
		rows, err := h.db.Query(ctx, `SELECT id::text, stage_name, photo_url
			FROM artists WHERE id::text = ANY($1) LIMIT 20`, p.LinkedArtistIDs)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var a LinkedArtist
				if err := rows.Scan(&a.ID, &a.StageName, &a.PhotoURL); err == nil {
					p.Artists = append(p.Artists, a)
				}
			}
		}
	}
}
