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
    │   └─> Status: PROCESSING
    │
    ├─> Step 4: Call Shipping Virtual Object
    │   └─> CreateShipment(...)
    │
    ├─> Step 5: Update Order with Shipment Info
    │   └─> Status: SHIPPED
    │
    └─> Step 6: Mark Order Complete
        └─> Status: DELIVERED
```

## Retry Scenarios

See examples of success and retry attempts above; the handler simulates a 40% failure rate with 5s delay and relies on Restate retries.

## Monitoring

Watch logs for failed/succeeded attempts and verify DB writes are idempotent.
