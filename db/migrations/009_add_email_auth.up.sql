DO $$ BEGIN
  ALTER TYPE oauth_provider ADD VALUE IF NOT EXISTS 'email';
EXCEPTION
  WHEN duplicate_object THEN NULL;
END $$;

ALTER TABLE users ADD COLUMN IF NOT EXISTS password_hash TEXT;
