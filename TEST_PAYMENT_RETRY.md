# Testing Payment Retry Mechanism

This guide demonstrates Restate's automatic retry behavior when payment processing fails.

## Overview

The `ProcessPayment` virtual object handler has been modified to simulate realistic payment gateway behavior:

- **5-second delay** - Simulates payment processing time
- **40% failure rate** - Random failures to trigger Restate's retry mechanism
- **Automatic retries** - Restate automatically retries failed operations
- **Durable execution** - All state is preserved across retries

## Payment Simulation Logic

```go
// Inside restate.Run() block:
1. Log start of payment processing
2. Sleep for 5 seconds (simulate gateway delay)
3. Generate random number (0-99)
4. If random < 40 (40% chance):
   - Return error → Restate will retry
5. If random >= 40 (60% chance):
   - Return success → Payment completes
```

## Expected Behavior

### Successful Payment (60% chance per attempt)
```
[Payment Object <id>] Starting payment transaction processing...
[Payment Object <id>] Simulating payment gateway call (5 second delay)
[Payment Object <id>] ✅ Payment attempt SUCCEEDED (random=67 >= 40)
[Payment Object <id>] Payment transaction completed successfully
[Payment Object <id>] Payment completed successfully
```

### Failed Payment with Retries (40% chance per attempt)
```
[Payment Object <id>] Starting payment transaction processing...
[Payment Object <id>] Simulating payment gateway call (5 second delay)
[Payment Object <id>] ❌ Payment attempt FAILED (random=23 < 40) - Restate will retry

# Restate automatically retries...

[Payment Object <id>] Starting payment transaction processing...
[Payment Object <id>] Simulating payment gateway call (5 second delay)
[Payment Object <id>] ❌ Payment attempt FAILED (random=15 < 40) - Restate will retry

# Restate retries again...

[Payment Object <id>] Starting payment transaction processing...
[Payment Object <id>] Simulating payment gateway call (5 second delay)
[Payment Object <id>] ✅ Payment attempt SUCCEEDED (random=78 >= 40)
[Payment Object <id>] Payment transaction completed successfully
```

## Testing Procedure

### Step 1: Start the Application

```bash
./order-processing-pipeline
```

### Step 2: Register with Restate

```bash
restate deployments register http://localhost:9081
```

### Step 3: Create a Test Order

```bash
restate workflow start order.sv1.OrderService/retry-test-001 CreateOrder \
  --input '{
    "customer_id": "customer-retry-test",
    "items": [
      {"product_id": "prod-001", "quantity": 1}
    ]
  }'
```

### Step 4: Observe the Logs

Watch the application logs to see:

1. **Order workflow starts**
2. **Payment processing begins** with 5-second delay
3. **Random success/failure** determination
4. **Automatic retries** if payment fails
5. **Eventually succeeds** and workflow continues
6. **Shipment creation** after successful payment
7. **Order completion**

### Example Log Output

```
[Workflow retry-test-001] Creating order for customer: customer-retry-test
[Workflow retry-test-001] Step 1: Persisting order to database
[DB] Inserted/Updated order: retry-test-001
[Workflow retry-test-001] Step 2: Processing payment

[Payment Object <payment-id>] Processing payment for order: retry-test-001, amount: 10.00
[Payment Object <payment-id>] Creating payment record in database
[DB] Inserted/Updated payment: <payment-id>

[Payment Object <payment-id>] Starting payment transaction processing...
[Payment Object <payment-id>] Simulating payment gateway call (5 second delay)
[Payment Object <payment-id>] ❌ Payment attempt FAILED (random=12 < 40) - Restate will retry

# 5 seconds pass...

[Payment Object <payment-id>] Starting payment transaction processing...
[Payment Object <payment-id>] Simulating payment gateway call (5 second delay)
[Payment Object <payment-id>] ❌ Payment attempt FAILED (random=38 < 40) - Restate will retry

# 5 seconds pass...

[Payment Object <payment-id>] Starting payment transaction processing...
[Payment Object <payment-id>] Simulating payment gateway call (5 second delay)
[Payment Object <payment-id>] ✅ Payment attempt SUCCEEDED (random=55 >= 40)
[Payment Object <payment-id>] Payment transaction completed successfully

[DB] Updated payment status: <payment-id> -> PAYMENT_COMPLETED
[Payment Object <payment-id>] Payment completed successfully

[Workflow retry-test-001] Payment completed: <payment-id>
[DB] Updated order payment: retry-test-001 -> <payment-id>
[DB] Updated order status: retry-test-001 -> PAID

[Workflow retry-test-001] Step 3: Creating shipment
[Shipping Object <shipment-id>] Creating shipment for order: retry-test-001
...
```

## Key Observations

### 1. Durable Execution
- Each payment attempt takes 5 seconds
- If it fails, Restate automatically retries
- No manual retry logic needed in the code

### 2. State Preservation
- Workflow state is preserved across retries
- Order ID, customer ID, amount all maintained
- No duplicate orders created

### 3. Exactly-Once Semantics
- Payment is only recorded once in the database
- Even with multiple retries, no duplicate payments
- Database operations are idempotent

### 4. Automatic Recovery
- No manual intervention needed
- System automatically recovers from failures
- Eventually consistent success

### 5. Observability
- Clear logging shows each attempt
- Random value logged for transparency
- Success/failure clearly indicated

## Probability Analysis

With a 40% failure rate per attempt:

- **1st attempt succeeds**: 60% chance
- **2nd attempt succeeds**: 24% chance (40% × 60%)
- **3rd attempt succeeds**: 9.6% chance (40% × 40% × 60%)
- **4th attempt succeeds**: 3.84% chance
- **5th attempt succeeds**: 1.54% chance

**Expected number of attempts**: ~1.67 attempts on average

Most orders will succeed on the first or second attempt, but some may require 3-5 attempts.

## Testing Multiple Orders

Create multiple orders to see different retry patterns:

```bash
# Create 5 test orders
for i in {1..5}; do
  restate workflow start order.sv1.OrderService/retry-test-00$i CreateOrder \
    --input "{\"customer_id\": \"customer-$i\", \"items\": [{\"product_id\": \"prod-001\", \"quantity\": 1}]}"
  echo "Created order retry-test-00$i"
done
```

You'll observe:
- Some orders succeed immediately (1 attempt)
- Some require 2-3 retries
- Rarely, some may need 4-5 retries
- All eventually succeed

## Verifying in Database

After orders complete, check the database:

```sql
-- Connect to database
psql -U orderpipelineadmin -d orderpipeline

-- View all orders with payment status
SELECT 
    o.id,
    o.customer_id,
    o.status,
    p.status as payment_status,
    o.created_at,
    o.updated_at
FROM orders o
LEFT JOIN payments p ON o.payment_id = p.id
WHERE o.id LIKE 'retry-test-%'
ORDER BY o.created_at;
```

**Expected result:**
- All orders have status `COMPLETED`
- All payments have status `PAYMENT_COMPLETED`
- No duplicate records despite retries

## Advanced Testing

### Test 1: Monitor Retry Timing

Use `watch` to monitor order status in real-time:

```bash
watch -n 1 "psql -U orderpipelineadmin -d orderpipeline -c \"SELECT id, status, updated_at FROM orders WHERE id LIKE 'retry-test-%' ORDER BY updated_at DESC LIMIT 5;\""
```

### Test 2: Stress Test with Many Orders

```bash
# Create 20 orders simultaneously
for i in {1..20}; do
  restate workflow start order.sv1.OrderService/stress-test-$(printf "%03d" $i) CreateOrder \
    --input "{\"customer_id\": \"stress-customer-$i\", \"items\": [{\"product_id\": \"prod-001\", \"quantity\": 1}]}" &
done
wait
```

### Test 3: Calculate Actual Failure Rate

```bash
# Count total payment attempts vs successes from logs
grep "Payment attempt" order-processing-pipeline.log | wc -l  # Total attempts
grep "Payment attempt SUCCEEDED" order-processing-pipeline.log | wc -l  # Successes
grep "Payment attempt FAILED" order-processing-pipeline.log | wc -l  # Failures
```

## Modifying Failure Rate

To test different failure rates, modify the threshold in `main.go`:

```go
// Current: 40% failure rate
if randomValue < 40 {
    // Fails
}

// For 20% failure rate:
if randomValue < 20 {
    // Fails
}

// For 70% failure rate:
if randomValue < 70 {
    // Fails
}
```

## Benefits Demonstrated

1. ✅ **Resilience** - System handles transient failures gracefully
2. ✅ **No Lost Orders** - All orders eventually complete despite failures
3. ✅ **Automatic Recovery** - No manual intervention required
4. ✅ **State Consistency** - Workflow state preserved across retries
5. ✅ **Idempotency** - No duplicate database records
6. ✅ **Observability** - Clear logging of retry behavior
7. ✅ **Realistic Simulation** - Mimics real payment gateway behavior

## Cleanup

```sql
-- Delete test orders
DELETE FROM orders WHERE id LIKE 'retry-test-%' OR id LIKE 'stress-test-%';
DELETE FROM payments WHERE order_id LIKE 'retry-test-%' OR order_id LIKE 'stress-test-%';
DELETE FROM shipments WHERE order_id LIKE 'retry-test-%' OR order_id LIKE 'stress-test-%';
```

## Conclusion

This test demonstrates Restate's powerful durable execution capabilities:
- Automatic retry of failed operations
- State preservation across retries
- Exactly-once semantics for database operations
- No manual retry logic needed in application code

The payment simulation provides a realistic way to observe and test these capabilities in action.

