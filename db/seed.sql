-- Seed data for ZedBeatz development
-- Run: psql -d zedstream -f db/seed.sql

-- Demo user for browsing
INSERT INTO users (id, email, name, avatar_url, role, provider, provider_id)
VALUES (gen_random_uuid(), 'demo@zedstream.app', 'Demo User', 'https://picsum.photos/seed/demo/200/200', 'listener', 'google', 'demo-google-id')
ON CONFLICT DO NOTHING;

-- Sample users for each artist
INSERT INTO users (id, email, name, avatar_url, role, provider, provider_id)
SELECT gen_random_uuid(), 'artist' || n || '@zedstream.app', 'Artist ' || n, 'https://picsum.photos/seed/user' || n || '/200/200', 'artist', 'google', 'artist-google-' || n
FROM generate_series(1, 9) AS n
ON CONFLICT DO NOTHING;

-- Sample artists (linked to the users above, 1-to-1)
DO $$
DECLARE
  artist_data TEXT[][] := ARRAY[
    ['Macky 2', 'Zambian hip-hop legend from Kitwe', 'https://picsum.photos/seed/macky2/300/300'],
    ['Slapdee', 'Pioneer of Zambian hip-hop, founder of X Y Generation', 'https://picsum.photos/seed/slapdee/300/300'],
    ['Yo Maps', 'Award-winning Zambian artist from Ndola', 'https://picsum.photos/seed/yomaps/300/300'],
    ['Cleo Ice Queen', 'Zambian female rapper and singer', 'https://picsum.photos/seed/cleo/300/300'],
    ['Roberto', 'Zambian Afro-pop and R&B sensation', 'https://picsum.photos/seed/roberto/300/300'],
    ['B1', 'Kalindula and traditional Zambian artist', 'https://picsum.photos/seed/b1music/300/300'],
    ['T Sean', 'Zambian dancehall artist', 'https://picsum.photos/seed/tsean/300/300'],
    ['Dandy Krazy', 'Versatile Zambian musician', 'https://picsum.photos/seed/dandykrazy/300/300'],
    ['Lynn M', 'Afro-pop and gospel artist', 'https://picsum.photos/seed/lynmn/300/300']
  ];
  rec RECORD;
  i INT := 1;
BEGIN
  FOR rec IN SELECT id FROM users WHERE email LIKE 'artist%@zedstream.app' ORDER BY email LOOP
    INSERT INTO artists (id, user_id, stage_name, bio, photo_url, verified)
    VALUES (gen_random_uuid(), rec.id, artist_data[i][1], artist_data[i][2], artist_data[i][3], true);
    i := i + 1;
  END LOOP;
END $$;

-- Albums (requires artists from above)
INSERT INTO albums (id, artist_id, title, cover_url, type, status, released_at)
SELECT
  gen_random_uuid(), a.id, a.stage_name || ' - Album 1',
  'https://picsum.photos/seed/' || lower(replace(a.stage_name, ' ', '')) || '1/300/300',
  'album'::album_type, 'published'::album_status, NOW() - INTERVAL '30 days'
FROM artists a
UNION ALL
SELECT
  gen_random_uuid(), a.id, a.stage_name || ' - EP 1',
  'https://picsum.photos/seed/' || lower(replace(a.stage_name, ' ', '')) || 'ep/300/300',
  'ep'::album_type, 'published'::album_status, NOW() - INTERVAL '15 days'
FROM artists a
ON CONFLICT DO NOTHING;

-- Tracks: 3-5 tracks per artist with varied genres
INSERT INTO tracks (id, artist_id, album_id, title, duration_sec, genre_id, cover_url, audio_key, file_size, mime_type, status, released_at, play_count, like_count, track_order)
SELECT
  gen_random_uuid(),
  a.id,
  CASE WHEN random() < 0.6 THEN (SELECT id FROM albums WHERE artist_id = a.id ORDER BY random() LIMIT 1) ELSE NULL END,
  a.stage_name || ' - Track ' || t.n,
  180 + (random() * 180)::int,
  (SELECT id FROM genres ORDER BY random() LIMIT 1),
  'https://picsum.photos/seed/' || lower(replace(a.stage_name, ' ', '')) || 'track' || t.n || '/300/300',
  'tracks/' || a.id || '/sample_' || t.n || '.mp3',
  5000000 + (random() * 10000000)::bigint,
  'audio/mpeg',
  'published'::track_status,
  NOW() - (random() * INTERVAL '60 days'),
  (random() * 50000)::bigint,
  (random() * 500)::bigint,
  t.n
FROM artists a
CROSS JOIN LATERAL (SELECT generate_series(1, 3 + (random() * 3)::int) AS n) t
ON CONFLICT DO NOTHING;

-- Add some featured collaborators
INSERT INTO track_collaborators (track_id, artist_id, role)
SELECT
  t.id,
  ca.id,
  'featured'
FROM tracks t
JOIN LATERAL (SELECT id FROM artists WHERE id != t.artist_id ORDER BY random() LIMIT 1) ca ON true
WHERE random() < 0.3
ON CONFLICT DO NOTHING;

-- Add likes (simulate user engagement)
INSERT INTO likes (user_id, track_id)
SELECT
  u.id, t.id
FROM users u
CROSS JOIN tracks t
WHERE random() < 0.2
ON CONFLICT DO NOTHING;

-- Update denormalized like counts
UPDATE tracks t
SET like_count = (SELECT COUNT(*) FROM likes WHERE track_id = t.id);

-- Add play events (simulate listening history)
INSERT INTO play_events (id, user_id, track_id, duration_listened, played_at)
SELECT
  gen_random_uuid(),
  u.id,
  t.id,
  (t.duration_sec * (0.3 + random() * 0.7))::int,
  NOW() - (random() * INTERVAL '14 days')
FROM users u
CROSS JOIN tracks t
WHERE random() < 0.15
ON CONFLICT DO NOTHING;

-- Update denormalized play counts
UPDATE tracks t
SET play_count = (SELECT COUNT(*) FROM play_events WHERE track_id = t.id);

-- Add sample comments
INSERT INTO track_comments (id, track_id, user_id, body)
SELECT
  gen_random_uuid(),
  t.id,
  u.id,
  (ARRAY['Fire track! 🔥', 'This is amazing', 'Been on repeat all week', 'Zambian music at its best', 'Love this vibe'])[floor(random() * 5 + 1)]
FROM tracks t
CROSS JOIN users u
WHERE random() < 0.1
ON CONFLICT DO NOTHING;

-- Sample playlists
INSERT INTO playlists (id, user_id, title, description, is_public)
SELECT
  gen_random_uuid(),
  (SELECT id FROM users LIMIT 1),
  'Zambian Heat',
  'Top Zambian tracks right now',
  true
WHERE NOT EXISTS (SELECT 1 FROM playlists WHERE title = 'Zambian Heat');

INSERT INTO playlists (id, user_id, title, description, is_public)
SELECT
  gen_random_uuid(),
  (SELECT id FROM users LIMIT 1),
  'Chill Vibes',
  'Relax and unwind',
  true
WHERE NOT EXISTS (SELECT 1 FROM playlists WHERE title = 'Chill Vibes');

INSERT INTO playlists (id, user_id, title, description, is_public)
SELECT
  gen_random_uuid(),
  (SELECT id FROM users LIMIT 1),
  'Workout Mix',
  'High energy tracks for the gym',
  true
WHERE NOT EXISTS (SELECT 1 FROM playlists WHERE title = 'Workout Mix');
