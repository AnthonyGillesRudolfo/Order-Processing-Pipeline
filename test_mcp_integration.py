#!/usr/bin/env python3
"""
Test MCP checkout_cart integration with AP2 and Xendit
"""

import asyncio
import httpx
import json
import uuid
from typing import Dict, Any

# Configuration
RESTATE_RUNTIME_URL = "http://127.0.0.1:8080"
AP2_BASE = "http://127.0.0.1:3000"

async def test_mcp_checkout_flow():
    """Test the complete MCP checkout flow"""
    print("🤖 Testing MCP checkout_cart Integration")
    print("=" * 45)
    
    customer_id = "customer-001"
    
    try:
        # Step 1: View current cart
        print("\n1️⃣ Getting current cart...")
        cart_result = await view_cart(customer_id)
        print(f"   Cart: {json.dumps(cart_result, indent=2)}")
        
        cart_state = cart_result.get("cart_state", {})
        items = cart_state.get("items", [])
        total_amount = cart_state.get("total_amount", 0)
        
        if not items:
            print("   ⚠️ Cart is empty, adding items...")
            await add_items_to_cart(customer_id)
            cart_result = await view_cart(customer_id)
            cart_state = cart_result.get("cart_state", {})
            total_amount = cart_state.get("total_amount", 0)
        
        print(f"   💰 Cart total: ${total_amount}")
        
        # Step 2: Test MCP checkout_cart logic
        print("\n2️⃣ Testing MCP checkout_cart logic...")
        checkout_result = await simulate_checkout_cart(customer_id)
        print(f"   Result: {checkout_result}")
        
        print("\n✅ MCP Integration Test Complete!")
        
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

async def add_items_to_cart(customer_id: str):
    """Add items to cart"""
    endpoint = f"/cart.sv1.CartService/{customer_id}/AddToCart"
    data = {
        "customer_id": customer_id,
        "merchant_id": "m_001",
        "items": [
            {"product_id": "i_001", "quantity": 2},
            {"product_id": "i_002", "quantity": 1}
        ]
    }
    
    async with httpx.AsyncClient() as client:
        response = await client.post(f"{RESTATE_RUNTIME_URL}{endpoint}", json=data)
        return response.json()

async def simulate_checkout_cart(customer_id: str) -> str:
    """Simulate the MCP checkout_cart tool behavior"""
    
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
    
    print(f"   📝 Creating AP2 intent...")
    intent_result = await make_ap2_request("/ap2/intents", "POST", intent_data)
    
    if "error" in intent_result:
        return f"Error creating AP2 intent: {intent_result['error']}"
    
    intent_id = intent_result.get("intent_id")
    if not intent_id:
        return f"Error: No intent_id received from AP2 intent creation"
    
    print(f"   ✅ Intent created: {intent_id}")
    
    # 2. Authorize the intent
    auth_data = {
        "intent_id": intent_id,
        "mandate_id": "mdt_session"
    }
    
    print(f"   🔐 Authorizing intent...")
    auth_result = await make_ap2_request("/ap2/authorize", "POST", auth_data)
    
    if "error" in auth_result:
        return f"Error authorizing AP2 intent: {auth_result['error']}"
    
    authorization_id = auth_result.get("authorization_id")
    if not authorization_id:
        return f"Error: No authorization_id received from AP2 authorization"
    
    print(f"   ✅ Authorization successful: {authorization_id}")
    
    # 3. Execute the intent -> returns Xendit invoice link
    execute_data = {
        "authorization_id": authorization_id,
        "intent_id": intent_id
    }
    
    print(f"   ⚡ Executing payment with Xendit...")
    execute_result = await make_ap2_request("/ap2/execute", "POST", execute_data)
    
    if "error" in execute_result:
        return f"Error executing AP2 intent: {execute_result['error']}"
    
    payment_id = execute_result.get("payment_id")
    invoice_link = execute_result.get("invoice_url")
    status = execute_result.get("status")
    
    print(f"   ✅ Payment executed: {payment_id}")
    print(f"   🔗 Xendit invoice: {invoice_link}")
    
    if not invoice_link:
        return f"Error: No invoice link received from AP2 execution. Status: {status}"
    
    return f"✅ Checkout completed successfully!\n\n" \
           f"**Order ID:** {order_id}\n" \
           f"**Payment ID:** {payment_id}\n" \
           f"**Status:** {status}\n\n" \
           f"🔗 **Invoice Link:** {invoice_link}\n\n" \
           f"Please complete the payment using the invoice link above. " \
           f"I'll notify you once the payment is confirmed."

async def make_ap2_request(endpoint: str, method: str = "POST", data: Dict = None) -> Dict:
    """Make a request to the AP2 adapter service"""
    url = f"{AP2_BASE}{endpoint}"
    
    try:
        async with httpx.AsyncClient() as client:
            if method == "GET":
                response = await client.get(url)
            else:
                response = await client.post(url, json=data or {})
            
            response.raise_for_status()
            return response.json()
    except httpx.HTTPError as e:
        return {"error": f"AP2 HTTP error: {str(e)}"}
    except Exception as e:
        return {"error": f"AP2 request failed: {str(e)}"}

if __name__ == "__main__":
    print("🚀 Starting MCP Integration Test")
    print("Make sure the following services are running:")
    print("  - Restate runtime on :8080")
    print("  - Backend server on :3000")
    print()
    
    asyncio.run(test_mcp_checkout_flow())
