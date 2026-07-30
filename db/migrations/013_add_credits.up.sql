-- Artist credit balance (one row per artist)
CREATE TABLE artist_credits (
    artist_id        TEXT PRIMARY KEY REFERENCES artists(id) ON DELETE CASCADE,
    balance          INTEGER NOT NULL DEFAULT 0,
    lifetime_granted INTEGER NOT NULL DEFAULT 0,
    lifetime_purchased INTEGER NOT NULL DEFAULT 0,
    lifetime_spent   INTEGER NOT NULL DEFAULT 0,
    lifetime_refunded INTEGER NOT NULL DEFAULT 0,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Ledger of every credit movement
CREATE TABLE credit_transactions (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    artist_id     TEXT NOT NULL REFERENCES artists(id) ON DELETE CASCADE,
    amount        INTEGER NOT NULL, -- positive for grants/purchases, negative for deductions
    type          TEXT NOT NULL,    -- 'free_grant' | 'purchase' | 'deduction' | 'admin_grant' | 'admin_revoke'
    description   TEXT,
    reference_id  TEXT,            -- e.g. track_id for deductions, payment ref for purchases
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_credit_transactions_artist_id ON credit_transactions (artist_id, created_at DESC);

-- Helper function to atomically adjust balance + log a transaction.
-- Returns the new balance.
-- Raises an exception if a deduction would make the balance negative.
CREATE OR REPLACE FUNCTION apply_credit_transaction(
    p_artist_id    TEXT,
    p_amount       INTEGER,
    p_type         TEXT,
    p_description  TEXT DEFAULT NULL,
    p_reference_id TEXT DEFAULT NULL
) RETURNS INTEGER AS $$
DECLARE
    new_balance INTEGER;
BEGIN
    -- Insert the ledger row first (FK keeps it consistent).
    INSERT INTO credit_transactions (artist_id, amount, type, description, reference_id)
    VALUES (p_artist_id, p_amount, p_type, p_description, p_reference_id);

    -- Upsert the balance row, bumping counters based on the type.
    INSERT INTO artist_credits AS ac (artist_id, balance, created_at, updated_at)
    VALUES (p_artist_id, p_amount, NOW(), NOW())
    ON CONFLICT (artist_id) DO UPDATE
        SET balance         = ac.balance + EXCLUDED.balance,
            lifetime_granted    = ac.lifetime_granted + CASE WHEN p_type IN ('free_grant', 'admin_grant') AND p_amount > 0 THEN p_amount ELSE 0 END,
            lifetime_purchased  = ac.lifetime_purchased + CASE WHEN p_type = 'purchase' AND p_amount > 0 THEN p_amount ELSE 0 END,
            lifetime_spent      = ac.lifetime_spent     + CASE WHEN p_type = 'deduction' AND p_amount < 0 THEN -p_amount ELSE 0 END,
            lifetime_refunded   = ac.lifetime_refunded  + CASE WHEN p_type = 'refund' AND p_amount > 0 THEN p_amount ELSE 0 END,
            updated_at          = NOW()
    RETURNING balance INTO new_balance;

    IF new_balance < 0 THEN
        RAISE EXCEPTION 'insufficient credits: balance would be %', new_balance;
    END IF;

    RETURN new_balance;
END;
$$ LANGUAGE plpgsql;
