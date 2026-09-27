-- Convert genre IDs from UUID to 12-char NanoID (base62), matching the
-- format pkg/id generates for artists/albums/tracks/etc. Genres are
-- seed-only (no app-side creation), so they never got converted by 007.
-- Up-only like 007 (old UUIDs are not recoverable, and nothing needs them).

-- Random 12-char base62 generator (same alphabet as pkg/id).
CREATE OR REPLACE FUNCTION pg_temp.nanoid12() RETURNS text AS $$
  SELECT string_agg(
    substr('0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz', (floor(random() * 62) + 1)::int, 1),
    '' ORDER BY g
  ) FROM generate_series(1, 12) g;
$$ LANGUAGE sql VOLATILE;

-- Drop the FK so PK values can be swapped.
ALTER TABLE tracks DROP CONSTRAINT IF EXISTS tracks_genre_id_fkey;

-- One stable mapping for the whole migration.
CREATE TEMP TABLE genre_id_map AS
  SELECT id AS old_id, pg_temp.nanoid12() AS new_id FROM genres;

-- Remap referencing rows, then the genres themselves.
-- (New IDs are 12-char alnum; they can never collide with UUID values,
-- so no ordering hazard between the two updates.)
UPDATE tracks t SET genre_id = m.new_id
  FROM genre_id_map m WHERE t.genre_id = m.old_id;

UPDATE genres g SET id = m.new_id
  FROM genre_id_map m WHERE g.id = m.old_id;

DROP TABLE genre_id_map;

-- Recreate the FK.
ALTER TABLE tracks ADD CONSTRAINT tracks_genre_id_fkey
  FOREIGN KEY (genre_id) REFERENCES genres(id) ON DELETE SET NULL;
