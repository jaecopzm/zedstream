ALTER TABLE artists ADD COLUMN social_links JSONB NOT NULL DEFAULT '{}'::jsonb;
