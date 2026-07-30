-- Rename Flutterwave columns to MoneyUnify in credit_payments
ALTER TABLE credit_payments RENAME COLUMN flutterwave_tx_ref TO moneyunify_tx_ref;
ALTER TABLE credit_payments RENAME COLUMN flutterwave_transaction_id TO moneyunify_transaction_id;

DROP INDEX IF EXISTS idx_credit_payments_tx_ref;
CREATE INDEX IF NOT EXISTS idx_credit_payments_mu_tx_ref ON credit_payments(moneyunify_tx_ref);
