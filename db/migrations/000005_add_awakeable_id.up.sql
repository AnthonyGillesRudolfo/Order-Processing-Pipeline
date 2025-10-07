-- Add awakeable_id column to orders table for workflow continuation
ALTER TABLE orders
ADD COLUMN IF NOT EXISTS awakeable_id TEXT;

CREATE INDEX IF NOT EXISTS idx_orders_awakeable_id ON orders(awakeable_id);


