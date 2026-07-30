DROP INDEX IF EXISTS idx_tracks_section;
ALTER TABLE tracks DROP COLUMN IF EXISTS section;
