#!/usr/bin/env python3
"""
Test script for AP2 integration with MCP server
This script tests the complete checkout flow using the new checkout_cart tool
"""

import asyncio
import httpx
import json
import uuid
from typing import Dict, Any

# Configuration
RESTATE_RUNTIME_URL = "http://127.0.0.1:8080"
AP2_BASE = "http://127.0.0.1:7010"
MCP_SERVER_URL = "http://127.0.0.1:3000"

async def test_ap2_integration():
    """Test the complete AP2 integration flow"""
    print("🧪 Testing AP2 Integration with MCP Server")
    print("=" * 50)
    
    # Test customer ID
    customer_id = "customer-001"
    
    try:
        # Step 1: Add items to cart
        print("\n1️⃣ Adding items to cart...")
        add_to_cart_result = await add_to_cart_via_mcp(customer_id)
        print(f"   Result: {add_to_cart_result}")
        
        # Step 2: View cart
        print("\n2️⃣ Viewing cart...")
        cart_result = await view_cart_via_mcp(customer_id)
        print(f"   Cart contents: {cart_result}")
        
        # Step 3: Test AP2 endpoints directly
        print("\n3️⃣ Testing AP2 endpoints directly...")
        await test_ap2_endpoints()
        
        # Step 4: Test checkout_cart via MCP
        print("\n4️⃣ Testing checkout_cart via MCP...")
        checkout_result = await checkout_cart_via_mcp(customer_id)
        print(f"   Checkout result: {checkout_result}")
        
        print("\n✅ AP2 Integration Test Complete!")
        
    except Exception as e:
        print(f"\n❌ Test failed: {e}")
        import traceback
        traceback.print_exc()

async def add_to_cart_via_mcp(customer_id: str) -> str:
    """Add items to cart using the MCP server"""
    # This would normally be called through the MCP protocol
    # For testing, we'll call the Restate endpoint directly
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
        return response.text

async def view_cart_via_mcp(customer_id: str) -> str:
    """View cart using the MCP server"""
    endpoint = f"/cart.sv1.CartService/{customer_id}/ViewCart"
    data = {"customer_id": customer_id}
    
    async with httpx.AsyncClient() as client:
        response = await client.post(f"{RESTATE_RUNTIME_URL}{endpoint}", json=data)
        return response.text

async def test_ap2_endpoints():
    """Test AP2 endpoints directly"""
    test_order_id = f"TEST-{uuid.uuid4().hex[:8]}"
    test_intent_id = f"pi_{uuid.uuid4().hex[:8]}"
    
    # Test 1: Create intent
    print("   📝 Creating AP2 intent...")
    intent_data = {
        "id": test_intent_id,
        "mandate_id": "mdt_session",
        "amount": 100.0,
        "currency": "IDR",
        "order_id": test_order_id
    }
    
    async with httpx.AsyncClient() as client:
        try:
            response = await client.post(f"{AP2_BASE}/ap2/intents", json=intent_data)
            print(f"      Intent creation: {response.status_code} - {response.text}")
        except Exception as e:
            print(f"      Intent creation failed: {e}")
            return
        
        # Test 2: Authorize intent
        print("   🔐 Authorizing intent...")
        auth_data = {
            "intent_id": test_intent_id,
            "approved": True
        }
        
        try:
            response = await client.post(f"{AP2_BASE}/ap2/authorize", json=auth_data)
            print(f"      Authorization: {response.status_code} - {response.text}")
        except Exception as e:
            print(f"      Authorization failed: {e}")
            return
        
        # Test 3: Execute intent
        print("   ⚡ Executing intent...")
        try:
            response = await client.post(f"{AP2_BASE}/ap2/execute", params={"intent_id": test_intent_id})
            print(f"      Execution: {response.status_code} - {response.text}")
            
            if response.status_code == 200:
                result = response.json()
                payment_id = result.get("payment_id")
                if payment_id:
                    # Test 4: Get status
                    print("   📊 Getting payment status...")
                    try:
                        status_response = await client.get(f"{AP2_BASE}/ap2/status/{payment_id}")
                        print(f"      Status: {status_response.status_code} - {status_response.text}")
                    except Exception as e:
                        print(f"      Status check failed: {e}")
        
        except Exception as e:
            print(f"      Execution failed: {e}")

async def checkout_cart_via_mcp(customer_id: str) -> str:
    """Test checkout_cart via MCP server"""
    # This would normally be called through the MCP protocol
    # For testing, we'll simulate the MCP tool behavior
    
    # Get cart first
    cart_data = await view_cart_via_mcp(customer_id)
    cart_json = json.loads(cart_data)
    cart_state = cart_json.get("cart_state", {})
    items = cart_state.get("items", [])
    total_amount = cart_state.get("total_amount", 0)
    
    if not items:
        return "Error: Cart is empty"
    
    # Generate order ID
    order_id = f"ORD-{uuid.uuid4().hex[:8]}"
    intent_id = f"pi_{uuid.uuid4().hex[:8]}"
    
    # Create AP2 Payment Intent
    intent_data = {
        "id": intent_id,
        "mandate_id": "mdt_session",
        "amount": total_amount,
        "currency": "IDR",
        "order_id": order_id
    }
    
    async with httpx.AsyncClient() as client:
        # 1. Create intent
        intent_response = await client.post(f"{AP2_BASE}/ap2/intents", json=intent_data)
        if intent_response.status_code != 200:
            return f"Error creating AP2 intent: {intent_response.text}"
        
        # 2. Authorize
        auth_data = {"intent_id": intent_id, "approved": True}
        auth_response = await client.post(f"{AP2_BASE}/ap2/authorize", json=auth_data)
        if auth_response.status_code != 200:
            return f"Error authorizing AP2 intent: {auth_response.text}"
        
        # 3. Execute
        execute_response = await client.post(f"{AP2_BASE}/ap2/execute", params={"intent_id": intent_id})
        if execute_response.status_code != 200:
            return f"Error executing AP2 intent: {execute_response.text}"
        
        result = execute_response.json()
        payment_id = result.get("payment_id")
        invoice_link = result.get("provider_ref")
        status = result.get("status")
        
        return f"✅ Checkout completed!\nOrder ID: {order_id}\nPayment ID: {payment_id}\nInvoice Link: {invoice_link}\nStatus: {status}"

if __name__ == "__main__":
    print("🚀 Starting AP2 Integration Test")
    print("Make sure the following services are running:")
    print("  - Restate runtime on :8080")
    print("  - AP2 adapter on :7010")
    print("  - MCP server on :3000")
    print()
    
    asyncio.run(test_ap2_integration())
