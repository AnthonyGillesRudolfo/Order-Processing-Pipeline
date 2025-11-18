#!/bin/bash

# OpenBao Unseal Helper Script
# Use this to unseal OpenBao after container restart
# Requires 2 out of 3 unseal keys

set -e

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
BLUE='\033[0;34m'
NC='\033[0m'

echo -e "${BLUE}========================================${NC}"
echo -e "${BLUE}OpenBao Unseal Helper${NC}"
echo -e "${BLUE}========================================${NC}"
echo ""

# Check if OpenBao container is running
if ! docker ps | grep -q openbao; then
    echo -e "${RED}✗ OpenBao container is not running${NC}"
    echo -e "${YELLOW}Start it with: docker compose up -d openbao${NC}"
    exit 1
fi

echo -e "${GREEN}✓ OpenBao container is running${NC}"
echo ""

# Check seal status
SEAL_STATUS=$(docker exec -e BAO_ADDR=http://127.0.0.1:8200 openbao bao status -format=json 2>/dev/null || echo '{}')
SEALED=$(echo "$SEAL_STATUS" | grep -o '"sealed":[^,}]*' | cut -d':' -f2 | tr -d ' ')

if [ "$SEALED" = "false" ]; then
    echo -e "${GREEN}✓ OpenBao is already unsealed${NC}"
    echo ""
    docker exec -e BAO_ADDR=http://127.0.0.1:8200 openbao bao status
    exit 0
fi

echo -e "${YELLOW}⚠ OpenBao is currently sealed${NC}"
echo -e "${YELLOW}Need 2 out of 3 unseal keys to unseal${NC}"
echo ""

# Check for init.txt file
if [ -f "./init.txt" ]; then
    echo -e "${GREEN}✓ Found init.txt file${NC}"
    UNSEAL_KEY_1=$(grep "Unseal Key 1:" ./init.txt | cut -d':' -f2 | tr -d ' ')
    UNSEAL_KEY_2=$(grep "Unseal Key 2:" ./init.txt | cut -d':' -f2 | tr -d ' ')
    
    if [ -n "$UNSEAL_KEY_1" ] && [ -n "$UNSEAL_KEY_2" ]; then
        echo -e "${YELLOW}⏳ Using unseal keys from init.txt...${NC}"
        echo ""
        
        # First unseal key
        docker exec -e BAO_ADDR=http://127.0.0.1:8200 openbao bao operator unseal "$UNSEAL_KEY_1" > /dev/null
        echo -e "${GREEN}✓ Unseal progress: 1/2${NC}"
        
        # Second unseal key
        docker exec -e BAO_ADDR=http://127.0.0.1:8200 openbao bao operator unseal "$UNSEAL_KEY_2" > /dev/null
        echo -e "${GREEN}✓ OpenBao unsealed (2/2)${NC}"
        echo ""
        echo -e "${GREEN}✓ OpenBao unsealed successfully${NC}"
        echo ""
        docker exec -e BAO_ADDR=http://127.0.0.1:8200 openbao bao status
        exit 0
    fi
fi

# Manual unseal (if init.txt not found or keys not in file)
echo -e "${YELLOW}Manual unseal mode${NC}"
echo -e "${YELLOW}Please enter 2 out of 3 unseal keys:${NC}"
echo ""

echo -e "${BLUE}Enter first unseal key:${NC}"
read -r UNSEAL_KEY_1

if [ -z "$UNSEAL_KEY_1" ]; then
    echo -e "${RED}✗ No unseal key provided${NC}"
    exit 1
fi

docker exec -e BAO_ADDR=http://127.0.0.1:8200 openbao bao operator unseal "$UNSEAL_KEY_1" > /dev/null
echo -e "${GREEN}✓ Unseal progress: 1/2${NC}"
echo ""

echo -e "${BLUE}Enter second unseal key:${NC}"
read -r UNSEAL_KEY_2

if [ -z "$UNSEAL_KEY_2" ]; then
    echo -e "${RED}✗ No unseal key provided${NC}"
    exit 1
fi

docker exec -e BAO_ADDR=http://127.0.0.1:8200 openbao bao operator unseal "$UNSEAL_KEY_2" > /dev/null
echo -e "${GREEN}✓ OpenBao unsealed (2/2)${NC}"
echo ""
echo -e "${GREEN}✓ OpenBao unsealed successfully${NC}"
echo ""
docker exec -e BAO_ADDR=http://127.0.0.1:8200 openbao bao status