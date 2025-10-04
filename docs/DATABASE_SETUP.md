# PostgreSQL Database Setup Guide

## Overview

The Order Processing Pipeline now includes PostgreSQL integration for persistent storage of orders, payments, and shipments. This provides long-term data persistence and enables complex querying beyond what Restate's built-in state management offers.

## Architecture

### Dual Storage Strategy

The application uses a **dual storage approach**:

1. **Restate State** (In-memory, fast access)
   - Used for workflow/object state during execution
   - Provides fast access to current state
   - Automatically managed by Restate
   - Survives restarts through Restate's journal

2. **PostgreSQL Database** (Persistent, queryable)
   - Long-term storage for historical records
   - Enables complex queries and reporting
   - Provides audit trail
   - Survives application restarts

### Database Operations

All database writes are wrapped in `restate.Run()` blocks to ensure:
- **Durability**: Operations are journaled and can be retried
- **Exactly-once execution**: No duplicate writes on retries
- **Automatic recovery**: Failed operations are retried automatically

## Prerequisites

1. **PostgreSQL** installed and running
   - Version 12 or higher recommended
   - Default port: 5432

2. **Database Configuration**
   - Database name: `orderpipeline`
   - Username: `orderpipelineadmin`
   - Password: (empty/none)
   - Host: `localhost`
   - Port: `5432`

## Installation Steps

### 1. Install PostgreSQL

**macOS (using Homebrew):**
```bash
brew install postgresql@15
brew services start postgresql@15
```

**Ubuntu/Debian:**
```bash
sudo apt update
sudo apt install postgresql postgresql-contrib
sudo systemctl start postgresql
```

**Windows:**
Download and install from https://www.postgresql.org/download/windows/

### 2. Create Database and User

Connect to PostgreSQL as the superuser:
```bash
psql -U postgres
```

Run the setup script:
```bash
psql -U postgres -f scripts/setup_database.sql
```

Or manually execute:
```sql
-- Create database
CREATE DATABASE orderpipeline;

-- Create user (no password)
CREATE USER orderpipelineadmin;

-- Grant privileges
GRANT ALL PRIVILEGES ON DATABASE orderpipeline TO orderpipelineadmin;

-- Connect to the database
\c orderpipeline

-- Grant schema privileges
GRANT ALL ON SCHEMA public TO orderpipelineadmin;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL ON TABLES TO orderpipelineadmin;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL ON SEQUENCES TO orderpipelineadmin;
```

### 3. Verify Setup

```bash
# Connect to the database
psql -U orderpipelineadmin -d orderpipeline

# List databases
\l

# List tables (will be empty until migrations run or app bootstraps)
\dt
```

### 4. Configure PostgreSQL Authentication (if needed)

If you encounter authentication errors, configure `pg_hba.conf`:

**Add this line for local connections without password:**
```
local   orderpipeline   orderpipelineadmin   trust
host    orderpipeline   orderpipelineadmin   127.0.0.1/32   trust
```

Restart PostgreSQL after changes.

## Database Schema

Schema is now managed via migrations in `db/migrations/`.

Tables created in initial migration:
- `orders`
- `payments`
- `shipments`

## Running the Application

```bash
make proto
make run
```

Expected output includes DB connection and Restate server listening on `:9081`.

## Querying the Database

Examples:
```sql
SELECT * FROM orders ORDER BY created_at DESC;
SELECT status, COUNT(*) FROM orders GROUP BY status;
```

## Troubleshooting
- Ensure PostgreSQL is running.
- Ensure database/user exist and have privileges.
- Check application logs for connection or migration errors.
