CREATE TABLE genres (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name       TEXT NOT NULL UNIQUE,
    slug       TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Seed common Zambian and African genres
INSERT INTO genres (name, slug) VALUES
    ('Afrobeats', 'afrobeats'),
    ('Zambian Hip-Hop', 'zambian-hip-hop'),
    ('Kalindula', 'kalindula'),
    ('Gospel', 'gospel'),
    ('R&B', 'rnb'),
    ('Reggae', 'reggae'),
    ('Dancehall', 'dancehall'),
    ('Afro-Pop', 'afro-pop'),
    ('Jazz', 'jazz'),
    ('Traditional', 'traditional');

CREATE TABLE artists (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    stage_name  TEXT NOT NULL,
    bio         TEXT,
    photo_url   TEXT,
    verified    BOOLEAN NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_artists_user_id ON artists(user_id);

CREATE TYPE track_status AS ENUM ('draft', 'published', 'scheduled');
CREATE TYPE album_type   AS ENUM ('album', 'ep', 'single');
CREATE TYPE album_status AS ENUM ('draft', 'published', 'scheduled');

CREATE TABLE albums (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    artist_id    UUID NOT NULL REFERENCES artists(id) ON DELETE CASCADE,
    title        TEXT NOT NULL,
    cover_url    TEXT,
    type         album_type NOT NULL DEFAULT 'album',
    status       album_status NOT NULL DEFAULT 'draft',
    scheduled_at TIMESTAMPTZ,
    released_at  TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_albums_artist_id ON albums(artist_id);

CREATE TABLE tracks (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    artist_id    UUID NOT NULL REFERENCES artists(id) ON DELETE CASCADE,
    album_id     UUID REFERENCES albums(id) ON DELETE SET NULL,
    title        TEXT NOT NULL,
    duration_sec INTEGER NOT NULL DEFAULT 0,
    genre_id     UUID REFERENCES genres(id) ON DELETE SET NULL,
    cover_url    TEXT,
    audio_key    TEXT NOT NULL,  -- R2 object key
    file_size    BIGINT NOT NULL DEFAULT 0,
    mime_type    TEXT NOT NULL DEFAULT 'audio/mpeg',
    status       track_status NOT NULL DEFAULT 'draft',
    scheduled_at TIMESTAMPTZ,
    released_at  TIMESTAMPTZ,
    play_count   BIGINT NOT NULL DEFAULT 0,
    like_count   BIGINT NOT NULL DEFAULT 0,
    track_order  INTEGER NOT NULL DEFAULT 0,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- Full-text search vector
    search_vector TSVECTOR GENERATED ALWAYS AS (
        to_tsvector('english', coalesce(title, ''))
    ) STORED
);

CREATE INDEX idx_tracks_artist_id  ON tracks(artist_id);
CREATE INDEX idx_tracks_album_id   ON tracks(album_id);
CREATE INDEX idx_tracks_genre_id   ON tracks(genre_id);
CREATE INDEX idx_tracks_status     ON tracks(status);
CREATE INDEX idx_tracks_search     ON tracks USING GIN(search_vector);

CREATE TABLE track_collaborators (
    track_id   UUID NOT NULL REFERENCES tracks(id) ON DELETE CASCADE,
    artist_id  UUID NOT NULL REFERENCES artists(id) ON DELETE CASCADE,
    role       TEXT NOT NULL DEFAULT 'featured',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (track_id, artist_id)
);

CREATE TABLE album_collaborators (
    album_id   UUID NOT NULL REFERENCES albums(id) ON DELETE CASCADE,
    artist_id  UUID NOT NULL REFERENCES artists(id) ON DELETE CASCADE,
    role       TEXT NOT NULL DEFAULT 'featured',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (album_id, artist_id)
);
