-- Rollback: Remove schema_migrations initialization
-- This doesn't actually drop the schema_migrations table since golang-migrate needs it
-- Just removes the entries for migrations 1-7 if they were added by the up migration

DELETE FROM schema_migrations WHERE version IN (1, 2, 3, 4, 5, 6, 7);
