-- Merchants and Items schema

-- merchants
CREATE TABLE IF NOT EXISTS merchants (
    merchant_id VARCHAR(255) PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- merchant_items
CREATE TABLE IF NOT EXISTS merchant_items (
    merchant_id VARCHAR(255) NOT NULL REFERENCES merchants(merchant_id) ON DELETE CASCADE,
    item_id VARCHAR(255) NOT NULL,
    name VARCHAR(255) NOT NULL,
    quantity INTEGER NOT NULL DEFAULT 999,
    price DECIMAL(10, 2) NOT NULL DEFAULT 0,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (merchant_id, item_id)
);

CREATE INDEX IF NOT EXISTS idx_merchant_items_merchant ON merchant_items(merchant_id);
