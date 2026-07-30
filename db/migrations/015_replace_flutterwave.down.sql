ALTER TABLE credit_payments RENAME COLUMN moneyunify_tx_ref TO flutterwave_tx_ref;
ALTER TABLE credit_payments RENAME COLUMN moneyunify_transaction_id TO flutterwave_transaction_id;

DROP INDEX IF EXISTS idx_credit_payments_mu_tx_ref;
CREATE INDEX IF NOT EXISTS idx_credit_payments_tx_ref ON credit_payments(flutterwave_tx_ref);
