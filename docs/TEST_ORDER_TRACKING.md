# Testing Order Status Tracking System

This guide provides step-by-step instructions to test the comprehensive order status tracking system with observable delays across PENDING → PROCESSING → SHIPPED → DELIVERED.

For the full original, see `TEST_ORDER_TRACKING.md` at repo root (kept for history). Key steps:

## Expected Behavior
- PENDING ~5s
- PROCESSING ~5s
- SHIPPED ~10s
- DELIVERED final

## Quick Start
```bash
make proto
make run
restate deployments register http://localhost:9081

# Create order
restate workflow start order.sv1.OrderService/track-test-001 CreateOrder \
  --input '{"customer_id": "customer-tracking-test", "items": [{"product_id": "prod-001", "quantity": 2}]}'

# Check status
restate workflow get order.sv1.OrderService/track-test-001 GetOrder \
  --input '{"order_id": "track-test-001"}'
```

## Database Verification
```sql
SELECT o.id, o.status, p.status AS payment_status, s.status AS shipment_status
FROM orders o
LEFT JOIN payments p ON o.payment_id = p.id
LEFT JOIN shipments s ON o.shipment_id = s.id
WHERE o.id = 'track-test-001';
```
