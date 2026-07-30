ALTER TABLE users DROP COLUMN IF EXISTS password_hash;

-- Note: removing 'email' from the oauth_provider enum requires a more complex migration.
-- It's safe to leave it as a member of the enum for now.
