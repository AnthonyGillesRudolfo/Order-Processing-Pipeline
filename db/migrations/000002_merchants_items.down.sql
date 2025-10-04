-- Revert merchants and items schema

DROP INDEX IF EXISTS idx_merchant_items_merchant;
DROP TABLE IF EXISTS merchant_items;
DROP TABLE IF EXISTS merchants;
