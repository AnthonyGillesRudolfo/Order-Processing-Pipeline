from mcpserver import mcp

def main():
    print("Starting Order Processing Pipeline MCP Server...")
    print("Available tools:")
    print("  - list_tools: Show all available MCP tools")
    print("  - list_merchants: Show all merchants in the system")
    print("  - list_merchant_items: Show items for a specific merchant")
    print("\nAvailable resources:")
    print("  - merchant://{merchant_id}/items: Get merchant catalog as JSON")
    print("\nStarting MCP server...")
    mcp.run()

if __name__ == "__main__":
    main()
