ALTER TABLE tracks ADD COLUMN hls_playlist_key TEXT;
ALTER TABLE tracks ADD COLUMN hls_status TEXT NOT NULL DEFAULT 'pending';
