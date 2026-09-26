-- Blog posts for SEO (Zed music news, roundups, charts, artist stories).
-- status flow: draft -> review -> published (human gate before publish).
CREATE TABLE posts (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slug             TEXT NOT NULL UNIQUE,
    title            TEXT NOT NULL,
    excerpt          TEXT NOT NULL DEFAULT '',
    body             TEXT NOT NULL DEFAULT '',
    cover_url        TEXT,
    status           TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'review', 'published')),
    post_type        TEXT NOT NULL DEFAULT 'article' CHECK (post_type IN ('article', 'roundup', 'spotlight', 'chart', 'profile')),
    linked_track_ids TEXT[] NOT NULL DEFAULT '{}',
    linked_artist_ids TEXT[] NOT NULL DEFAULT '{}',
    keywords         TEXT NOT NULL DEFAULT '',
    scheduled_at     TIMESTAMPTZ,
    published_at     TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_posts_status_published ON posts (status, published_at DESC);
CREATE INDEX idx_posts_slug ON posts (slug);
