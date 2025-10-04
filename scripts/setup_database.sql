-- Order Processing Pipeline Database Setup
-- This script creates the database, user, and grants necessary permissions

-- Connect to PostgreSQL as superuser (postgres) and run these commands:

-- Create the database
CREATE DATABASE orderpipeline;

-- Create the user (with no password as specified)
CREATE USER orderpipelineadmin;

-- Grant all privileges on the database to the user
GRANT ALL PRIVILEGES ON DATABASE orderpipeline TO orderpipelineadmin;

-- Connect to the orderpipeline database
\c orderpipeline

-- Grant schema privileges
GRANT ALL ON SCHEMA public TO orderpipelineadmin;

-- Grant default privileges for future tables
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL ON TABLES TO orderpipelineadmin;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL ON SEQUENCES TO orderpipelineadmin;

-- The application will automatically create the following tables on startup (until migrations run):
-- 1. orders - Stores order information
-- 2. payments - Stores payment transactions
-- 3. shipments - Stores shipment tracking information

-- To verify the setup, you can run:
-- \l                          -- List databases
-- \du                         -- List users
-- \c orderpipeline            -- Connect to database
-- \dt                         -- List tables (after running the application)
