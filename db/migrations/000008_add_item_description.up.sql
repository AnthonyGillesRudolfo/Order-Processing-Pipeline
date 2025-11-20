-- Add description column to merchant_items table

ALTER TABLE merchant_items 
ADD COLUMN IF NOT EXISTS description TEXT;

-- Update existing items with descriptions
UPDATE merchant_items SET description = 'Premium wireless headphones with noise cancellation and 30-hour battery life' WHERE item_id = 'i_001';
UPDATE merchant_items SET description = 'Latest flagship smartphone with 5G, 128GB storage, and triple camera system' WHERE item_id = 'i_002';
UPDATE merchant_items SET description = 'High-performance laptop with 16GB RAM, 512GB SSD, and dedicated graphics' WHERE item_id = 'i_003';
UPDATE merchant_items SET description = '10-inch tablet with stylus support, perfect for productivity and entertainment' WHERE item_id = 'i_004';
UPDATE merchant_items SET description = 'Fitness tracker with heart rate monitor, GPS, and water resistance' WHERE item_id = 'i_005';

UPDATE merchant_items SET description = 'Premium denim jeans with modern fit and durable construction' WHERE item_id = 'f_001';
UPDATE merchant_items SET description = 'Soft 100% cotton t-shirt, available in multiple colors' WHERE item_id = 'f_002';
UPDATE merchant_items SET description = 'Lightweight running shoes with cushioned sole and breathable mesh' WHERE item_id = 'f_003';
UPDATE merchant_items SET description = 'Insulated winter jacket with waterproof exterior and warm lining' WHERE item_id = 'f_004';
UPDATE merchant_items SET description = 'UV protection sunglasses with polarized lenses and stylish frames' WHERE item_id = 'f_005';

UPDATE merchant_items SET description = 'Comprehensive programming guide covering algorithms, data structures, and best practices' WHERE item_id = 'b_001';
UPDATE merchant_items SET description = 'Bestselling fiction novel with captivating story and memorable characters' WHERE item_id = 'b_002';
UPDATE merchant_items SET description = 'Collection of delicious recipes from around the world with step-by-step instructions' WHERE item_id = 'b_003';
UPDATE merchant_items SET description = 'Engaging history book exploring major events and civilizations' WHERE item_id = 'b_004';
UPDATE merchant_items SET description = 'Illustrated children''s book with fun stories and educational content' WHERE item_id = 'b_005';

UPDATE merchant_items SET description = 'Curated collection of interesting and educational books' WHERE item_id = 'test-1';
UPDATE merchant_items SET description = 'Stylish and durable bags for everyday use and travel' WHERE item_id = 'test-123';
