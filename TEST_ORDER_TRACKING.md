# Testing Order Status Tracking System

This guide provides step-by-step instructions to test the comprehensive order status tracking system.

## Expected Behavior

When you create an order, it should automatically progress through these statuses with observable delays:

1. **PENDING** (~5 seconds) - Order created, payment processing
2. **PROCESSING** (~5 seconds) - Payment completed, preparing shipment
3. **SHIPPED** (~10 seconds) - Shipment in transit, delivery in progress
4. **DELIVERED** (final) - Order completed successfully

**Total workflow time: ~20 seconds** (plus additional time if payment retries are needed)

Each status is observable for several seconds, allowing you to query the order and see its current state.

## Prerequisites

1. Application built: `go build -o order-processing-pipeline .`
2. PostgreSQL database running and configured
3. Restate server running

## Test Procedure

### Step 1: Start the Application

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
```

### Step 2: Register with Restate

```bash
restate deployments register http://localhost:9081
```

### Step 3: Create a Test Order

```bash
restate workflow start order.sv1.OrderService/track-test-001 CreateOrder \
  --input '{
    "customer_id": "customer-tracking-test",
    "items": [
      {"product_id": "prod-001", "quantity": 2},
      {"product_id": "prod-002", "quantity": 1}
    ]
  }'
```

**Expected response:**
```json
{
  "order_id": "track-test-001"
}
```

### Step 4: Check Order Status Immediately (PENDING)

```bash
restate workflow get order.sv1.OrderService/track-test-001 GetOrder \
  --input '{"order_id": "track-test-001"}'
```

**Expected response (if checked quickly):**
```json
{
  "order": {
    "id": "track-test-001",
    "customer_id": "customer-tracking-test",
    "status": "PENDING"
  }
}
```

### Step 5: Wait 5-10 Seconds, Check Again (PROCESSING)

After payment completes:

```bash
restate workflow get order.sv1.OrderService/track-test-001 GetOrder \
  --input '{"order_id": "track-test-001"}'
```

**Expected response:**
```json
{
  "order": {
    "id": "track-test-001",
    "customer_id": "customer-tracking-test",
    "status": "PROCESSING"
  },
  "payment_info": {
    "payment_id": "abc-123-xyz",
    "status": "PAYMENT_COMPLETED",
    "amount": 30.0,
    "payment_method": "CREDIT_CARD"
  }
}
```

### Step 6: Wait Another 5 Seconds, Check Again (SHIPPED)

After shipment is created:

```bash
restate workflow get order.sv1.OrderService/track-test-001 GetOrder \
  --input '{"order_id": "track-test-001"}'
```

**Expected response:**
```json
{
  "order": {
    "id": "track-test-001",
    "customer_id": "customer-tracking-test",
    "status": "SHIPPED"
  },
  "payment_info": {
    "payment_id": "abc-123-xyz",
    "status": "PAYMENT_COMPLETED",
    "amount": 30.0,
    "payment_method": "CREDIT_CARD"
  },
  "shipment_info": {
    "shipment_id": "ship-456-def",
    "tracking_number": "TRACK-a1b2c3d4",
    "carrier": "FedEx",
    "status": "SHIPMENT_IN_TRANSIT",
    "current_location": "In Transit",
    "estimated_delivery": "2025-10-10"
  }
}
```

### Step 7: Verify Order Stays in SHIPPED Status

The workflow completes with the order in SHIPPED status:

```bash
restate workflow get order.sv1.OrderService/track-test-001 GetOrder \
  --input '{"order_id": "track-test-001"}'
```

**Expected response (order remains SHIPPED):**
```json
{
  "order": {
    "id": "track-test-001",
    "customer_id": "customer-tracking-test",
    "status": "SHIPPED"
  },
  "payment_info": {
    "payment_id": "abc-123-xyz",
    "status": "PAYMENT_COMPLETED",
    "amount": 30.0,
    "payment_method": "CREDIT_CARD"
  },
  "shipment_info": {
    "shipment_id": "ship-456-def",
    "tracking_number": "TRACK-a1b2c3d4",
    "carrier": "FedEx",
    "status": "SHIPMENT_IN_TRANSIT",
    "current_location": "In Transit",
    "estimated_delivery": "2025-10-10"
  }
}
```

### Step 8: Manually Mark as Delivered

To mark the order as delivered:

```bash
restate workflow send order.sv1.OrderService/track-test-001 UpdateOrderStatus \
  --input '{"status": "DELIVERED"}'
```

**Expected response:**
```json
{
  "success": true,
  "message": "Order status updated successfully"
}
```

### Step 9: Verify DELIVERED Status

```bash
restate workflow get order.sv1.OrderService/track-test-001 GetOrder \
  --input '{"order_id": "track-test-001"}'
```

**Expected response:**
```json
{
  "order": {
    "id": "track-test-001",
    "customer_id": "customer-tracking-test",
    "status": "DELIVERED"
  },
  "payment_info": {
    "payment_id": "abc-123-xyz",
    "status": "PAYMENT_COMPLETED",
    "amount": 30.0,
    "payment_method": "CREDIT_CARD"
  },
  "shipment_info": {
    "shipment_id": "ship-456-def",
    "tracking_number": "TRACK-a1b2c3d4",
    "carrier": "FedEx",
    "status": "SHIPMENT_DELIVERED",
    "current_location": "In Transit",
    "estimated_delivery": "2025-10-10"
  }
}
```

## Database Verification

### Step 10: Query Database for Order Details

```bash
psql -U orderpipelineadmin -d orderpipeline
```

**Query comprehensive order information:**
```sql
SELECT 
    o.id as order_id,
    o.customer_id,
    o.status as order_status,
    o.total_amount,
    p.id as payment_id,
    p.status as payment_status,
    p.amount as payment_amount,
    p.payment_method,
    s.id as shipment_id,
    s.tracking_number,
    s.carrier,
    s.status as shipment_status,
    s.current_location,
    s.estimated_delivery,
    o.created_at,
    o.updated_at
FROM orders o
LEFT JOIN payments p ON o.payment_id = p.id
LEFT JOIN shipments s ON o.shipment_id = s.id
WHERE o.id = 'track-test-001';
```

**Expected result:**
```
 order_id        | customer_id              | order_status | total_amount | payment_id | payment_status     | ...
-----------------+--------------------------+--------------+--------------+------------+--------------------+-----
 track-test-001  | customer-tracking-test   | DELIVERED    |        30.00 | abc-123... | PAYMENT_COMPLETED  | ...
```

### Step 11: Query Orders by Status

```sql
-- Count orders by status
SELECT status, COUNT(*) as count
FROM orders
GROUP BY status
ORDER BY count DESC;
```

**Expected result:**
```
   status    | count
-------------+-------
 DELIVERED   |     1
 PROCESSING  |     0
 PENDING     |     0
 SHIPPED     |     0
 CANCELLED   |     0
```

### Step 12: Query Payment Success Rate

```sql
-- Payment success rate
SELECT 
    status,
    COUNT(*) as count,
    ROUND(COUNT(*) * 100.0 / SUM(COUNT(*)) OVER (), 2) as percentage
FROM payments
GROUP BY status;
```

## Advanced Testing

### Test Multiple Orders Simultaneously

```bash
# Create 5 orders at once
for i in {1..5}; do
  restate workflow start order.sv1.OrderService/multi-test-00$i CreateOrder \
    --input "{\"customer_id\": \"customer-multi-$i\", \"items\": [{\"product_id\": \"prod-001\", \"quantity\": 1}]}" &
done
wait

# Wait 20 seconds for all to complete

# Check all orders
for i in {1..5}; do
  echo "=== Order multi-test-00$i ==="
  restate workflow get order.sv1.OrderService/multi-test-00$i GetOrder \
    --input "{\"order_id\": \"multi-test-00$i\"}"
  echo ""
done
```

### Test Order Tracking at Different Stages

Create a script to poll order status:

```bash
#!/bin/bash
ORDER_ID="poll-test-001"

# Create order
restate workflow start order.sv1.OrderService/$ORDER_ID CreateOrder \
  --input '{"customer_id": "customer-poll", "items": [{"product_id": "prod-001", "quantity": 1}]}'

# Poll every 2 seconds for 30 seconds
for i in {1..15}; do
  echo "=== Check $i ($(date +%H:%M:%S)) ==="
  restate workflow get order.sv1.OrderService/$ORDER_ID GetOrder \
    --input "{\"order_id\": \"$ORDER_ID\"}" | jq '.order.status'
  sleep 2
done
```

**Expected output:**
```
=== Check 1 (12:00:00) ===
"PENDING"
=== Check 2 (12:00:02) ===
"PENDING"
=== Check 3 (12:00:04) ===
"PROCESSING"
=== Check 4 (12:00:06) ===
"PROCESSING"
=== Check 5 (12:00:08) ===
"SHIPPED"
=== Check 6 (12:00:10) ===
"SHIPPED"
=== Check 7 (12:00:12) ===
"DELIVERED"
```

## Monitoring and Analytics

### Real-time Order Status Dashboard

```sql
-- Create a view for real-time order monitoring
CREATE OR REPLACE VIEW order_status_dashboard AS
SELECT 
    o.id,
    o.customer_id,
    o.status,
    o.total_amount,
    p.status as payment_status,
    s.tracking_number,
    s.status as shipment_status,
    s.current_location,
    EXTRACT(EPOCH FROM (NOW() - o.created_at)) as age_seconds,
    o.created_at,
    o.updated_at
FROM orders o
LEFT JOIN payments p ON o.payment_id = p.id
LEFT JOIN shipments s ON o.shipment_id = s.id
ORDER BY o.created_at DESC;

-- Query the dashboard
SELECT * FROM order_status_dashboard;
```

### Identify Stuck Orders

```sql
-- Orders stuck in PENDING for more than 30 seconds
SELECT id, customer_id, created_at, 
       EXTRACT(EPOCH FROM (NOW() - created_at)) as stuck_seconds
FROM orders
WHERE status = 'PENDING'
  AND created_at < NOW() - INTERVAL '30 seconds';

-- Orders stuck in PROCESSING for more than 30 seconds
SELECT id, customer_id, created_at,
       EXTRACT(EPOCH FROM (NOW() - created_at)) as stuck_seconds
FROM orders
WHERE status = 'PROCESSING'
  AND created_at < NOW() - INTERVAL '30 seconds';
```

### Order Fulfillment Metrics

```sql
-- Average time to complete orders
SELECT 
    AVG(EXTRACT(EPOCH FROM (updated_at - created_at))) as avg_completion_seconds
FROM orders
WHERE status = 'DELIVERED';

-- Order completion rate
SELECT 
    COUNT(CASE WHEN status = 'DELIVERED' THEN 1 END) * 100.0 / COUNT(*) as completion_rate
FROM orders;

-- Payment success rate
SELECT 
    COUNT(CASE WHEN status = 'PAYMENT_COMPLETED' THEN 1 END) * 100.0 / COUNT(*) as success_rate
FROM payments;
```

## Expected Timings

| Stage | Duration | Cumulative Time |
|-------|----------|-----------------|
| Order Creation | < 1s | 0-1s |
| Payment Processing | 5-15s (with retries) | 5-16s |
| Shipment Creation | 5s | 10-21s |
| Workflow Completion | < 1s | 10-22s |

**Total workflow time: 10-22 seconds (ends in SHIPPED status)**

**Note:** The order remains in SHIPPED status until manually marked as DELIVERED using UpdateOrderStatus.

## Success Criteria

✅ Order starts in PENDING status
✅ Order transitions to PROCESSING after payment
✅ Order transitions to SHIPPED after shipment creation
✅ Workflow completes with order in SHIPPED status
✅ Order can be manually marked as DELIVERED via UpdateOrderStatus
✅ Payment info is included in GetOrder response
✅ Shipment info is included in GetOrder response
✅ Database contains all order, payment, and shipment records
✅ JOIN queries return comprehensive information
✅ Status transitions are logged clearly

## Cleanup

```sql
-- Delete test orders
DELETE FROM orders WHERE id LIKE 'track-test-%' OR id LIKE 'multi-test-%' OR id LIKE 'poll-test-%';
DELETE FROM payments WHERE order_id LIKE 'track-test-%' OR order_id LIKE 'multi-test-%' OR order_id LIKE 'poll-test-%';
DELETE FROM shipments WHERE order_id LIKE 'track-test-%' OR order_id LIKE 'multi-test-%' OR order_id LIKE 'poll-test-%';
```

## Troubleshooting

### Order Stuck in PENDING
- Check application logs for payment processing errors
- Verify payment service is responding
- Check for payment retry attempts

### Missing Payment Info in GetOrder
- Verify payment was created successfully
- Check workflow state for payment_id
- Query payments table directly

### Missing Shipment Info in GetOrder
- Verify shipment was created successfully
- Check workflow state for shipment_id
- Query shipments table directly

### Database Query Returns No Results
- Verify order was persisted to database
- Check for database connection errors in logs
- Verify order ID matches exactly

