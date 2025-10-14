from typing import Any, List, Dict
import httpx
import json
from mcp.server.fastmcp import FastMCP

# Initialize the MCP server
mcp = FastMCP("Order Processing Pipeline MCP Server")

# Configuration
RESTATE_RUNTIME_URL = "http://127.0.0.1:8080"

async def make_restate_request(endpoint: str, method: str = "POST", data: Dict = None) -> Dict:
    """Make a request to the Restate runtime"""
    url = f"{RESTATE_RUNTIME_URL}{endpoint}"
    
    try:
        async with httpx.AsyncClient() as client:
            if method == "GET":
                response = await client.get(url)
            else:
                response = await client.post(url, json=data or {})
            
            response.raise_for_status()
            return response.json()
    except httpx.HTTPError as e:
        return {"error": f"HTTP error: {str(e)}"}
    except Exception as e:
        return {"error": f"Request failed: {str(e)}"}

@mcp.tool()
async def list_tools() -> str:
    """Show a list of all available MCP tools with their descriptions"""
    tools = [
        {
            "name": "list_tools",
            "description": "Show a list of all available MCP tools with their descriptions",
            "parameters": "None"
        },
        {
            "name": "list_merchants", 
            "description": "Show a list of all merchants in the system",
            "parameters": "None"
        },
        {
            "name": "list_merchant_items",
            "description": "Show a list of items that a specific merchant sells",
            "parameters": "merchant_id (required): The ID of the merchant to get items for"
        }
    ]
    
    result = "Available MCP Tools:\n\n"
    for tool in tools:
        result += f"• **{tool['name']}**\n"
        result += f"  Description: {tool['description']}\n"
        result += f"  Parameters: {tool['parameters']}\n\n"
    
    return result

@mcp.tool()
async def list_merchants() -> str:
    """Show a list of all merchants in the system"""
    # Since there's no direct API to list all merchants, we'll use the known merchants
    # from the sample data and try to get their information
    known_merchants = ["m_001", "m_002", "m_003"]
    merchants_info = []
    
    for merchant_id in known_merchants:
        # Try to get merchant info by calling GetMerchant
        endpoint = f"/merchant.sv1.MerchantService/{merchant_id}/GetMerchant"
        data = {"merchant_id": merchant_id}
        
        result = await make_restate_request(endpoint, "POST", data)
        
        if "error" not in result:
            merchant_name = result.get("name", "Unknown")
            items_count = len(result.get("items", []))
            merchants_info.append({
                "id": merchant_id,
                "name": merchant_name,
                "items_count": items_count
            })
        else:
            # If we can't get merchant info, still show the ID
            merchants_info.append({
                "id": merchant_id,
                "name": "Unknown",
                "items_count": 0
            })
    
    if not merchants_info:
        return "No merchants found or unable to connect to the system."
    
    result = "Available Merchants:\n\n"
    for merchant in merchants_info:
        result += f"• **{merchant['name']}** (ID: {merchant['id']})\n"
        result += f"  Items available: {merchant['items_count']}\n\n"
    
    return result

@mcp.tool()
async def list_merchant_items(merchant_id: str) -> str:
    """Show a list of items that a specific merchant sells
    
    Args:
        merchant_id: The ID of the merchant to get items for
    """
    if not merchant_id:
        return "Error: merchant_id is required. Please provide a merchant ID (e.g., 'm_001', 'm_002', 'm_003')."
    
    # Call the ListItems endpoint for the specific merchant
    endpoint = f"/merchant.sv1.MerchantService/{merchant_id}/ListItems"
    data = {
        "merchant_id": merchant_id,
        "page_size": 100,  # Get all items
        "page_token": ""
    }
    
    result = await make_restate_request(endpoint, "POST", data)
    
    if "error" in result:
        return f"Error retrieving items for merchant {merchant_id}: {result['error']}"
    
    items = result.get("items", [])
    
    if not items:
        return f"No items found for merchant {merchant_id}."
    
    response = f"Items for Merchant {merchant_id}:\n\n"
    
    for item in items:
        item_id = item.get("item_id", "Unknown")
        name = item.get("name", "Unknown")
        quantity = item.get("quantity", 0)
        price = item.get("price", 0.0)
        
        response += f"• **{name}** (ID: {item_id})\n"
        response += f"  Price: ${price:.2f}\n"
        response += f"  Quantity in stock: {quantity}\n\n"
    
    return response

# MCP Resources for merchant catalogs
@mcp.resource("merchant://{merchant_id}/items")
async def get_merchant_catalog(merchant_id: str) -> str:
    """Get the item catalog for a specific merchant as a resource"""
    if not merchant_id:
        return "Error: merchant_id is required in the resource URI."
    
    # Call the ListItems endpoint for the specific merchant
    endpoint = f"/merchant.sv1.MerchantService/{merchant_id}/ListItems"
    data = {
        "merchant_id": merchant_id,
        "page_size": 100,  # Get all items
        "page_token": ""
    }
    
    result = await make_restate_request(endpoint, "POST", data)
    
    if "error" in result:
        return f"Error retrieving catalog for merchant {merchant_id}: {result['error']}"
    
    items = result.get("items", [])
    
    if not items:
        return f"No items found for merchant {merchant_id}."
    
    # Format as JSON for resource consumption
    catalog_data = {
        "merchant_id": merchant_id,
        "items": items,
        "total_items": len(items)
    }
    
    return json.dumps(catalog_data, indent=2)

# Main entry point
if __name__ == "__main__":
    mcp.run()

