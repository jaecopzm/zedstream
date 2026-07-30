CREATE EXTENSION IF NOT EXISTS vector;

ALTER TABLE tracks ADD COLUMN embedding vector(1536);

-- Create an index for faster similarity searches (cosine similarity)
CREATE INDEX ON tracks USING ivfflat (embedding vector_cosine_ops) WITH (lists = 100);
