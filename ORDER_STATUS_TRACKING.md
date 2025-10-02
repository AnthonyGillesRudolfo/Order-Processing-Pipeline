# Order Status Tracking System

## Overview

The Order Processing Pipeline now includes a comprehensive order status tracking system that provides granular visibility into the order fulfillment process, including payment and shipment progress.

## Order Status Values

### New Granular Status Values

The system uses the following status values to reflect the actual progress of an order:

| Status | Description | When Set |
|--------|-------------|----------|
| `PENDING` | Order created, payment not yet completed | After order creation, before payment |
| `PROCESSING` | Payment completed, shipment not yet created | After successful payment, before shipment |
| `SHIPPED` | Shipment created and in transit | After shipment creation |
| `DELIVERED` | Shipment delivered to customer | After delivery confirmation |
| `CANCELLED` | Order cancelled (payment failed or other issues) | When payment fails or order is cancelled |

### Deprecated Status Values (Backward Compatibility)

| Status | Replacement | Notes |
|--------|-------------|-------|
| `PAID` | `PROCESSING` | Use PROCESSING for new implementations |
| `COMPLETED` | `DELIVERED` | Use DELIVERED for new implementations |

## Order Lifecycle

### Automatic Workflow Progression

The CreateOrder workflow automatically progresses through all statuses with observable delays:

```
┌─────────────┐
│   PENDING   │  Order created, awaiting payment (5 seconds)
└──────┬──────┘
       │
       │ Durable Sleep: 5 seconds (payment processing)
       │
       ▼
┌─────────────┐
│ PROCESSING  │  Payment completed, preparing shipment (5 seconds)
└──────┬──────┘
       │
       │ Durable Sleep: 5 seconds (order preparation)
       │
       ▼
┌─────────────┐
│   SHIPPED   │  Shipment in transit (10 seconds)
└──────┬──────┘
       │
       │ Durable Sleep: 10 seconds (delivery time)
       │
       ▼
┌─────────────┐
│  DELIVERED  │  Order completed (WORKFLOW ENDS HERE)
└─────────────┘
```

**Total workflow time: ~20 seconds** (plus payment retry time if payment fails)

**Key Features:**
- **Observable Status Transitions**: Durable sleeps between each status allow you to query and observe the order at each stage
- **Automatic Completion**: The workflow automatically progresses to DELIVERED without manual intervention
- **Durable Execution**: All sleeps are durable - if the system restarts, the workflow resumes from where it left off

### Cancellation Flow

```
┌─────────────┐
│   PENDING   │  Order created
└──────┬──────┘
       │
       │ Payment fails after retries
       │
       ▼
┌─────────────┐
│  CANCELLED  │  Payment failed or order cancelled
└─────────────┘
```

## Enhanced GetOrder Response

The `GetOrder` endpoint now returns comprehensive information including:

### Order Information
- Order ID
- Customer ID
- Current order status
- Total amount

### Payment Information (if payment exists)
- Payment ID
- Payment status (PENDING, PROCESSING, COMPLETED, FAILED)
- Payment amount
- Payment method (CREDIT_CARD, BANK_TRANSFER, DIGITAL_WALLET)

### Shipment Information (if shipment exists)
- Shipment ID
- Tracking number
- Carrier name (e.g., FedEx, UPS)
- Shipment status (CREATED, IN_TRANSIT, OUT_FOR_DELIVERY, DELIVERED)
- Current location
- Estimated delivery date

## API Usage

### Create an Order

```bash
restate workflow start order.sv1.OrderService/order-123 CreateOrder \
  --input '{
    "customer_id": "customer-456",
    "items": [
      {"product_id": "prod-001", "quantity": 2},
      {"product_id": "prod-002", "quantity": 1}
    ]
  }'
```

### Get Order Status

```bash
restate workflow get order.sv1.OrderService/order-123 GetOrder \
  --input '{"order_id": "order-123"}'
```

### Manually Update Order Status (Optional)

You can manually update the order status if needed:

```bash
restate workflow send order.sv1.OrderService/order-123 UpdateOrderStatus \
  --input '{"status": "CANCELLED"}'
```

**Note:** The workflow automatically progresses to DELIVERED, so manual status updates are typically only needed for cancellations or corrections.

### Example Response

```json
{
  "order": {
    "id": "order-123",
    "customer_id": "customer-456",
    "status": "SHIPPED"
  },
  "payment_info": {
    "payment_id": "pay-abc-123",
    "status": "PAYMENT_COMPLETED",
    "amount": 30.0,
    "payment_method": "CREDIT_CARD"
  },
  "shipment_info": {
    "shipment_id": "ship-xyz-789",
    "tracking_number": "TRACK-a1b2c3d4",
    "carrier": "FedEx",
    "status": "SHIPMENT_IN_TRANSIT",
    "current_location": "In Transit",
    "estimated_delivery": "2025-10-10"
  }
}
```

## Database Queries

### Query Order Details with JOINs

The system includes database functions to retrieve comprehensive order information:

```go
// Get single order with all details
details, err := GetOrderDetails("order-123")

// Get all orders for a customer
orders, err := GetOrdersByCustomer("customer-456")
```

### SQL Query Example

```sql
-- Get comprehensive order information
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
    s.estimated_delivery
FROM orders o
LEFT JOIN payments p ON o.payment_id = p.id
LEFT JOIN shipments s ON o.shipment_id = s.id
WHERE o.id = 'order-123';
```

### Query Orders by Status

```sql
-- Get all orders in PROCESSING status
SELECT * FROM orders WHERE status = 'PROCESSING';

-- Get all orders that are SHIPPED
SELECT 
    o.id,
    o.customer_id,
    s.tracking_number,
    s.carrier,
    s.current_location
FROM orders o
JOIN shipments s ON o.shipment_id = s.id
WHERE o.status = 'SHIPPED';
```

### Analytics Queries

```sql
-- Count orders by status
SELECT status, COUNT(*) as count
FROM orders
GROUP BY status;

-- Average order value by status
SELECT status, AVG(total_amount) as avg_amount
FROM orders
GROUP BY status;

-- Orders with payment issues
SELECT o.id, o.customer_id, p.status
FROM orders o
JOIN payments p ON o.payment_id = p.id
WHERE p.status = 'PAYMENT_FAILED';
```

## Workflow State Management

The CreateOrder workflow stores the following information in Restate state:

### Order State
- `customer_id` - Customer identifier
- `status` - Current order status
- `total_amount` - Total order amount

### Payment State
- `payment_id` - Payment transaction ID
- `payment_status` - Payment status

### Shipment State
- `shipment_id` - Shipment ID
- `tracking_number` - Tracking number
- `estimated_delivery` - Estimated delivery date
- `current_location` - Current shipment location

## Status Transitions

### Successful Order Flow

```
PENDING → PROCESSING → SHIPPED (workflow ends) → DELIVERED (manual update)
```

**Timeline:**
1. **PENDING** (0s) - Order created
2. **PROCESSING** (~5-15s) - Payment completed (with retries)
3. **SHIPPED** (~10-20s) - Shipment created, workflow completes
4. **DELIVERED** (manual) - Order marked as delivered via UpdateOrderStatus

### Failed Order Flow

```
PENDING → CANCELLED
```

**When:**
- Payment fails after all retries
- Order is manually cancelled

## Testing the Status Tracking

### Test 1: Create and Track an Order

```bash
# Create order
restate workflow start order.sv1.OrderService/test-001 CreateOrder \
  --input '{"customer_id": "customer-test", "items": [{"product_id": "prod-001", "quantity": 1}]}'

# Wait a few seconds, then check status (should be PENDING or PROCESSING)
restate workflow get order.sv1.OrderService/test-001 GetOrder \
  --input '{"order_id": "test-001"}'

# Wait for completion, then check again (should be DELIVERED)
restate workflow get order.sv1.OrderService/test-001 GetOrder \
  --input '{"order_id": "test-001"}'
```

### Test 2: Query Database

```bash
# Connect to database
psql -U orderpipelineadmin -d orderpipeline

# View order with all details
SELECT 
    o.id,
    o.status,
    p.status as payment_status,
    s.tracking_number,
    s.status as shipment_status
FROM orders o
LEFT JOIN payments p ON o.payment_id = p.id
LEFT JOIN shipments s ON o.shipment_id = s.id
WHERE o.id = 'test-001';
```

## Benefits

1. **Granular Visibility** - Know exactly where each order is in the fulfillment process
2. **Customer Service** - Quickly answer customer questions about order status
3. **Debugging** - Identify where orders are stuck or failing
4. **Analytics** - Track conversion rates at each stage
5. **Monitoring** - Alert on orders stuck in specific statuses
6. **Reporting** - Generate reports on order fulfillment metrics

## Implementation Details

### Files Modified

1. **api/order-pipeline.proto**
   - Added new status values (PROCESSING, DELIVERED)
   - Added PaymentInfo and ShipmentInfo messages
   - Enhanced GetOrderResponse

2. **main.go**
   - Updated CreateOrder to use PROCESSING instead of PAID
   - Updated CreateOrder to use DELIVERED instead of COMPLETED
   - Enhanced GetOrder to return comprehensive information
   - Store additional state (payment_status, current_location, etc.)

3. **database.go**
   - Added GetOrderDetails() function
   - Added GetOrdersByCustomer() function
   - Comprehensive JOIN queries

## Next Steps

- Add UpdateShipmentLocation handler to update current_location
- Add MarkAsDelivered handler to transition to DELIVERED status
- Add CancelOrder handler to transition to CANCELLED status
- Add webhook notifications for status changes
- Add customer-facing order tracking page
- Add admin dashboard for order monitoring

