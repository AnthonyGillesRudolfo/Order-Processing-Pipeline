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

## Enhanced GetOrder Response

Includes order, payment, and shipment details if available. See examples in `TEST_ORDER_TRACKING.md`.
