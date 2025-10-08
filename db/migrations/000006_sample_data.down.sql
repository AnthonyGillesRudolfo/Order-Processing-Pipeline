-- Remove sample data

-- Remove sample items
DELETE FROM merchant_items WHERE merchant_id IN ('m_001', 'm_002', 'm_003');

-- Remove sample merchants
DELETE FROM merchants WHERE merchant_id IN ('m_001', 'm_002', 'm_003');
