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
Download and install from [postgresql.org](https://www.postgresql.org/download/windows/)

### 2. Create Database and User

Connect to PostgreSQL as the superuser:
```bash
psql -U postgres
```

Run the setup script:
```bash
psql -U postgres -f setup_database.sql
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

# List tables (will be empty until application runs)
\dt
```

### 4. Configure PostgreSQL Authentication (if needed)

If you encounter authentication errors, you may need to configure `pg_hba.conf`:

**Location:**
- macOS (Homebrew): `/opt/homebrew/var/postgresql@15/pg_hba.conf`
- Linux: `/etc/postgresql/15/main/pg_hba.conf`
- Windows: `C:\Program Files\PostgreSQL\15\data\pg_hba.conf`

**Add this line for local connections without password:**
```
local   orderpipeline   orderpipelineadmin   trust
host    orderpipeline   orderpipelineadmin   127.0.0.1/32   trust
```

**Restart PostgreSQL after changes:**
```bash
# macOS (Homebrew)
brew services restart postgresql@15

# Linux
sudo systemctl restart postgresql

# Windows
# Use Services app to restart PostgreSQL service
```

## Database Schema

The application automatically creates three tables on startup:

### 1. `orders` Table
```sql
CREATE TABLE orders (
    id VARCHAR(255) PRIMARY KEY,
    customer_id VARCHAR(255) NOT NULL,
    status VARCHAR(50) NOT NULL,
    total_amount DECIMAL(10, 2),
    payment_id VARCHAR(255),
    shipment_id VARCHAR(255),
    tracking_number VARCHAR(255),
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

**Indexes:**
- `idx_orders_customer_id` on `customer_id`
- `idx_orders_status` on `status`

### 2. `payments` Table
```sql
CREATE TABLE payments (
    id VARCHAR(255) PRIMARY KEY,
    order_id VARCHAR(255) NOT NULL,
    amount DECIMAL(10, 2) NOT NULL,
    payment_method VARCHAR(50),
    status VARCHAR(50) NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

**Indexes:**
- `idx_payments_order_id` on `order_id`
- `idx_payments_status` on `status`

### 3. `shipments` Table
```sql
CREATE TABLE shipments (
    id VARCHAR(255) PRIMARY KEY,
    order_id VARCHAR(255) NOT NULL,
    tracking_number VARCHAR(255) NOT NULL,
    carrier VARCHAR(100),
    service_type VARCHAR(100),
    status VARCHAR(50) NOT NULL,
    current_location VARCHAR(255),
    estimated_delivery DATE,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

**Indexes:**
- `idx_shipments_order_id` on `order_id`
- `idx_shipments_tracking_number` on `tracking_number`
- `idx_shipments_status` on `status`

## Running the Application

### 1. Start the Application

```bash
./order-processing-pipeline
```

**Expected output:**
```
Starting Order Processing Pipeline...
Connecting to PostgreSQL database...
Successfully connected to PostgreSQL database: orderpipeline
Database connection established successfully
Database tables created successfully
Restate server listening on :9081
...
```

### 2. If Database Connection Fails

The application will continue running without database persistence:
```
WARNING: Failed to connect to database: <error>
Continuing without database persistence...
```

**Common issues:**
- PostgreSQL not running: `brew services start postgresql@15`
- Database doesn't exist: Run `setup_database.sql`
- Authentication error: Check `pg_hba.conf` configuration
- Wrong port: Verify PostgreSQL is running on port 5432

## Querying the Database

### View All Orders
```sql
SELECT * FROM orders ORDER BY created_at DESC;
```

### View Orders by Status
```sql
SELECT * FROM orders WHERE status = 'COMPLETED';
```

### View Orders with Payment and Shipment Info
```sql
SELECT 
    o.id,
    o.customer_id,
    o.status,
    o.total_amount,
    p.payment_method,
    p.status as payment_status,
    s.tracking_number,
    s.carrier,
    s.status as shipment_status
FROM orders o
LEFT JOIN payments p ON o.payment_id = p.id
LEFT JOIN shipments s ON o.shipment_id = s.id
ORDER BY o.created_at DESC;
```

### View Payment Statistics
```sql
SELECT 
    status,
    COUNT(*) as count,
    SUM(amount) as total_amount
FROM payments
GROUP BY status;
```

### View Shipment Status Distribution
```sql
SELECT 
    status,
    COUNT(*) as count
FROM shipments
GROUP BY status;
```

## Troubleshooting

### Connection Refused
```
Error: connection refused
```
**Solution:** Ensure PostgreSQL is running
```bash
brew services start postgresql@15  # macOS
sudo systemctl start postgresql    # Linux
```

### Database Does Not Exist
```
Error: database "orderpipeline" does not exist
```
**Solution:** Create the database
```bash
psql -U postgres -c "CREATE DATABASE orderpipeline;"
```

### Authentication Failed
```
Error: password authentication failed for user "orderpipelineadmin"
```
**Solution:** Configure `pg_hba.conf` to use `trust` authentication for local connections

### Permission Denied
```
Error: permission denied for schema public
```
**Solution:** Grant proper privileges
```sql
GRANT ALL ON SCHEMA public TO orderpipelineadmin;
```

## Configuration

To change database configuration, modify the `DatabaseConfig` in `main.go`:

```go
dbConfig := DatabaseConfig{
    Host:     "localhost",      // Change to your PostgreSQL host
    Port:     5432,             // Change to your PostgreSQL port
    Database: "orderpipeline",  // Change database name
    User:     "orderpipelineadmin", // Change username
    Password: "",               // Add password if needed
}
```

## Benefits of Database Integration

1. **Persistent Storage**: Data survives application restarts
2. **Complex Queries**: SQL queries for reporting and analytics
3. **Audit Trail**: Complete history of all orders, payments, and shipments
4. **Data Export**: Easy to export data for external systems
5. **Backup & Recovery**: Standard PostgreSQL backup tools
6. **Scalability**: PostgreSQL can handle millions of records
7. **Compliance**: Meet data retention requirements

## Next Steps

- Set up database backups
- Add database connection pooling configuration
- Implement database migrations for schema changes
- Add database monitoring and alerting
- Consider read replicas for scaling queries

