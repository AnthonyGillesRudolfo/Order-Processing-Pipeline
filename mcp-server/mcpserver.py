from typing import Any, List, Dict
import httpx
import json
import uuid
import os
from mcp.server.fastmcp import FastMCP

# Initialize the MCP server
mcp = FastMCP("Order Processing Pipeline MCP Server")

# Configuration
RESTATE_RUNTIME_URL = "http://127.0.0.1:8080"
AP2_BASE = os.getenv("AP2_BASE", "http://localhost:3000")

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

async def make_ap2_request(endpoint: str, method: str = "POST", data: Dict = None, params: Dict = None) -> Dict:
    """Make a request to the AP2 adapter service"""
    url = f"{AP2_BASE}{endpoint}"
    
    try:
        async with httpx.AsyncClient(timeout=30.0) as client:
            if method == "GET":
                response = await client.get(url, params=params)
            else:
                response = await client.post(url, json=data or {}, params=params)
            
            response.raise_for_status()
            return response.json()
    except httpx.HTTPStatusError as e:
        error_body = ""
        try:
            error_body = await e.response.aread()
            error_body = error_body.decode('utf-8')
        except:
            pass
        return {"error": f"AP2 HTTP error {e.response.status_code}: {error_body or str(e)}"}
    except httpx.RequestError as e:
        return {"error": f"AP2 request error: {str(e)}"}
    except Exception as e:
        return {"error": f"AP2 request failed: {str(e)}"}

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
        },
        {
            "name": "add_to_cart",
            "description": "Add items to a customer's shopping cart",
            "parameters": "customer_id (required), merchant_id (required), items (required): List of items with product_id and quantity"
        },
        {
            "name": "view_cart",
            "description": "View the current contents of a customer's shopping cart",
            "parameters": "customer_id (required): The ID of the customer"
        },
        {
            "name": "update_cart_item",
            "description": "Update the quantity of an item in the cart",
            "parameters": "customer_id (required), product_id (required), quantity (required): New quantity for the item"
        },
        {
            "name": "remove_from_cart",
            "description": "Remove items from the cart",
            "parameters": "customer_id (required), product_ids (required): List of product IDs to remove"
        },
        {
            "name": "checkout",
            "description": "Initiate checkout process (triggers AP2 flow, verifies user intent)",
            "parameters": "customer_id (required): The ID of the customer"
        },
        {
            "name": "checkout_cart",
            "description": "Checkout and create a Xendit invoice for the current cart using AP2 integration",
            "parameters": "customer_id (required): The ID of the customer"
        },
        {
            "name": "get_shipping_preferences",
            "description": "Retrieve shipping address and delivery preferences for a customer",
            "parameters": "customer_id (required): The ID of the customer"
        },
        {
            "name": "set_shipping_preferences",
            "description": "Update shipping information for a customer",
            "parameters": "customer_id (required), address_line1, address_line2, city, state, postal_code, country, delivery_method"
        },
        {
            "name": "list_orders",
            "description": "List all orders with comprehensive details (items, prices, order status, payment status, shipping status)",
            "parameters": "None"
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
        item_id = item.get("itemId", "Unknown")
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

# Cart Management MCP Tools

async def resolve_product_id(merchant_id: str, product_name_or_id: str) -> str:
    """Resolve a product name to its ID, or return the ID if already provided"""
    # If it looks like an ID (starts with letter and underscore), treat it as an ID
    if product_name_or_id.startswith(('i_', 'f_', 'b_')) and '_' in product_name_or_id:
        return product_name_or_id
    
    # First, try to get the merchant's items
    endpoint = f"/merchant.sv1.MerchantService/{merchant_id}/ListItems"
    data = {
        "merchant_id": merchant_id,
        "page_size": 100,
        "page_token": ""
    }
    
    result = await make_restate_request(endpoint, "POST", data)
    
    if "error" in result:
        # If we can't get items, assume it's already an ID
        return product_name_or_id
    
    items = result.get("items", [])
    
    # Look for exact match by name (case-insensitive)
    for item in items:
        if item.get("name", "").lower() == product_name_or_id.lower():
            return item.get("itemId", product_name_or_id)
    
    # If no match found, assume it's already an ID
    return product_name_or_id

@mcp.tool()
async def add_to_cart(customer_id: str, merchant_id: str, items: List[Dict[str, Any]]) -> str:
    """Add items to a customer's shopping cart
    
    Args:
        customer_id: The ID of the customer
        merchant_id: The ID of the merchant
        items: List of items with product_id and quantity
    """
    if not customer_id or not merchant_id or not items:
        return "Error: customer_id, merchant_id, and items are required."
    
    # Resolve product names to product IDs
    resolved_items = []
    for item in items:
        product_id = await resolve_product_id(merchant_id, item.get("product_id", ""))
        resolved_items.append({
            "product_id": product_id,
            "quantity": item.get("quantity", 1)
        })
    
    # Call the AddToCart endpoint for the specific customer
    endpoint = f"/cart.sv1.CartService/{customer_id}/AddToCart"
    data = {
        "customer_id": customer_id,
        "merchant_id": merchant_id,
        "items": resolved_items
    }
    
    result = await make_restate_request(endpoint, "POST", data)
    
    if "error" in result:
        return f"Error adding items to cart: {result['error']}"
    
    if result.get("success", False):
        cart_state = result.get("cart_state", {})
        items_count = len(cart_state.get("items", []))
        total_amount = cart_state.get("total_amount", 0)
        return f"Successfully added items to cart! Cart now contains {items_count} items with total amount: ${total_amount:.2f}"
    else:
        return f"Failed to add items to cart: {result.get('message', 'Unknown error')}"

@mcp.tool()
async def view_cart(customer_id: str) -> str:
    """View the current contents of a customer's shopping cart
    
    Args:
        customer_id: The ID of the customer
    """
    if not customer_id:
        return "Error: customer_id is required."
    
    # Call the ViewCart endpoint for the specific customer
    endpoint = f"/cart.sv1.CartService/{customer_id}/ViewCart"
    data = {
        "customer_id": customer_id
    }
    
    result = await make_restate_request(endpoint, "POST", data)
    
    if "error" in result:
        return f"Error viewing cart: {result['error']}"
    
    cart_state = result.get("cart_state", {})
    items = cart_state.get("items", [])
    total_amount = cart_state.get("total_amount", 0)
    merchant_id = cart_state.get("merchant_id", "Unknown")
    
    if not items:
        return f"Cart is empty for customer {customer_id}."
    
    response = f"Cart for customer {customer_id} (Merchant: {merchant_id}):\n\n"
    response += f"Total Amount: ${total_amount:.2f}\n\n"
    response += "Items:\n"
    
    for item in items:
        product_id = item.get("product_id", "Unknown")
        name = item.get("name", "Unknown")
        quantity = item.get("quantity", 0)
        unit_price = item.get("unit_price", 0.0)
        item_total = quantity * unit_price
        
        response += f"• **{name}** (ID: {product_id})\n"
        response += f"  Quantity: {quantity}\n"
        response += f"  Unit Price: ${unit_price:.2f}\n"
        response += f"  Item Total: ${item_total:.2f}\n\n"
    
    return response

@mcp.tool()
async def update_cart_item(customer_id: str, product_id: str, quantity: int) -> str:
    """Update the quantity of an item in the cart
    
    Args:
        customer_id: The ID of the customer
        product_id: The ID of the product to update (can be product name or ID)
        quantity: New quantity for the item
    """
    if not customer_id or not product_id:
        return "Error: customer_id and product_id are required."
    
    # First get the merchant_id from the cart to resolve product names
    cart_endpoint = f"/cart.sv1.CartService/{customer_id}/ViewCart"
    cart_data = {"customer_id": customer_id}
    
    cart_result = await make_restate_request(cart_endpoint, "POST", cart_data)
    merchant_id = cart_result.get("cart_state", {}).get("merchant_id", "m_001")
    
    # Resolve product name to ID if needed
    resolved_product_id = await resolve_product_id(merchant_id, product_id)
    
    # Call the UpdateCartItem endpoint for the specific customer
    endpoint = f"/cart.sv1.CartService/{customer_id}/UpdateCartItem"
    data = {
        "customer_id": customer_id,
        "product_id": resolved_product_id,
        "quantity": quantity
    }
    
    result = await make_restate_request(endpoint, "POST", data)
    
    if "error" in result:
        return f"Error updating cart item: {result['error']}"
    
    if result.get("success", False):
        cart_state = result.get("cart_state", {})
        items_count = len(cart_state.get("items", []))
        total_amount = cart_state.get("total_amount", 0)
        return f"Successfully updated cart item! Cart now contains {items_count} items with total amount: ${total_amount:.2f}"
    else:
        return f"Failed to update cart item: {result.get('message', 'Unknown error')}"

@mcp.tool()
async def remove_from_cart(customer_id: str, product_ids: List[str]) -> str:
    """Remove items from the cart
    
    Args:
        customer_id: The ID of the customer
        product_ids: List of product IDs to remove
    """
    if not customer_id or not product_ids:
        return "Error: customer_id and product_ids are required."
    
    # Call the RemoveFromCart endpoint for the specific customer
    endpoint = f"/cart.sv1.CartService/{customer_id}/RemoveFromCart"
    data = {
        "customer_id": customer_id,
        "product_ids": product_ids
    }
    
    result = await make_restate_request(endpoint, "POST", data)
    
    if "error" in result:
        return f"Error removing items from cart: {result['error']}"
    
    if result.get("success", False):
        cart_state = result.get("cart_state", {})
        items_count = len(cart_state.get("items", []))
        total_amount = cart_state.get("total_amount", 0)
        return f"Successfully removed items from cart! Cart now contains {items_count} items with total amount: ${total_amount:.2f}"
    else:
        return f"Failed to remove items from cart: {result.get('message', 'Unknown error')}"

# Checkout and Shipping MCP Tools

@mcp.tool()
async def checkout(customer_id: str) -> str:
    """Initiate checkout process (triggers AP2 flow, verifies user intent)
    
    Args:
        customer_id: The ID of the customer
    """
    if not customer_id:
        return "Error: customer_id is required."
    
    # First, get the current cart to verify it's not empty
    cart_endpoint = f"/cart.sv1.CartService/{customer_id}/ViewCart"
    cart_data = {"customer_id": customer_id}
    
    cart_result = await make_restate_request(cart_endpoint, "POST", cart_data)
    
    if "error" in cart_result:
        return f"Error getting cart for checkout: {cart_result['error']}"
    
    cart_state = cart_result.get("cart_state", {})
    items = cart_state.get("items", [])
    total_amount = cart_state.get("total_amount", 0)
    
    if not items:
        return "Error: Cannot checkout with an empty cart."
    
    # Create AP2 mandate (simplified - in real implementation, this would require user confirmation)
    import datetime
    future_date = (datetime.datetime.now() + datetime.timedelta(days=365)).strftime("%Y-%m-%dT%H:%M:%SZ")
    mandate_data = {
        "customer_id": customer_id,
        "scope": "purchase",
        "amount_limit": total_amount * 1.1,  # Allow 10% buffer
        "expires_at": future_date  # Set expiration far in future
    }
    
    mandate_result = await make_ap2_request("/ap2/mandates", "POST", mandate_data)
    
    if "error" in mandate_result:
        return f"Error creating mandate: {mandate_result['error']}"
    
    mandate_id = mandate_result.get("mandate_id")
    
    # Create AP2 intent
    intent_data = {
        "mandate_id": mandate_id,
        "customer_id": customer_id,
        "cart_id": customer_id,  # Using customer_id as cart_id
        "shipping_address": {
            "address_line1": "123 Main St",
            "city": "Jakarta",
            "state": "DKI Jakarta",
            "postal_code": "10110",
            "country": "Indonesia",
            "delivery_method": "standard"
        }
    }
    
    intent_result = await make_ap2_request("/ap2/intents", "POST", intent_data)
    
    if "error" in intent_result:
        return f"Error creating intent: {intent_result['error']}"
    
    intent_id = intent_result.get("intent_id")
    
    # Authorize the payment
    auth_data = {
        "intent_id": intent_id,
        "mandate_id": mandate_id
    }
    
    auth_result = await make_ap2_request("/ap2/authorize", "POST", auth_data)
    
    if "error" in auth_result:
        return f"Error authorizing payment: {auth_result['error']}"
    
    if not auth_result.get("authorized", False):
        return f"Payment authorization failed: {auth_result.get('message', 'Unknown error')}"
    
    authorization_id = auth_result.get("authorization_id")
    
    # Execute the payment
    execute_data = {
        "authorization_id": authorization_id,
        "intent_id": intent_id
    }
    
    execute_result = await make_ap2_request("/ap2/execute", "POST", execute_data)
    
    if "error" in execute_result:
        print(f"DEBUG: Execute result error: {execute_result['error']}")
        return f"Error executing payment: {execute_result['error']}"
    
    # Handle AP2 envelope response format
    result_data = execute_result.get("result", execute_result)  # Fallback to direct response
    
    execution_id = result_data.get("execution_id") or result_data.get("executionId")
    invoice_url = result_data.get("invoice_url") or result_data.get("invoiceLink")
    order_id = result_data.get("order_id") or result_data.get("orderId")
    payment_id = result_data.get("payment_id") or result_data.get("paymentId")
    status = result_data.get("status")
    
    # Clear the cart after successful checkout
    clear_cart_endpoint = f"/cart.sv1.CartService/{customer_id}/ClearCart"
    clear_cart_data = {"customer_id": customer_id}
    
    clear_result = await make_restate_request(clear_cart_endpoint, "POST", clear_cart_data)
    
    if "error" in clear_result:
        # Log the error but don't fail the checkout
        print(f"Warning: Failed to clear cart after checkout: {clear_result['error']}")
    
    return f"✅ Checkout initiated successfully!\n\n" \
           f"**Order ID:** {order_id or 'Generated'}\n" \
           f"**Payment ID:** {payment_id}\n" \
           f"**Execution ID:** {execution_id}\n" \
           f"**Status:** {status}\n\n" \
           f"🔗 **Invoice URL:** {invoice_url}\n\n" \
           f"Please complete payment using the invoice URL above. " \
           f"Your cart has been cleared and the payment will be processed once you complete it on the invoice page."

@mcp.tool()
async def checkout_cart(customer_id: str) -> str:
    """Checkout and create a Xendit invoice for the current cart using AP2 integration
    
    Args:
        customer_id: The ID of the customer
    """
    if not customer_id:
        return "Error: customer_id is required."
    
    # Get the current cart contents
    cart_endpoint = f"/cart.sv1.CartService/{customer_id}/ViewCart"
    cart_data = {"customer_id": customer_id}
    
    cart_result = await make_restate_request(cart_endpoint, "POST", cart_data)
    
    if "error" in cart_result:
        return f"Error getting cart for checkout: {cart_result['error']}"
    
    cart_state = cart_result.get("cart_state", {})
    items = cart_state.get("items", [])
    total_amount = cart_state.get("total_amount", 0)
    
    if not items:
        return "Error: Cannot checkout with an empty cart."
    
    # Generate order ID
    order_id = f"ORD-{uuid.uuid4().hex[:8]}"
    
    # 1. Create AP2 Mandate first
    import datetime
    future_date = (datetime.datetime.now() + datetime.timedelta(days=365)).strftime("%Y-%m-%dT%H:%M:%SZ")
    mandate_data = {
        "customer_id": customer_id,
        "scope": "purchase",
        "amount_limit": total_amount * 1.1,  # Allow 10% buffer
        "expires_at": future_date
    }
    
    mandate_result = await make_ap2_request("/ap2/mandates", "POST", mandate_data)
    
    if "error" in mandate_result:
        return f"Error creating mandate: {mandate_result['error']}"
    
    mandate_id = mandate_result.get("mandate_id")
    
    # 2. Create AP2 Payment Intent
    intent_data = {
        "mandate_id": mandate_id,
        "customer_id": customer_id,
        "cart_id": customer_id,  # Using customer_id as cart_id
        "shipping_address": {
            "address_line1": "123 Main St",
            "city": "Jakarta",
            "state": "DKI Jakarta",
            "postal_code": "10110",
            "country": "Indonesia",
            "delivery_method": "standard"
        }
    }
    
    intent_result = await make_ap2_request("/ap2/intents", "POST", intent_data)
    
    if "error" in intent_result:
        return f"Error creating AP2 intent: {intent_result['error']}"
    
    intent_id = intent_result.get("intent_id")
    if not intent_id:
        return f"Error: No intent_id received from AP2 intent creation"
    
    # 3. Authorize the intent
    auth_data = {
        "intent_id": intent_id,
        "mandate_id": mandate_id
    }
    
    auth_result = await make_ap2_request("/ap2/authorize", "POST", auth_data)
    
    if "error" in auth_result:
        return f"Error authorizing AP2 intent: {auth_result['error']}"
    
    authorization_id = auth_result.get("authorization_id")
    if not authorization_id:
        return f"Error: No authorization_id received from AP2 authorization"
    
    # 3. Execute the intent -> returns invoice link
    execute_data = {
        "authorization_id": authorization_id,
        "intent_id": intent_id
    }
    
    execute_result = await make_ap2_request("/ap2/execute", "POST", execute_data)
    
    if "error" in execute_result:
        print(f"DEBUG: AP2 Execute result error: {execute_result['error']}")
        return f"Error executing AP2 intent: {execute_result['error']}"
    
    # Handle AP2 envelope response format
    result_data = execute_result.get("result", execute_result)  # Fallback to direct response
    
    payment_id = result_data.get("payment_id") or result_data.get("paymentId")
    invoice_link = result_data.get("invoice_url") or result_data.get("invoiceLink")
    status = result_data.get("status")
    execution_id = result_data.get("execution_id") or result_data.get("executionId")
    order_id = result_data.get("order_id") or result_data.get("orderId")
    
    if not invoice_link:
        return f"Error: No invoice link received from AP2 execution. Status: {status}"
    
    # Clear the cart after successful checkout
    clear_cart_endpoint = f"/cart.sv1.CartService/{customer_id}/ClearCart"
    clear_cart_data = {"customer_id": customer_id}
    
    clear_result = await make_restate_request(clear_cart_endpoint, "POST", clear_cart_data)
    
    if "error" in clear_result:
        # Log the error but don't fail the checkout
        print(f"Warning: Failed to clear cart after checkout: {clear_result['error']}")
    
    return f"✅ Checkout completed successfully!\n\n" \
           f"**Order ID:** {order_id or 'Generated'}\n" \
           f"**Payment ID:** {payment_id}\n" \
           f"**Execution ID:** {execution_id}\n" \
           f"**Status:** {status}\n\n" \
           f"🔗 **Invoice Link:** {invoice_link}\n\n" \
           f"Please complete the payment using the invoice link above. " \
           f"Your cart has been cleared and the payment will be processed once you complete it on the invoice page."

@mcp.tool()
async def get_shipping_preferences(customer_id: str) -> str:
    """Retrieve shipping address and delivery preferences for a customer
    
    Args:
        customer_id: The ID of the customer
    """
    if not customer_id:
        return "Error: customer_id is required."
    
    # For now, return a placeholder response since we haven't implemented the database query yet
    return f"Shipping preferences for customer {customer_id}:\n\nAddress: 123 Main St, San Francisco, CA 94105, USA\nDelivery Method: Standard\n\nNote: This is a placeholder response. Shipping preferences will be stored in the database."

@mcp.tool()
async def set_shipping_preferences(customer_id: str, address_line1: str = "", address_line2: str = "", 
                                 city: str = "", state: str = "", postal_code: str = "", 
                                 country: str = "", delivery_method: str = "") -> str:
    """Update shipping information for a customer
    
    Args:
        customer_id: The ID of the customer
        address_line1: First line of address
        address_line2: Second line of address (optional)
        city: City name
        state: State or province
        postal_code: Postal or ZIP code
        country: Country name
        delivery_method: Delivery method preference
    """
    if not customer_id:
        return "Error: customer_id is required."
    
    # For now, return a placeholder response since we haven't implemented the database update yet
    return f"Shipping preferences updated for customer {customer_id}:\n\nAddress: {address_line1}, {city}, {state} {postal_code}, {country}\nDelivery Method: {delivery_method}\n\nNote: This is a placeholder response. Shipping preferences will be stored in the database."

@mcp.tool()
async def list_orders() -> str:
    """List all orders with comprehensive details (items, prices, order status, payment status, shipping status)"""
    
    # Call the orders API endpoint on the main server (not Restate runtime)
    result = await make_ap2_request("/api/orders", "GET")
    
    if "error" in result:
        return f"Error retrieving orders: {result['error']}"
    
    orders = result.get("orders", [])
    
    if not orders:
        return "No orders found."
    
    response = f"Found {len(orders)} orders:\n\n"
    
    for order in orders:
        order_id = order.get("id", "Unknown")
        customer_id = order.get("customer_id", "Unknown")
        status = order.get("status", "Unknown")
        total_amount = order.get("total_amount", 0)
        payment_status = order.get("payment_status", "Unknown")
        invoice_url = order.get("invoice_url", "")
        items = order.get("items", [])
        updated_at = order.get("updated_at", "Unknown")
        
        response += f"**Order {order_id}**\n"
        response += f"Customer: {customer_id}\n"
        response += f"Status: {status}\n"
        response += f"Total Amount: ${total_amount:.2f}\n"
        response += f"Payment Status: {payment_status}\n"
        if invoice_url:
            response += f"Invoice URL: {invoice_url}\n"
        response += f"Updated: {updated_at}\n"
        
        if items:
            response += "Items:\n"
            for item in items:
                item_name = item.get("name", "Unknown")
                item_quantity = item.get("quantity", 0)
                item_price = item.get("unit_price", 0)
                response += f"  • {item_name} (Qty: {item_quantity}, Price: ${item_price:.2f})\n"
        
        response += "\n"
    
    return response

# Main entry point
if __name__ == "__main__":
    mcp.run()

