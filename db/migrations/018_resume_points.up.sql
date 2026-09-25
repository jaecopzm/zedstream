-- Resume points: where each user left off in each track (Jump Back In).
CREATE TABLE resume_points (
    user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    track_id     TEXT NOT NULL REFERENCES tracks(id) ON DELETE CASCADE,
    position_sec INT NOT NULL DEFAULT 0 CHECK (position_sec >= 0),
    duration_sec INT NOT NULL DEFAULT 0 CHECK (duration_sec >= 0),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, track_id)
);

CREATE INDEX idx_resume_points_user_updated
    ON resume_points (user_id, updated_at DESC);
