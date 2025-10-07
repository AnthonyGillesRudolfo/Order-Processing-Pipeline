-- Remove awakeable_id column from orders table
DROP INDEX IF EXISTS idx_orders_awakeable_id;

ALTER TABLE orders
DROP COLUMN IF EXISTS awakeable_id;


