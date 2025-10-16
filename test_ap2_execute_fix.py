#!/usr/bin/env python3
"""
Test script to verify that the AP2 execute endpoint returns an invoice URL immediately
"""

import httpx
import json
import asyncio

async def test_ap2_execute():
    """Test the AP2 execute endpoint to ensure it returns an invoice URL"""
    
    # Test data
    execute_data = {
        "authorization_id": "auth-test-123",
        "intent_id": "intent-test-456"
    }
    
    try:
        async with httpx.AsyncClient(timeout=10.0) as client:
            print("Testing AP2 execute endpoint...")
            response = await client.post(
                "http://localhost:3000/ap2/execute",
                json=execute_data
            )
            
            print(f"Status Code: {response.status_code}")
            print(f"Response Headers: {dict(response.headers)}")
            
            if response.status_code == 200:
                result = response.json()
                print(f"Response: {json.dumps(result, indent=2)}")
                
                # Check if we have an invoice URL
                invoice_url = None
                if "result" in result:
                    invoice_url = result["result"].get("invoiceLink")
                else:
                    invoice_url = result.get("invoice_url")
                
                if invoice_url:
                    print(f"✅ SUCCESS: Invoice URL found: {invoice_url}")
                    return True
                else:
                    print("❌ ERROR: No invoice URL found in response")
                    return False
            else:
                print(f"❌ ERROR: HTTP {response.status_code}")
                print(f"Response: {response.text}")
                return False
                
    except httpx.RequestError as e:
        print(f"❌ ERROR: Request failed: {e}")
        return False
    except Exception as e:
        print(f"❌ ERROR: Unexpected error: {e}")
        return False

async def main():
    """Main test function"""
    print("=" * 60)
    print("AP2 Execute Endpoint Test")
    print("=" * 60)
    
    success = await test_ap2_execute()
    
    print("=" * 60)
    if success:
        print("✅ Test PASSED: AP2 execute endpoint returns invoice URL")
    else:
        print("❌ Test FAILED: AP2 execute endpoint does not return invoice URL")
    print("=" * 60)

if __name__ == "__main__":
    asyncio.run(main())
