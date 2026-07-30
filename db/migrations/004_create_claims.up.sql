CREATE TYPE claim_status AS ENUM ('pending', 'under_review', 'approved', 'rejected');
CREATE TYPE verification_method AS ENUM ('social_media', 'manual_review');

CREATE TABLE artist_claims (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    artist_id         UUID NOT NULL REFERENCES artists(id) ON DELETE CASCADE,
    user_id           UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status            claim_status NOT NULL DEFAULT 'pending',
    method            verification_method NOT NULL,

    -- Social media verification
    social_platform   TEXT,
    social_post_url   TEXT,
    verification_code TEXT,

    -- Manual review
    document_keys     TEXT[],  -- R2 keys of uploaded ID documents
    notes             TEXT,

    -- Admin review
    reviewed_by       UUID REFERENCES users(id),
    reviewed_at       TIMESTAMPTZ,
    rejection_reason  TEXT,

    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE (artist_id)
);

CREATE INDEX idx_artist_claims_user_id   ON artist_claims(user_id);
CREATE INDEX idx_artist_claims_status    ON artist_claims(status);
CREATE INDEX idx_artist_claims_artist_id ON artist_claims(artist_id);
