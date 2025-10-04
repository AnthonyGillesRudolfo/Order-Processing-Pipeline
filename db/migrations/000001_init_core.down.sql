-- Revert initial core schema

DROP INDEX IF EXISTS idx_shipments_status;
DROP INDEX IF EXISTS idx_shipments_tracking_number;
DROP INDEX IF EXISTS idx_shipments_order_id;
DROP TABLE IF EXISTS shipments;

DROP INDEX IF EXISTS idx_payments_status;
DROP INDEX IF EXISTS idx_payments_order_id;
DROP TABLE IF EXISTS payments;

DROP INDEX IF EXISTS idx_orders_status;
DROP INDEX IF EXISTS idx_orders_customer_id;
DROP TABLE IF EXISTS orders;

