-- Revert linking orders to merchants and order items table

DROP INDEX IF EXISTS idx_order_items_merchant;
DROP INDEX IF EXISTS idx_order_items_order;
DROP TABLE IF EXISTS order_items;

DROP INDEX IF EXISTS idx_orders_merchant_id;
ALTER TABLE orders DROP COLUMN IF EXISTS merchant_id;
