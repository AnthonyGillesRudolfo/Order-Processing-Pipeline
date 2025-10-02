# Payment Retry Flow Visualization

## Overview

This document visualizes the payment processing flow with Restate's automatic retry mechanism.

## Flow Diagram

```
Order Workflow (CreateOrder)
    │
    ├─> Step 1: Create Order in Database
    │   └─> Status: PENDING
    │
    ├─> Step 2: Call Payment Virtual Object
    │   │
    │   └─> ProcessPayment(payment_id, order_id, amount)
    │       │
    │       ├─> Check if already processed (idempotency)
    │       │   └─> If yes: Return existing status
    │       │
    │       ├─> Insert payment record in DB (PROCESSING)
    │       │
    │       ├─> restate.Run() - Payment Transaction
    │       │   │
    │       │   ├─> Log: "Starting payment transaction"
    │       │   ├─> Sleep 5 seconds (simulate gateway)
    │       │   ├─> Generate random(0-99)
    │       │   │
    │       │   ├─> If random < 40 (40% chance):
    │       │   │   ├─> Log: "❌ FAILED - Restate will retry"
    │       │   │   └─> Return ERROR
    │       │   │       │
    │       │   │       └─> RESTATE AUTOMATIC RETRY
    │       │   │           │
    │       │   │           ├─> Replay from last checkpoint
    │       │   │           ├─> Sleep 5 seconds again
    │       │   │           ├─> Generate new random(0-99)
    │       │   │           │
    │       │   │           ├─> If random < 40:
    │       │   │           │   └─> Return ERROR → RETRY AGAIN
    │       │   │           │
    │       │   │           └─> If random >= 40:
    │       │   │               └─> Return SUCCESS → Continue
    │       │   │
    │       │   └─> If random >= 40 (60% chance):
    │       │       ├─> Log: "✅ SUCCEEDED"
    │       │       └─> Return SUCCESS
    │       │
    │       ├─> Update payment status in DB (COMPLETED)
    │       └─> Return payment response
    │
    ├─> Step 3: Update Order with Payment Info
    │   └─> Status: PAID
    │
    ├─> Step 4: Call Shipping Virtual Object
    │   └─> CreateShipment(...)
    │
    ├─> Step 5: Update Order with Shipment Info
    │   └─> Status: SHIPPED
    │
    └─> Step 6: Mark Order Complete
        └─> Status: COMPLETED
```

## Retry Scenarios

### Scenario 1: Success on First Attempt (60% probability)

```
Time: 0s    → Payment processing starts
Time: 5s    → Random = 67 (>= 40) → SUCCESS
Time: 5s    → Payment completed
Total: 5 seconds
Attempts: 1
```

### Scenario 2: Success on Second Attempt (24% probability)

```
Time: 0s    → Payment processing starts (Attempt 1)
Time: 5s    → Random = 23 (< 40) → FAIL
Time: 5s    → Restate triggers retry
Time: 5s    → Payment processing starts (Attempt 2)
Time: 10s   → Random = 55 (>= 40) → SUCCESS
Time: 10s   → Payment completed
Total: 10 seconds
Attempts: 2
```

### Scenario 3: Success on Third Attempt (9.6% probability)

```
Time: 0s    → Payment processing starts (Attempt 1)
Time: 5s    → Random = 12 (< 40) → FAIL
Time: 5s    → Restate triggers retry
Time: 5s    → Payment processing starts (Attempt 2)
Time: 10s   → Random = 38 (< 40) → FAIL
Time: 10s   → Restate triggers retry
Time: 10s   → Payment processing starts (Attempt 3)
Time: 15s   → Random = 82 (>= 40) → SUCCESS
Time: 15s   → Payment completed
Total: 15 seconds
Attempts: 3
```

### Scenario 4: Success on Fourth Attempt (3.84% probability)

```
Time: 0s    → Payment processing starts (Attempt 1)
Time: 5s    → Random = 15 (< 40) → FAIL
Time: 5s    → Restate triggers retry
Time: 5s    → Payment processing starts (Attempt 2)
Time: 10s   → Random = 8 (< 40) → FAIL
Time: 10s   → Restate triggers retry
Time: 10s   → Payment processing starts (Attempt 3)
Time: 15s   → Random = 31 (< 40) → FAIL
Time: 15s   → Restate triggers retry
Time: 15s   → Payment processing starts (Attempt 4)
Time: 20s   → Random = 72 (>= 40) → SUCCESS
Time: 20s   → Payment completed
Total: 20 seconds
Attempts: 4
```

## State Preservation Across Retries

### What is Preserved:
- ✅ Workflow state (order_id, customer_id, total_amount)
- ✅ Virtual object state (payment_id, order_id, amount)
- ✅ Database records (order, payment)
- ✅ Execution position in workflow

### What is NOT Duplicated:
- ✅ Order record (only created once)
- ✅ Payment record (only created once)
- ✅ Workflow instance (single instance per order_id)
- ✅ Virtual object instance (single instance per payment_id)

## Restate's Retry Mechanism

### How Restate Handles Retries:

1. **Journal-Based Execution**
   - Every operation is logged in a journal
   - On retry, Restate replays from the journal
   - Deterministic replay ensures consistency

2. **Automatic Retry**
   - No manual retry logic needed
   - Restate automatically retries failed operations
   - Exponential backoff (configurable)

3. **Idempotency**
   - Operations can be safely retried
   - Database uses `ON CONFLICT DO UPDATE`
   - State checks prevent duplicate work

4. **Exactly-Once Semantics**
   - Each operation executes exactly once
   - Even with retries, no duplicate side effects
   - Database writes are idempotent

## Code Structure

### Payment Processing Code:

```go
// Virtual Object Handler
func ProcessPayment(ctx restate.ObjectContext, req *ProcessPaymentRequest) (*ProcessPaymentResponse, error) {
    paymentId := restate.Key(ctx)
    
    // Check if already processed (idempotency)
    status, err := restate.Get[PaymentStatus](ctx, "status")
    if err == nil {
        return &ProcessPaymentResponse{PaymentId: paymentId, Status: status}, nil
    }
    
    // Insert payment record in database
    _, err = restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
        return nil, InsertPayment(paymentId, req.OrderId, req.Amount, ...)
    })
    
    // Process payment with retry logic
    _, err = restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
        // 5-second delay
        time.Sleep(5 * time.Second)
        
        // 40% failure rate
        if rand.Intn(100) < 40 {
            return nil, fmt.Errorf("payment gateway error")  // Triggers retry
        }
        
        return nil, nil  // Success
    })
    
    if err != nil {
        // Update status to FAILED
        restate.Set(ctx, "status", PAYMENT_FAILED)
        return &ProcessPaymentResponse{PaymentId: paymentId, Status: PAYMENT_FAILED}, err
    }
    
    // Update status to COMPLETED
    restate.Set(ctx, "status", PAYMENT_COMPLETED)
    
    // Update database
    _, err = restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
        return nil, UpdatePaymentStatus(paymentId, PAYMENT_COMPLETED)
    })
    
    return &ProcessPaymentResponse{PaymentId: paymentId, Status: PAYMENT_COMPLETED}, nil
}
```

## Probability Distribution

### Success by Attempt Number:

| Attempt | Probability | Cumulative | Expected Time |
|---------|-------------|------------|---------------|
| 1       | 60.00%      | 60.00%     | 5 seconds     |
| 2       | 24.00%      | 84.00%     | 10 seconds    |
| 3       | 9.60%       | 93.60%     | 15 seconds    |
| 4       | 3.84%       | 97.44%     | 20 seconds    |
| 5       | 1.54%       | 98.98%     | 25 seconds    |
| 6       | 0.61%       | 99.59%     | 30 seconds    |
| 7+      | 0.41%       | 100.00%    | 35+ seconds   |

### Average Metrics:
- **Expected attempts**: 1.67
- **Expected time**: 8.35 seconds
- **Median attempts**: 1
- **Median time**: 5 seconds
- **99th percentile**: 6 attempts (30 seconds)

## Monitoring Retry Behavior

### Log Patterns to Watch:

**Successful First Attempt:**
```
[Payment Object abc] Starting payment transaction processing...
[Payment Object abc] Simulating payment gateway call (5 second delay)
[Payment Object abc] ✅ Payment attempt SUCCEEDED (random=67 >= 40)
```

**Failed Attempt with Retry:**
```
[Payment Object abc] Starting payment transaction processing...
[Payment Object abc] Simulating payment gateway call (5 second delay)
[Payment Object abc] ❌ Payment attempt FAILED (random=23 < 40) - Restate will retry
[Payment Object abc] Starting payment transaction processing...
[Payment Object abc] Simulating payment gateway call (5 second delay)
[Payment Object abc] ✅ Payment attempt SUCCEEDED (random=55 >= 40)
```

### Database State During Retries:

```sql
-- Payment record is created once
INSERT INTO payments (id, order_id, amount, status) VALUES (..., 'PROCESSING');

-- Status updated only after success
UPDATE payments SET status = 'PAYMENT_COMPLETED' WHERE id = ...;
```

**Key Point:** Even with multiple retries, the payment record is only inserted once due to `ON CONFLICT DO UPDATE` clause.

## Benefits of This Approach

1. **Realistic Testing**
   - Simulates real payment gateway behavior
   - Random failures mimic network issues, timeouts, etc.
   - 5-second delay mimics actual processing time

2. **Demonstrates Restate Features**
   - Automatic retry mechanism
   - Durable execution
   - State preservation
   - Exactly-once semantics

3. **Production-Ready Pattern**
   - Same pattern works for real payment gateways
   - Just replace simulation with actual API calls
   - Retry logic is already built-in

4. **Observable Behavior**
   - Clear logging shows retry attempts
   - Random values logged for transparency
   - Easy to debug and monitor

## Adapting for Production

To use with a real payment gateway:

```go
_, err = restate.Run(ctx, func(ctx restate.RunContext) (any, error) {
    // Replace simulation with actual API call
    response, err := paymentGateway.ProcessPayment(PaymentRequest{
        Amount: req.Amount,
        CardNumber: req.PaymentMethod.CreditCard.CardNumber,
        // ... other fields
    })
    
    if err != nil {
        // Transient errors will trigger retry
        return nil, fmt.Errorf("payment gateway error: %w", err)
    }
    
    if response.Status == "declined" {
        // Permanent failure, don't retry
        return nil, &PermanentError{Msg: "card declined"}
    }
    
    return response, nil
})
```

## Conclusion

The payment retry mechanism demonstrates:
- ✅ Restate's automatic retry capabilities
- ✅ Durable execution across failures
- ✅ State consistency during retries
- ✅ Exactly-once database operations
- ✅ Realistic payment gateway simulation
- ✅ Observable retry behavior
- ✅ Production-ready patterns

This provides a solid foundation for building resilient payment processing systems.

