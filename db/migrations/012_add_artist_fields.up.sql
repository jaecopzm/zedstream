ALTER TABLE artists ADD COLUMN cover_url TEXT;
ALTER TABLE artists ADD COLUMN location TEXT;
ALTER TABLE artists ADD COLUMN genre_tags TEXT[] DEFAULT '{}';
