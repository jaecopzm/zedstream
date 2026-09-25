-- Downloader jobs for superfast MP3 ingestion (API for Flutter)
CREATE TYPE download_status AS ENUM ('queued','downloading','converting','uploading','completed','failed');
CREATE TYPE download_quality AS ENUM ('128','192','256','320');

CREATE TABLE downloader_jobs (
    id             TEXT PRIMARY KEY,
    user_id        TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    url            TEXT NOT NULL,
    title          TEXT,
    artist_name    TEXT,
    quality        download_quality NOT NULL DEFAULT '192',
    status         download_status NOT NULL DEFAULT 'queued',
    progress       INT NOT NULL DEFAULT 0, -- 0-100
    error          TEXT,
    file_size      BIGINT,
    duration_sec   INT,
    cover_url      TEXT,
    audio_key      TEXT, -- R2 key when completed
    track_id       TEXT REFERENCES tracks(id) ON DELETE SET NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at   TIMESTAMPTZ
);

CREATE INDEX idx_downloader_jobs_user_id ON downloader_jobs(user_id);
CREATE INDEX idx_downloader_jobs_status ON downloader_jobs(status);
CREATE INDEX idx_downloader_jobs_created ON downloader_jobs(created_at DESC);
