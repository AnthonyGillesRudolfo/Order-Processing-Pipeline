-- Rollback: Clear all item descriptions
UPDATE merchant_items SET description = NULL;
