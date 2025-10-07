-- Add invoice fields to payments
ALTER TABLE payments
ADD COLUMN IF NOT EXISTS invoice_url TEXT,
ADD COLUMN IF NOT EXISTS xendit_invoice_id TEXT;

-- Index for quick lookup by xendit invoice id (optional)
CREATE INDEX IF NOT EXISTS idx_payments_xendit_invoice_id ON payments (xendit_invoice_id);

