ALTER TABLE tracks ADD COLUMN section TEXT NOT NULL DEFAULT '';
CREATE INDEX idx_tracks_section ON tracks (section) WHERE section != '';
