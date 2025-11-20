-- Initialize schema_migrations table for tracking
-- This migration sets up the tracking system for golang-migrate
-- If migrations 1-7 were already applied manually, mark them as done

-- Create the schema_migrations table (golang-migrate will also try to create this, but we ensure it exists)
CREATE TABLE IF NOT EXISTS schema_migrations (
    version bigint NOT NULL PRIMARY KEY,
    dirty boolean NOT NULL
);

-- Check if any migrations are tracked, if not, mark 1-7 as already applied
-- This handles the case where the database was set up manually
DO $$
BEGIN
    -- Only insert if schema_migrations is empty (fresh setup from manual creation)
    IF NOT EXISTS (SELECT 1 FROM schema_migrations) THEN
        -- Check if the tables from migration 7 exist (ap2_mandates, etc)
        IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'ap2_mandates') THEN
            -- Database was already set up manually, mark migrations 1-7 as applied
            INSERT INTO schema_migrations (version, dirty) VALUES (1, false);
            INSERT INTO schema_migrations (version, dirty) VALUES (2, false);
            INSERT INTO schema_migrations (version, dirty) VALUES (3, false);
            INSERT INTO schema_migrations (version, dirty) VALUES (4, false);
            INSERT INTO schema_migrations (version, dirty) VALUES (5, false);
            INSERT INTO schema_migrations (version, dirty) VALUES (6, false);
            INSERT INTO schema_migrations (version, dirty) VALUES (7, false);
            RAISE NOTICE 'Detected existing database schema - marked migrations 1-7 as applied';
        END IF;
    END IF;
END $$;
