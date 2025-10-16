#!/usr/bin/env python3
"""
Test script for MCP checkout_cart tool
This script tests the checkout flow by simulating AP2 calls
"""

import asyncio
import httpx
import json
import uuid
from typing import Dict, Any

# Configuration
RESTATE_RUNTIME_URL = "http://127.0.0.1:8080"

async def test_checkout_flow():
    """Test the complete checkout flow"""
    print("🧪 Testing MCP Checkout Flow")
    print("=" * 40)
    
    customer_id = "customer-001"
    
    try:
        # Step 1: View current cart
        print("\n1️⃣ Viewing current cart...")
        cart_result = await view_cart(customer_id)
        print(f"   Cart: {json.dumps(cart_result, indent=2)}")
        
        # Step 2: Test the checkout_cart logic (without AP2 adapter)
        print("\n2️⃣ Testing checkout_cart logic...")
        checkout_result = await simulate_checkout_cart(customer_id)
        print(f"   Result: {checkout_result}")
        
        print("\n✅ Checkout Flow Test Complete!")
        
    except Exception as e:
        print(f"\n❌ Test failed: {e}")
        import traceback
        traceback.print_exc()

async def view_cart(customer_id: str) -> Dict[str, Any]:
    """View cart using Restate endpoint"""
    endpoint = f"/cart.sv1.CartService/{customer_id}/ViewCart"
    data = {"customer_id": customer_id}
    
    async with httpx.AsyncClient() as client:
        response = await client.post(f"{RESTATE_RUNTIME_URL}{endpoint}", json=data)
        return response.json()

async def simulate_checkout_cart(customer_id: str) -> str:
    """Simulate the checkout_cart MCP tool logic"""
    
    # Get the current cart contents
    cart_result = await view_cart(customer_id)
    
    if "error" in cart_result:
        return f"Error getting cart for checkout: {cart_result['error']}"
    
    cart_state = cart_result.get("cart_state", {})
    items = cart_state.get("items", [])
    total_amount = cart_state.get("total_amount", 0)
    
    if not items:
        return "Error: Cannot checkout with an empty cart."
    
    # Generate order ID
    order_id = f"ORD-{uuid.uuid4().hex[:8]}"
    
    print(f"   📦 Cart has {len(items)} items, total: ${total_amount}")
    print(f"   🆔 Generated order ID: {order_id}")
    
    # Simulate AP2 calls (since AP2 adapter is not running)
    print("   🔄 Simulating AP2 calls...")
    
    # 1. Create AP2 Payment Intent
    intent_data = {
        "mandate_id": "mdt_session",
        "customer_id": customer_id,
        "cart_id": customer_id,
        "shipping_address": {
            "address_line1": "123 Main St",
            "city": "Jakarta",
            "state": "DKI Jakarta",
            "postal_code": "10110",
            "country": "Indonesia",
            "delivery_method": "standard"
        }
    }
    
    print(f"   📝 Creating intent with data: {json.dumps(intent_data, indent=2)}")
    
    # Simulate intent creation response
    intent_result = {
        "intent_id": f"pi_{uuid.uuid4().hex[:8]}",
        "total_amount": total_amount,
        "items": items,
        "status": "CREATED"
    }
    
    intent_id = intent_result.get("intent_id")
    print(f"   ✅ Intent created: {intent_id}")
    
    # 2. Authorize the intent
    auth_data = {
        "intent_id": intent_id,
        "mandate_id": "mdt_session"
    }
    
    print(f"   🔐 Authorizing intent: {json.dumps(auth_data, indent=2)}")
    
    # Simulate authorization response
    auth_result = {
        "authorized": True,
        "authorization_id": f"auth_{uuid.uuid4().hex[:8]}",
        "message": "Payment authorized successfully"
    }
    
    authorization_id = auth_result.get("authorization_id")
    print(f"   ✅ Authorization successful: {authorization_id}")
    
    # 3. Execute the intent
    execute_data = {
        "authorization_id": authorization_id,
        "intent_id": intent_id
    }
    
    print(f"   ⚡ Executing payment: {json.dumps(execute_data, indent=2)}")
    
    # Simulate execution response
    execute_result = {
        "execution_id": f"exec_{uuid.uuid4().hex[:8]}",
        "status": "PENDING",
        "invoice_url": f"https://checkout-staging.xendit.co/web/{uuid.uuid4().hex}",
        "payment_id": f"pay_{uuid.uuid4().hex[:8]}",
        "order_id": order_id
    }
    
    payment_id = execute_result.get("payment_id")
    invoice_link = execute_result.get("invoice_url")
    status = execute_result.get("status")
    
    print(f"   ✅ Payment executed: {payment_id}")
    print(f"   🔗 Invoice link: {invoice_link}")
    
    if not invoice_link:
        return f"Error: No invoice link received from AP2 execution. Status: {status}"
    
    return f"✅ Checkout completed successfully!\n\n" \
           f"**Order ID:** {order_id}\n" \
           f"**Payment ID:** {payment_id}\n" \
           f"**Status:** {status}\n\n" \
           f"🔗 **Invoice Link:** {invoice_link}\n\n" \
           f"Please complete the payment using the invoice link above. " \
           f"I'll notify you once the payment is confirmed."

if __name__ == "__main__":
    print("🚀 Starting MCP Checkout Test")
    print("Make sure the Restate runtime is running on :8080")
    print()
    
    asyncio.run(test_checkout_flow())
