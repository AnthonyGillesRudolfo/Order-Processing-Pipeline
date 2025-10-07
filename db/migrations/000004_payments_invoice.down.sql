-- Remove invoice fields from payments
ALTER TABLE payments
DROP COLUMN IF EXISTS invoice_url,
DROP COLUMN IF EXISTS xendit_invoice_id;

DROP INDEX IF EXISTS idx_payments_xendit_invoice_id;

