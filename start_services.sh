#!/bin/bash

# Order Processing Pipeline - Service Startup Script
# This script helps start all required services for AP2 integration testing

echo "🚀 Starting Order Processing Pipeline Services"
echo "=============================================="

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Function to check if a service is running
check_service() {
    local service_name=$1
    local url=$2
    local expected_status=${3:-200}
    
    echo -n "Checking $service_name... "
    if curl -s -o /dev/null -w "%{http_code}" "$url" | grep -q "$expected_status"; then
        echo -e "${GREEN}✓ Running${NC}"
        return 0
    else
        echo -e "${RED}✗ Not running${NC}"
        return 1
    fi
}

# Function to start a service in background
start_service() {
    local service_name=$1
    local command=$2
    local log_file=$3
    
    echo -e "${BLUE}Starting $service_name...${NC}"
    nohup $command > "$log_file" 2>&1 &
    local pid=$!
    echo "Started $service_name (PID: $pid)"
    sleep 2
    return $pid
}

# Check prerequisites
echo -e "\n${YELLOW}📋 Checking Prerequisites${NC}"
echo "=========================="

# Check if required commands exist
for cmd in go python3 psql restate; do
    if command -v $cmd &> /dev/null; then
        echo -e "${GREEN}✓ $cmd is installed${NC}"
    else
        echo -e "${RED}✗ $cmd is not installed${NC}"
        echo "Please install $cmd before continuing"
        exit 1
    fi
done

# Check if services are already running
echo -e "\n${YELLOW}🔍 Checking Existing Services${NC}"
echo "=============================="

restate_running=false
if check_service "Restate Runtime" "http://localhost:8080/health"; then
    restate_running=true
fi

ap2_running=false
if check_service "AP2 Adapter" "http://localhost:7010/health" "200\|404"; then
    ap2_running=true
fi

backend_running=false
if check_service "Backend Server" "http://localhost:3000/api/orders"; then
    backend_running=true
fi

mcp_running=false
if check_service "MCP Server" "http://localhost:3001"; then
    mcp_running=true
fi

# Start services that aren't running
echo -e "\n${YELLOW}🚀 Starting Services${NC}"
echo "===================="

# Start Restate Runtime
if [ "$restate_running" = false ]; then
    echo "Starting Restate Runtime..."
    start_service "Restate Runtime" "restate dev" "restate.log"
    echo "Waiting for Restate to initialize..."
    sleep 5
fi

# Start Backend Server
if [ "$backend_running" = false ]; then
    echo "Starting Backend Server..."
    cd "$(dirname "$0")"
    start_service "Backend Server" "go run cmd/server/main.go" "backend.log"
    echo "Waiting for Backend to initialize..."
    sleep 3
fi

# Start AP2 Adapter (if available)
if [ "$ap2_running" = false ]; then
    echo -e "${YELLOW}Note: AP2 Adapter should be running on port 7010${NC}"
    echo "Please start your AP2 adapter service manually if needed"
fi

# Start MCP Server
if [ "$mcp_running" = false ]; then
    echo "Starting MCP Server..."
    cd "$(dirname "$0")/mcp-server"
    start_service "MCP Server" "python main.py" "../mcp.log"
    echo "Waiting for MCP Server to initialize..."
    sleep 2
fi

# Final status check
echo -e "\n${YELLOW}✅ Final Status Check${NC}"
echo "===================="

check_service "Restate Runtime" "http://localhost:8080/health"
check_service "Backend Server" "http://localhost:3000/api/orders"
check_service "MCP Server" "http://localhost:3001"

echo -e "\n${GREEN}🎉 Services Started!${NC}"
echo "===================="
echo "📊 Backend API: http://localhost:3000"
echo "🔧 Restate Admin: http://localhost:8080"
echo "🤖 MCP Server: http://localhost:3001"
echo "📝 AP2 Adapter: http://localhost:7010 (if running)"

echo -e "\n${BLUE}📋 Available MCP Tools:${NC}"
echo "- checkout_cart(customer_id)"
echo "- view_cart(customer_id)"
echo "- add_to_cart(customer_id, merchant_id, items)"
echo "- update_cart_item(customer_id, product_id, quantity)"
echo "- remove_from_cart(customer_id, product_ids)"

echo -e "\n${BLUE}🧪 Test the integration:${NC}"
echo "python test_ap2_integration.py"

echo -e "\n${YELLOW}📄 Log files:${NC}"
echo "- restate.log (Restate Runtime)"
echo "- backend.log (Backend Server)"
echo "- mcp.log (MCP Server)"

echo -e "\n${BLUE}🛑 To stop services:${NC}"
echo "pkill -f 'restate dev'"
echo "pkill -f 'go run cmd/server/main.go'"
echo "pkill -f 'python main.py'"

echo -e "\n${GREEN}Happy coding! 🎉${NC}"
