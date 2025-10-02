#!/bin/bash

# Test script to demonstrate order status tracking
# This script creates an order and polls its status to show the progression

set -e

ORDER_ID="status-test-$(date +%s)"
CUSTOMER_ID="customer-status-test"

echo "=========================================="
echo "Order Status Tracking Test"
echo "=========================================="
echo "Order ID: $ORDER_ID"
echo "Customer ID: $CUSTOMER_ID"
echo ""

# Function to get order status
get_status() {
    restate workflow get order.sv1.OrderService/$ORDER_ID GetOrder \
        --input "{\"order_id\": \"$ORDER_ID\"}" 2>/dev/null | jq -r '.order.status // "UNKNOWN"'
}

# Function to get full order details
get_details() {
    restate workflow get order.sv1.OrderService/$ORDER_ID GetOrder \
        --input "{\"order_id\": \"$ORDER_ID\"}" 2>/dev/null
}

echo "Step 1: Creating order..."
restate workflow start order.sv1.OrderService/$ORDER_ID CreateOrder \
    --input "{
        \"customer_id\": \"$CUSTOMER_ID\",
        \"items\": [
            {\"product_id\": \"prod-001\", \"quantity\": 2}
        ]
    }" > /dev/null 2>&1

echo "✓ Order created"
echo ""

echo "Step 2: Monitoring order status progression..."
echo "----------------------------------------"

# Poll for status changes
LAST_STATUS=""
MAX_CHECKS=30
CHECK_COUNT=0

while [ $CHECK_COUNT -lt $MAX_CHECKS ]; do
    CURRENT_STATUS=$(get_status)
    
    if [ "$CURRENT_STATUS" != "$LAST_STATUS" ]; then
        TIMESTAMP=$(date +"%H:%M:%S")
        echo "[$TIMESTAMP] Status changed: $LAST_STATUS → $CURRENT_STATUS"
        
        # Show details when status changes
        case $CURRENT_STATUS in
            "PENDING")
                echo "  └─ Order created, awaiting payment processing"
                ;;
            "PROCESSING")
                echo "  └─ Payment completed, preparing shipment"
                DETAILS=$(get_details)
                PAYMENT_ID=$(echo "$DETAILS" | jq -r '.payment_info.payment_id // "N/A"')
                PAYMENT_AMOUNT=$(echo "$DETAILS" | jq -r '.payment_info.amount // "N/A"')
                echo "  └─ Payment ID: $PAYMENT_ID"
                echo "  └─ Amount: \$$PAYMENT_AMOUNT"
                ;;
            "SHIPPED")
                echo "  └─ Shipment created and in transit"
                DETAILS=$(get_details)
                TRACKING=$(echo "$DETAILS" | jq -r '.shipment_info.tracking_number // "N/A"')
                CARRIER=$(echo "$DETAILS" | jq -r '.shipment_info.carrier // "N/A"')
                echo "  └─ Tracking: $TRACKING"
                echo "  └─ Carrier: $CARRIER"
                echo ""
                echo "✓ Order workflow completed - Order is now SHIPPED"
                break
                ;;
            "DELIVERED")
                echo "  └─ Order delivered to customer"
                break
                ;;
            "CANCELLED")
                echo "  └─ Order cancelled (payment failed)"
                break
                ;;
        esac
        echo ""
        
        LAST_STATUS=$CURRENT_STATUS
    fi
    
    CHECK_COUNT=$((CHECK_COUNT + 1))
    sleep 1
done

echo "----------------------------------------"
echo ""

echo "Step 3: Final order details"
echo "----------------------------------------"
get_details | jq '.'
echo ""

echo "Step 4: Verify in database"
echo "----------------------------------------"
echo "SELECT o.id, o.status, p.status as payment_status, s.tracking_number, s.status as shipment_status" \
     "FROM orders o LEFT JOIN payments p ON o.payment_id = p.id LEFT JOIN shipments s ON o.shipment_id = s.id" \
     "WHERE o.id = '$ORDER_ID';" | \
psql -U orderpipelineadmin -d orderpipeline 2>/dev/null || echo "Database query failed (database may not be accessible)"

echo ""
echo "=========================================="
echo "Test Complete!"
echo "=========================================="
echo ""
echo "To mark the order as DELIVERED, run:"
echo "  restate workflow send order.sv1.OrderService/$ORDER_ID UpdateOrderStatus \\"
echo "    --input '{\"status\": \"DELIVERED\"}'"
echo ""

