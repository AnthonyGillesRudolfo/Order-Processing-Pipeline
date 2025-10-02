# Testing Database Integration

This guide provides step-by-step instructions to test the PostgreSQL database integration with the Order Processing Pipeline.

## Prerequisites

1. PostgreSQL installed and running
2. Database and user created (see DATABASE_SETUP.md)
3. Application built: `go build -o order-processing-pipeline .`

## Test Procedure

### Step 1: Start PostgreSQL

**macOS (Homebrew):**
```bash
brew services start postgresql@15
```

**Linux:**
```bash
sudo systemctl start postgresql
```

**Verify PostgreSQL is running:**
```bash
psql -U postgres -c "SELECT version();"
```

### Step 2: Create Database and User

```bash
# Run the setup script
psql -U postgres -f setup_database.sql

# Or manually:
psql -U postgres <<EOF
CREATE DATABASE orderpipeline;
CREATE USER orderpipelineadmin;
GRANT ALL PRIVILEGES ON DATABASE orderpipeline TO orderpipelineadmin;
\c orderpipeline
GRANT ALL ON SCHEMA public TO orderpipelineadmin;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL ON TABLES TO orderpipelineadmin;
EOF
```

### Step 3: Verify Database Setup

```bash
# Connect to the database
psql -U orderpipelineadmin -d orderpipeline

# Should connect without errors
# Type \q to quit
```

### Step 4: Start the Application

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

Service Architecture:
  - OrderService: WORKFLOW (keyed by order ID)
  - PaymentService: VIRTUAL OBJECT (keyed by payment ID)
  - ShippingService: VIRTUAL OBJECT (keyed by shipment ID)

Register with Restate:
  restate deployments register http://localhost:9081
```

### Step 5: Verify Tables Were Created

In another terminal:
```bash
psql -U orderpipelineadmin -d orderpipeline -c "\dt"
```

**Expected output:**
```
           List of relations
 Schema |   Name    | Type  |       Owner        
--------+-----------+-------+--------------------
 public | orders    | table | orderpipelineadmin
 public | payments  | table | orderpipelineadmin
 public | shipments | table | orderpipeline admin
```

### Step 6: Register with Restate

```bash
restate deployments register http://localhost:9081
```

### Step 7: Create a Test Order

```bash
# Start a workflow with order ID "test-order-001"
restate workflow start order.sv1.OrderService/test-order-001 CreateOrder \
  --input '{
    "customer_id": "customer-123",
    "items": [
      {"product_id": "prod-001", "quantity": 2},
      {"product_id": "prod-002", "quantity": 1}
    ]
  }'
```

**Expected output:**
```json
{
  "order_id": "test-order-001"
}
```

### Step 8: Verify Data in Database

```bash
# Connect to database
psql -U orderpipelineadmin -d orderpipeline
```

**Check orders table:**
```sql
SELECT * FROM orders;
```

**Expected result:**
```
       id        |  customer_id  |  status   | total_amount | payment_id | shipment_id | tracking_number |         created_at         
-----------------+---------------+-----------+--------------+------------+-------------+-----------------+----------------------------
 test-order-001  | customer-123  | COMPLETED |        30.00 | <uuid>     | <uuid>      | TRACK-<id>      | 2025-10-02 ...
```

**Check payments table:**
```sql
SELECT * FROM payments;
```

**Expected result:**
```
       id        |    order_id     | amount | payment_method |      status       
-----------------+-----------------+--------+----------------+-------------------
 <uuid>          | test-order-001  |  30.00 | CREDIT_CARD    | PAYMENT_COMPLETED
```

**Check shipments table:**
```sql
SELECT * FROM shipments;
```

**Expected result:**
```
       id        |    order_id     | tracking_number | carrier |  status          | current_location
-----------------+-----------------+-----------------+---------+------------------+-----------------
 <uuid>          | test-order-001  | TRACK-<id>      | FedEx   | SHIPMENT_CREATED | Warehouse
```

### Step 9: Query Order with Joins

```sql
SELECT 
    o.id as order_id,
    o.customer_id,
    o.status as order_status,
    o.total_amount,
    p.id as payment_id,
    p.payment_method,
    p.status as payment_status,
    s.id as shipment_id,
    s.tracking_number,
    s.carrier,
    s.status as shipment_status,
    o.created_at
FROM orders o
LEFT JOIN payments p ON o.payment_id = p.id
LEFT JOIN shipments s ON o.shipment_id = s.id
WHERE o.id = 'test-order-001';
```

### Step 10: Create Multiple Orders

```bash
# Create order 2
restate workflow start order.sv1.OrderService/test-order-002 CreateOrder \
  --input '{"customer_id": "customer-456", "items": [{"product_id": "prod-003", "quantity": 5}]}'

# Create order 3
restate workflow start order.sv1.OrderService/test-order-003 CreateOrder \
  --input '{"customer_id": "customer-123", "items": [{"product_id": "prod-001", "quantity": 1}]}'
```

### Step 11: Run Analytics Queries

**Count orders by status:**
```sql
SELECT status, COUNT(*) as count
FROM orders
GROUP BY status;
```

**Total revenue:**
```sql
SELECT SUM(total_amount) as total_revenue
FROM orders
WHERE status = 'COMPLETED';
```

**Orders by customer:**
```sql
SELECT customer_id, COUNT(*) as order_count, SUM(total_amount) as total_spent
FROM orders
GROUP BY customer_id
ORDER BY total_spent DESC;
```

**Payment success rate:**
```sql
SELECT 
    status,
    COUNT(*) as count,
    ROUND(COUNT(*) * 100.0 / SUM(COUNT(*)) OVER (), 2) as percentage
FROM payments
GROUP BY status;
```

## Troubleshooting

### Application Shows Database Warning

**Symptom:**
```
WARNING: Failed to connect to database: <error>
Continuing without database persistence...
```

**Solutions:**

1. **Check PostgreSQL is running:**
   ```bash
   pg_isready
   ```

2. **Check database exists:**
   ```bash
   psql -U postgres -c "\l" | grep orderpipeline
   ```

3. **Check user exists:**
   ```bash
   psql -U postgres -c "\du" | grep orderpipelineadmin
   ```

4. **Test connection manually:**
   ```bash
   psql -U orderpipelineadmin -d orderpipeline -c "SELECT 1;"
   ```

### Tables Not Created

**Symptom:** `\dt` shows no tables

**Solution:** Check application logs for table creation errors. Ensure user has proper privileges:
```sql
GRANT ALL ON SCHEMA public TO orderpipelineadmin;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL ON TABLES TO orderpipelineadmin;
```

### No Data in Tables

**Symptom:** Tables exist but are empty after creating orders

**Possible causes:**
1. Database writes failed (check application logs)
2. Order creation failed before database writes
3. Restate workflow didn't complete

**Check workflow status:**
```bash
restate workflow get order.sv1.OrderService/test-order-001 GetOrder \
  --input '{"order_id": "test-order-001"}'
```

### Authentication Errors

**Symptom:** `password authentication failed`

**Solution:** Configure `pg_hba.conf` for trust authentication:
```
# Add this line
local   orderpipeline   orderpipelineadmin   trust
host    orderpipeline   orderpipelineadmin   127.0.0.1/32   trust
```

Then restart PostgreSQL:
```bash
brew services restart postgresql@15  # macOS
sudo systemctl restart postgresql    # Linux
```

## Cleanup

### Delete Test Data

```sql
-- Connect to database
psql -U orderpipelineadmin -d orderpipeline

-- Delete all data
DELETE FROM orders;
DELETE FROM payments;
DELETE FROM shipments;

-- Verify
SELECT COUNT(*) FROM orders;
SELECT COUNT(*) FROM payments;
SELECT COUNT(*) FROM shipments;
```

### Drop Database (Complete Reset)

```bash
psql -U postgres <<EOF
DROP DATABASE IF EXISTS orderpipeline;
DROP USER IF EXISTS orderpipelineadmin;
EOF
```

Then recreate using `setup_database.sql`.

## Success Criteria

✅ Application starts without database errors
✅ Tables are created automatically
✅ Orders are persisted to database
✅ Payments are persisted to database
✅ Shipments are persisted to database
✅ Data can be queried with SQL joins
✅ Application continues running if database is unavailable

## Next Steps

- Test database failover scenarios
- Test concurrent order creation
- Benchmark database performance
- Set up database monitoring
- Configure automated backups

