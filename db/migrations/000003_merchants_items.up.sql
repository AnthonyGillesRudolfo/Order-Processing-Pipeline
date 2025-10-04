-- Link orders to merchants and record ordered items

-- 1) Add merchant_id to orders
ALTER TABLE orders
  ADD COLUMN IF NOT EXISTS merchant_id VARCHAR(255);
CREATE INDEX IF NOT EXISTS idx_orders_merchant_id ON orders(merchant_id);

-- 2) Order items snapshot table
CREATE TABLE IF NOT EXISTS order_items (
  order_id VARCHAR(255) NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
  item_id VARCHAR(255) NOT NULL,
  merchant_id VARCHAR(255),
  name VARCHAR(255),
  quantity INTEGER NOT NULL,
  unit_price DECIMAL(10, 2) NOT NULL DEFAULT 0,
  subtotal DECIMAL(10, 2) NOT NULL DEFAULT 0,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (order_id, item_id)
);

CREATE INDEX IF NOT EXISTS idx_order_items_order ON order_items(order_id);
CREATE INDEX IF NOT EXISTS idx_order_items_merchant ON order_items(merchant_id);
