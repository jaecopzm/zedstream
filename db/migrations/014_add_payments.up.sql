CREATE TABLE IF NOT EXISTS credit_payments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    artist_id UUID NOT NULL REFERENCES artists(id),
    credit_amount INTEGER NOT NULL,
    zmw_amount INTEGER NOT NULL,
    currency TEXT NOT NULL DEFAULT 'ZMW',
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'completed', 'failed')),
    flutterwave_tx_ref TEXT NOT NULL UNIQUE,
    flutterwave_transaction_id TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_credit_payments_artist_id ON credit_payments(artist_id);
CREATE INDEX IF NOT EXISTS idx_credit_payments_tx_ref ON credit_payments(flutterwave_tx_ref);
