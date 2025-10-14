-- Drop indexes first
DROP INDEX IF EXISTS idx_ap2_refunds_execution_id;
DROP INDEX IF EXISTS idx_ap2_authorizations_intent_id;
DROP INDEX IF EXISTS idx_ap2_executions_payment_id;
DROP INDEX IF EXISTS idx_ap2_executions_order_id;
DROP INDEX IF EXISTS idx_ap2_executions_intent_id;
DROP INDEX IF EXISTS idx_ap2_intents_mandate_id;
DROP INDEX IF EXISTS idx_ap2_intents_customer_id;
DROP INDEX IF EXISTS idx_ap2_mandates_status;
DROP INDEX IF EXISTS idx_ap2_mandates_customer_id;

-- Drop tables in reverse order of creation
DROP TABLE IF EXISTS shipping_preferences;
DROP TABLE IF EXISTS ap2_refunds;
DROP TABLE IF EXISTS ap2_authorizations;
DROP TABLE IF EXISTS ap2_executions;
DROP TABLE IF EXISTS ap2_intents;
DROP TABLE IF EXISTS ap2_mandates;
