-- Remove description column from merchant_items table

ALTER TABLE merchant_items 
DROP COLUMN IF EXISTS description;
