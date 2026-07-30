CREATE TABLE play_events (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id           UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    track_id          UUID NOT NULL REFERENCES tracks(id) ON DELETE CASCADE,
    duration_listened INTEGER NOT NULL DEFAULT 0,
    played_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_play_events_user_id    ON play_events(user_id);
CREATE INDEX idx_play_events_track_id   ON play_events(track_id);
CREATE INDEX idx_play_events_played_at  ON play_events(played_at);

CREATE TABLE follows (
    follower_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    artist_id   UUID NOT NULL REFERENCES artists(id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (follower_id, artist_id)
);

CREATE INDEX idx_follows_artist_id ON follows(artist_id);

CREATE TABLE likes (
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    track_id   UUID NOT NULL REFERENCES tracks(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, track_id)
);

CREATE INDEX idx_likes_track_id ON likes(track_id);

CREATE TABLE playlists (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title       TEXT NOT NULL,
    description TEXT,
    cover_url   TEXT,
    is_public   BOOLEAN NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_playlists_user_id ON playlists(user_id);

CREATE TABLE playlist_tracks (
    playlist_id  UUID NOT NULL REFERENCES playlists(id) ON DELETE CASCADE,
    track_id     UUID NOT NULL REFERENCES tracks(id) ON DELETE CASCADE,
    track_order  INTEGER NOT NULL DEFAULT 0,
    added_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (playlist_id, track_id)
);

CREATE TABLE track_comments (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    track_id   UUID NOT NULL REFERENCES tracks(id) ON DELETE CASCADE,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    body       TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_track_comments_track_id ON track_comments(track_id);

CREATE TABLE messages (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    artist_id  UUID NOT NULL REFERENCES artists(id) ON DELETE CASCADE,
    subject    TEXT NOT NULL,
    body       TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE message_recipients (
    message_id UUID NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    read       BOOLEAN NOT NULL DEFAULT FALSE,
    read_at    TIMESTAMPTZ,
    PRIMARY KEY (message_id, user_id)
);

CREATE INDEX idx_message_recipients_user_id ON message_recipients(user_id);
