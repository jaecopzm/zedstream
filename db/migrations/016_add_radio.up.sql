ALTER TABLE playlists ADD COLUMN is_radio BOOLEAN NOT NULL DEFAULT false;

CREATE TABLE radio_stations (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    slug        TEXT NOT NULL UNIQUE,
    description TEXT,
    cover_url   TEXT,
    type        TEXT NOT NULL CHECK (type IN ('genre', 'curated', 'personalized')),
    genre_id    TEXT REFERENCES genres(id) ON DELETE SET NULL,
    created_by  TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    is_active   BOOLEAN NOT NULL DEFAULT true,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE radio_station_tracks (
    station_id  TEXT NOT NULL REFERENCES radio_stations(id) ON DELETE CASCADE,
    track_id    TEXT NOT NULL REFERENCES tracks(id) ON DELETE CASCADE,
    track_order INTEGER NOT NULL DEFAULT 0,
    added_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (station_id, track_id)
);

CREATE INDEX idx_radio_stations_type ON radio_stations(type);
CREATE INDEX idx_radio_stations_genre ON radio_stations(genre_id);
CREATE INDEX idx_radio_station_tracks_station ON radio_station_tracks(station_id);
CREATE INDEX idx_radio_station_tracks_order ON radio_station_tracks(station_id, track_order);
