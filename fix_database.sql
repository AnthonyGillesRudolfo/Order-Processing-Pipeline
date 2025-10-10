-- Fix database schema for Xendit invoice support
-- Run this manually if migrations aren't working

-- Add missing columns to payments table
ALTER TABLE payments 
ADD COLUMN IF NOT EXISTS invoice_url TEXT,
ADD COLUMN IF NOT EXISTS xendit_invoice_id TEXT;

-- Add missing column to orders table  
ALTER TABLE orders
ADD COLUMN IF NOT EXISTS awakeable_id TEXT;

-- Create indexes
CREATE INDEX IF NOT EXISTS idx_payments_xendit_invoice_id ON payments (xendit_invoice_id);
CREATE INDEX IF NOT EXISTS idx_orders_awakeable_id ON orders (awakeable_id);

-- Verify the changes
SELECT column_name, data_type 
FROM information_schema.columns 
WHERE table_name = 'payments' 
AND column_name IN ('invoice_url', 'xendit_invoice_id');

SELECT column_name, data_type 
FROM information_schema.columns 
WHERE table_name = 'orders' 
AND column_name = 'awakeable_id';
