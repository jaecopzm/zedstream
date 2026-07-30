DROP INDEX IF EXISTS tracks_embedding_idx;
ALTER TABLE tracks DROP COLUMN embedding;
