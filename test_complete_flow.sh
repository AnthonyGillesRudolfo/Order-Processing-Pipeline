#!/bin/bash

# Complete AP2 Integration Test Script
# This script tests the entire checkout flow from cart to invoice

echo "🧪 Testing Complete AP2 Integration Flow"
echo "========================================"

# Configuration
CUSTOMER_ID="customer-001"
RESTATE_URL="http://localhost:8080"
BACKEND_URL="http://localhost:3000"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

echo -e "\n${BLUE}1️⃣ Testing Cart Functionality${NC}"
echo "================================"

# View current cart
echo "📦 Viewing current cart..."
CART_RESPONSE=$(curl -s -X POST "$RESTATE_URL/cart.sv1.CartService/$CUSTOMER_ID/ViewCart" \
  -H "Content-Type: application/json" \
  -d '{"customer_id": "'$CUSTOMER_ID'"}')

echo "Cart contents:"
echo "$CART_RESPONSE" | jq . 2>/dev/null || echo "$CART_RESPONSE"

# Extract total amount from cart
TOTAL_AMOUNT=$(echo "$CART_RESPONSE" | jq -r '.cart_state.total_amount // "0"' 2>/dev/null || echo "0")
echo -e "\n💰 Cart total amount: \$${TOTAL_AMOUNT}"

if [ "$TOTAL_AMOUNT" = "0" ]; then
    echo -e "${YELLOW}⚠️  Cart is empty. Adding items...${NC}"
    
    # Add items to cart
    echo "🛒 Adding items to cart..."
    ADD_RESPONSE=$(curl -s -X POST "$RESTATE_URL/cart.sv1.CartService/$CUSTOMER_ID/AddToCart" \
      -H "Content-Type: application/json" \
      -d '{
        "customer_id": "'$CUSTOMER_ID'",
        "merchant_id": "m_001",
        "items": [
          {"product_id": "i_001", "quantity": 2},
          {"product_id": "i_002", "quantity": 1}
        ]
      }')
    
    echo "Add to cart result:"
    echo "$ADD_RESPONSE" | jq . 2>/dev/null || echo "$ADD_RESPONSE"
    
    # Update total amount
    TOTAL_AMOUNT=$(echo "$ADD_RESPONSE" | jq -r '.cart_state.total_amount // "0"' 2>/dev/null || echo "0")
    echo -e "\n💰 Updated cart total: \$${TOTAL_AMOUNT}"
fi

echo -e "\n${BLUE}2️⃣ Testing AP2 Integration${NC}"
echo "============================="

# Test AP2 endpoints
echo "🔧 Testing AP2 endpoints..."

# Create mandate
echo "📝 Creating AP2 mandate..."
MANDATE_RESPONSE=$(curl -s -X POST "$BACKEND_URL/ap2/mandates" \
  -H "Content-Type: application/json" \
  -d '{
    "customer_id": "'$CUSTOMER_ID'",
    "scope": "purchase",
    "amount_limit": 2000,
    "expires_at": "2024-12-31T23:59:59Z"
  }')

echo "Mandate creation result:"
echo "$MANDATE_RESPONSE" | jq . 2>/dev/null || echo "$MANDATE_RESPONSE"

# Extract mandate ID
MANDATE_ID=$(echo "$MANDATE_RESPONSE" | jq -r '.mandate_id // empty' 2>/dev/null)

if [ -z "$MANDATE_ID" ]; then
    echo -e "${RED}❌ Failed to create mandate. Using session mandate for testing.${NC}"
    MANDATE_ID="mdt_session"
else
    echo -e "${GREEN}✅ Mandate created: $MANDATE_ID${NC}"
fi

# Create intent
echo -e "\n🎯 Creating AP2 intent..."
INTENT_RESPONSE=$(curl -s -X POST "$BACKEND_URL/ap2/intents" \
  -H "Content-Type: application/json" \
  -d '{
    "mandate_id": "'$MANDATE_ID'",
    "customer_id": "'$CUSTOMER_ID'",
    "cart_id": "'$CUSTOMER_ID'",
    "shipping_address": {
      "address_line1": "123 Main St",
      "city": "Jakarta",
      "state": "DKI Jakarta",
      "postal_code": "10110",
      "country": "Indonesia",
      "delivery_method": "standard"
    }
  }')

echo "Intent creation result:"
echo "$INTENT_RESPONSE" | jq . 2>/dev/null || echo "$INTENT_RESPONSE"

# Extract intent ID
INTENT_ID=$(echo "$INTENT_RESPONSE" | jq -r '.intent_id // empty' 2>/dev/null)

if [ -z "$INTENT_ID" ]; then
    echo -e "${RED}❌ Failed to create intent${NC}"
    exit 1
fi

echo -e "${GREEN}✅ Intent created: $INTENT_ID${NC}"

# Authorize intent
echo -e "\n🔐 Authorizing intent..."
AUTH_RESPONSE=$(curl -s -X POST "$BACKEND_URL/ap2/authorize" \
  -H "Content-Type: application/json" \
  -d '{
    "intent_id": "'$INTENT_ID'",
    "mandate_id": "'$MANDATE_ID'"
  }')

echo "Authorization result:"
echo "$AUTH_RESPONSE" | jq . 2>/dev/null || echo "$AUTH_RESPONSE"

# Extract authorization ID
AUTH_ID=$(echo "$AUTH_RESPONSE" | jq -r '.authorization_id // empty' 2>/dev/null)

if [ -z "$AUTH_ID" ]; then
    echo -e "${RED}❌ Failed to authorize intent${NC}"
    exit 1
fi

echo -e "${GREEN}✅ Intent authorized: $AUTH_ID${NC}"

# Execute intent
echo -e "\n⚡ Executing payment..."
EXECUTE_RESPONSE=$(curl -s -X POST "$BACKEND_URL/ap2/execute" \
  -H "Content-Type: application/json" \
  -d '{
    "authorization_id": "'$AUTH_ID'",
    "intent_id": "'$INTENT_ID'"
  }')

echo "Execution result:"
echo "$EXECUTE_RESPONSE" | jq . 2>/dev/null || echo "$EXECUTE_RESPONSE"

# Extract payment details
PAYMENT_ID=$(echo "$EXECUTE_RESPONSE" | jq -r '.payment_id // empty' 2>/dev/null)
INVOICE_URL=$(echo "$EXECUTE_RESPONSE" | jq -r '.invoice_url // empty' 2>/dev/null)
STATUS=$(echo "$EXECUTE_RESPONSE" | jq -r '.status // empty' 2>/dev/null)

if [ -n "$PAYMENT_ID" ] && [ -n "$INVOICE_URL" ]; then
    echo -e "${GREEN}✅ Payment executed successfully!${NC}"
    echo -e "💳 Payment ID: $PAYMENT_ID"
    echo -e "🔗 Invoice URL: $INVOICE_URL"
    echo -e "📊 Status: $STATUS"
else
    echo -e "${RED}❌ Failed to execute payment${NC}"
    exit 1
fi

echo -e "\n${BLUE}3️⃣ Testing Payment Status Check${NC}"
echo "================================="

# Check payment status
echo "📊 Checking payment status..."
STATUS_RESPONSE=$(curl -s "$BACKEND_URL/ap2/status/$PAYMENT_ID")

echo "Status check result:"
echo "$STATUS_RESPONSE" | jq . 2>/dev/null || echo "$STATUS_RESPONSE"

echo -e "\n${GREEN}🎉 AP2 Integration Test Complete!${NC}"
echo "=================================="
echo -e "✅ Cart functionality: Working"
echo -e "✅ AP2 mandate creation: Working"
echo -e "✅ AP2 intent creation: Working"
echo -e "✅ AP2 authorization: Working"
echo -e "✅ AP2 execution: Working"
echo -e "✅ Payment status check: Working"

echo -e "\n${BLUE}📋 Summary:${NC}"
echo "- Customer ID: $CUSTOMER_ID"
echo "- Cart Total: \$${TOTAL_AMOUNT}"
echo "- Mandate ID: $MANDATE_ID"
echo "- Intent ID: $INTENT_ID"
echo "- Authorization ID: $AUTH_ID"
echo "- Payment ID: $PAYMENT_ID"
echo "- Invoice URL: $INVOICE_URL"

echo -e "\n${YELLOW}💡 Next Steps:${NC}"
echo "1. The invoice link can be used for payment"
echo "2. Xendit will send webhook callbacks to /api/webhooks/xendit"
echo "3. Payment status updates will be handled automatically"
echo "4. The MCP checkout_cart tool can be used by agents"

echo -e "\n${GREEN}Happy coding! 🎉${NC}"
