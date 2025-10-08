-- Add sample data for testing

-- Insert sample merchants
INSERT INTO merchants (merchant_id, name) VALUES 
('m_001', 'Tech Store'),
('m_002', 'Fashion Hub'),
('m_003', 'Book Paradise')
ON CONFLICT (merchant_id) DO NOTHING;

-- Insert sample items for m_001 (Tech Store)
INSERT INTO merchant_items (merchant_id, item_id, name, quantity, price) VALUES 
('m_001', 'i_001', 'Wireless Headphones', 50, 100.00),
('m_001', 'i_002', 'Smartphone', 25, 700.00),
('m_001', 'i_003', 'Laptop', 10, 1300.00),
('m_001', 'i_004', 'Tablet', 15, 400.00),
('m_001', 'i_005', 'Smart Watch', 30, 200.00)
ON CONFLICT (merchant_id, item_id) DO UPDATE SET
    name = EXCLUDED.name,
    quantity = EXCLUDED.quantity,
    price = EXCLUDED.price,
    updated_at = CURRENT_TIMESTAMP;

-- Insert sample items for m_002 (Fashion Hub)
INSERT INTO merchant_items (merchant_id, item_id, name, quantity, price) VALUES 
('m_002', 'f_001', 'Designer Jeans', 100, 90.00),
('m_002', 'f_002', 'Cotton T-Shirt', 200, 25.00),
('m_002', 'f_003', 'Running Shoes', 75, 130.00),
('m_002', 'f_004', 'Winter Jacket', 40, 200.00),
('m_002', 'f_005', 'Sunglasses', 60, 80.00)
ON CONFLICT (merchant_id, item_id) DO UPDATE SET
    name = EXCLUDED.name,
    quantity = EXCLUDED.quantity,
    price = EXCLUDED.price,
    updated_at = CURRENT_TIMESTAMP;

-- Insert sample items for m_003 (Book Paradise)
INSERT INTO merchant_items (merchant_id, item_id, name, quantity, price) VALUES 
('m_003', 'b_001', 'Programming Book', 50, 50.00),
('m_003', 'b_002', 'Fiction Novel', 100, 20.00),
('m_003', 'b_003', 'Cookbook', 75, 30.00),
('m_003', 'b_004', 'History Book', 60, 35.00),
('m_003', 'b_005', 'Children Book', 120, 15.00)
ON CONFLICT (merchant_id, item_id) DO UPDATE SET
    name = EXCLUDED.name,
    quantity = EXCLUDED.quantity,
    price = EXCLUDED.price,
    updated_at = CURRENT_TIMESTAMP;
