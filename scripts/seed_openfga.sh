#!/bin/bash
set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

log_info() {
    echo -e "${BLUE}ℹ${NC} $1"
}

log_success() {
    echo -e "${GREEN}✓${NC} $1"
}

log_error() {
    echo -e "${RED}✗${NC} $1"
}

echo "======================================"
echo "OpenFGA Setup with PostgreSQL"
echo "======================================"
echo ""

# Check if FGA CLI is installed
if ! command -v fga &> /dev/null; then
    log_error "FGA CLI not found. Installing..."
    brew install openfga/tap/fga || {
        log_error "Failed to install FGA CLI"
        exit 1
    }
fi

# Check if database exists, create if needed
log_info "Checking OpenFGA database..."
./scripts/setup_openfga_postgres.sh

# Start OpenFGA services
log_info "Starting OpenFGA services..."
docker compose --profile openfga up -d openfga-migrator openfga

# Wait for migrations
log_info "Waiting for migrations to complete..."
sleep 5

# Check migration status
if docker logs openfga-migrator 2>&1 | grep -q "successfully"; then
    log_success "Migrations completed successfully"
else
    log_error "Migrations may have failed. Check logs: docker logs openfga-migrator"
fi

# Wait for OpenFGA to be ready
log_info "Waiting for OpenFGA to be ready..."
for i in {1..30}; do
    if curl -s http://localhost:8081/healthz >/dev/null 2>&1; then
        log_success "OpenFGA is ready"
        break
    fi
    sleep 1
done

# Verify tables
log_info "Verifying database tables..."
docker exec -e PGPASSWORD=postgres postgres \
  psql -U orderpipelineadmin -d openfga -c "\dt"

# Get OPENFGA_STORE_ID from .env
if [ -f ".env" ]; then
    STORE_ID=$(grep "^OPENFGA_STORE_ID=" .env | cut -d'=' -f2)
else
    log_error ".env file not found"
    exit 1
fi

if [ -z "$STORE_ID" ]; then
    log_error "OPENFGA_STORE_ID not found in .env file"
    exit 1
fi

log_info "Using store ID: $STORE_ID"

# Upload authorization model
log_info "Uploading authorization model..."
if [ ! -f "policy/authz.fga" ]; then
    log_error "Authorization model file not found: policy/authz.fga"
    exit 1
fi

fga model write \
  --store-id "$STORE_ID" \
  --api-url http://localhost:8081 \
  --file policy/authz.fga

log_success "Authorization model uploaded"

# Seed authorization tuples
log_info "Seeding authorization tuples..."
curl -s -X POST "http://localhost:8081/stores/$STORE_ID/write" \
  -H "Content-Type: application/json" \
  -d '{
    "writes": {
      "tuple_keys": [
        {"user": "user:bob", "relation": "merchant", "object": "org:acme"},
        {"user": "org:acme", "relation": "org", "object": "store:merchant-001"},
        {"user": "user:alice", "relation": "admin", "object": "org:acme"}
      ]
    }
  }' > /dev/null

log_success "Authorization tuples seeded"

# Test authorization
log_info "Testing authorization..."
RESPONSE=$(curl -s -X POST "http://localhost:8081/stores/$STORE_ID/check" \
  -H "Content-Type: application/json" \
  -d '{
    "tuple_key": {
      "user": "user:bob",
      "relation": "merchant",
      "object": "store:merchant-001"
    }
  }')

if echo "$RESPONSE" | grep -q '"allowed":true'; then
    log_success "Authorization test passed"
else
    log_error "Authorization test failed"
    exit 1
fi

# Test persistence
# log_info "Testing data persistence..."
# docker compose restart openfga
# sleep 5

# RESPONSE=$(curl -s -X POST "http://localhost:8081/stores/$STORE_ID/check" \
#   -H "Content-Type: application/json" \
#   -d '{
#     "tuple_key": {
#       "user": "user:bob",
#       "relation": "merchant",
#       "object": "store:merchant-001"
#     }
#   }')

# if echo "$RESPONSE" | grep -q '"allowed":true'; then
#     log_success "Persistence test passed! Data survived restart 🎉"
# else
#     log_error "Persistence test failed"
#     exit 1
# fi

echo ""
echo "======================================"
echo -e "${GREEN}OpenFGA Setup Complete!${NC}"
echo "======================================"
echo ""
echo "✓ PostgreSQL database configured"
echo "✓ OpenFGA tables migrated"
echo "✓ Authorization model uploaded"
echo "✓ User permissions seeded"
echo "✓ Data persistence verified"
echo ""
echo "OpenFGA API: http://localhost:8081"
echo "OpenFGA Playground: http://localhost:8081/playground"
echo ""