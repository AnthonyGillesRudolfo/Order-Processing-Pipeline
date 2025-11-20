#!/bin/bash

# OpenBao Unseal Helper Script
# Use this to unseal OpenBao after container restart

set -e

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

echo -e "${YELLOW}OpenBao Unseal Helper${NC}"
echo ""

# Check if OpenBao container is running
if ! docker ps | grep -q openbao; then
    echo -e "${RED}✗ OpenBao container is not running${NC}"
    echo -e "${YELLOW}Start it with: docker compose up -d openbao${NC}"
    exit 1
fi

# Check seal status
SEAL_STATUS=$(docker exec -e BAO_ADDR=http://127.0.0.1:8200 openbao bao status -format=json 2>/dev/null || echo '{}')
SEALED=$(echo "$SEAL_STATUS" | grep -o '"sealed":[^,}]*' | cut -d':' -f2 | tr -d ' ')

if [ "$SEALED" = "false" ]; then
    echo -e "${GREEN}✓ OpenBao is already unsealed${NC}"
    docker exec -e BAO_ADDR=http://127.0.0.1:8200 openbao bao status
    exit 0
fi

echo -e "${YELLOW}⚠ OpenBao is currently sealed${NC}"
echo ""

# Check for credentials file
if [ -f "./openbao-credentials.txt" ]; then
    echo -e "${GREEN}Found credentials file: openbao-credentials.txt${NC}"
    UNSEAL_KEY=$(grep "Unseal Key:" ./openbao-credentials.txt | cut -d':' -f2 | tr -d ' ')
    
    if [ -n "$UNSEAL_KEY" ]; then
        echo -e "${YELLOW}Using unseal key from credentials file...${NC}"
        docker exec openbao bao operator unseal "$UNSEAL_KEY"
        echo ""
        echo -e "${GREEN}✓ OpenBao unsealed successfully${NC}"
        docker exec openbao bao status
        exit 0
    fi
fi

# Manual unseal
echo -e "${YELLOW}Please enter your unseal key:${NC}"
read -r UNSEAL_KEY

if [ -z "$UNSEAL_KEY" ]; then
    echo -e "${RED}✗ No unseal key provided${NC}"
    exit 1
fi

docker exec -e BAO_ADDR=http://127.0.0.1:8200 openbao bao operator unseal "$UNSEAL_KEY"
echo ""
echo -e "${GREEN}✓ OpenBao unsealed successfully${NC}"
docker exec -e BAO_ADDR=http://127.0.0.1:8200 openbao bao status
