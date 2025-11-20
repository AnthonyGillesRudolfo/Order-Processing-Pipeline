#!/bin/bash
set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

echo -e "${BLUE}========================================${NC}"
echo -e "${BLUE}OpenBao Docker Initialization Script${NC}"
echo -e "${BLUE}========================================${NC}"
echo ""

# Check if OpenBao container is running
if ! docker ps | grep -q openbao; then
    echo -e "${RED}✗ OpenBao container is not running${NC}"
    echo -e "${YELLOW}Please start OpenBao first: docker compose up -d openbao${NC}"
    exit 1
fi

echo -e "${GREEN}✓ OpenBao container is running${NC}"

# Wait for OpenBao to be ready (it will return 503 when sealed, which is OK)
echo -e "${YELLOW}⏳ Waiting for OpenBao to be ready...${NC}"
for i in {1..30}; do
    # Check if the API is responding (even with 503 sealed status is OK)
    if docker exec openbao wget --spider http://localhost:8200/v1/sys/health 2>&1 | grep -qE "HTTP/1.1 (200|503)"; then
        echo -e "${GREEN}✓ OpenBao is ready${NC}"
        break
    fi
    if [ $i -eq 30 ]; then
        echo -e "${RED}✗ OpenBao failed to start after 30 seconds${NC}"
        echo -e "${YELLOW}Check logs with: docker compose logs openbao${NC}"
        exit 1
    fi
    sleep 1
done

# Check if OpenBao is already initialized
if docker exec openbao wget -qO- http://localhost:8200/v1/sys/init 2>/dev/null | grep -q '"initialized":true'; then
    echo -e "${YELLOW}⚠ OpenBao is already initialized${NC}"
    echo -e "${YELLOW}If you need to re-initialize, you must delete the ./data directory and restart the container${NC}"
    exit 0
fi

echo -e "${YELLOW}⏳ Initializing OpenBao with 3 key shares and threshold of 2...${NC}"

# Initialize OpenBao with 3 keys, requiring 2 to unseal
INIT_OUTPUT=$(docker exec openbao bao operator init -key-shares=3 -key-threshold=2 -format=json)

# Parse the output - get all 3 unseal keys
UNSEAL_KEY_1=$(echo "$INIT_OUTPUT" | grep -o '"unseal_keys_b64":\["[^"]*"' | cut -d'"' -f4)
UNSEAL_KEY_2=$(echo "$INIT_OUTPUT" | grep -o '"unseal_keys_b64":\["[^"]*","[^"]*"' | grep -o ',"[^"]*"$' | cut -d'"' -f2)
UNSEAL_KEY_3=$(echo "$INIT_OUTPUT" | grep -o '"unseal_keys_b64":\["[^"]*","[^"]*","[^"]*"' | grep -o ',"[^"]*"$' | cut -d'"' -f2)
ROOT_TOKEN=$(echo "$INIT_OUTPUT" | grep -o '"root_token":"[^"]*"' | cut -d'"' -f4)

if [ -z "$UNSEAL_KEY_1" ] || [ -z "$UNSEAL_KEY_2" ] || [ -z "$UNSEAL_KEY_3" ] || [ -z "$ROOT_TOKEN" ]; then
    echo -e "${RED}✗ Failed to parse initialization output${NC}"
    exit 1
fi

echo -e "${GREEN}✓ OpenBao initialized successfully${NC}"
echo ""
echo -e "${YELLOW}========================================${NC}"
echo -e "${YELLOW}IMPORTANT: Save these credentials securely!${NC}"
echo -e "${YELLOW}========================================${NC}"
echo -e "Unseal Key 1: ${GREEN}$UNSEAL_KEY_1${NC}"
echo -e "Unseal Key 2: ${GREEN}$UNSEAL_KEY_2${NC}"
echo -e "Unseal Key 3: ${GREEN}$UNSEAL_KEY_3${NC}"
echo -e "Root Token:   ${GREEN}$ROOT_TOKEN${NC}"
echo -e "${YELLOW}========================================${NC}"
echo ""

# Save credentials to init.txt (matches deployment guide)
CREDS_FILE="./init.txt"
cat > "$CREDS_FILE" <<EOF
OpenBao Initialization Output (Generated: $(date))
========================================
Unseal Key 1: $UNSEAL_KEY_1
Unseal Key 2: $UNSEAL_KEY_2
Unseal Key 3: $UNSEAL_KEY_3

Initial Root Token: $ROOT_TOKEN
========================================

IMPORTANT: Keep these credentials safe!
- Any 2 of the 3 unseal keys are needed to unseal OpenBao after restart
- The root token has full access to OpenBao
- Distribute the 3 unseal keys to different trusted parties for security

This file should NEVER be committed to version control!
EOF

echo -e "${GREEN}✓ Credentials saved to: $CREDS_FILE${NC}"
echo -e "${YELLOW}⚠ Make sure this file is in .gitignore!${NC}"
echo ""

# Unseal OpenBao (need 2 out of 3 keys)
echo -e "${YELLOW}⏳ Unsealing OpenBao (2 out of 3 keys required)...${NC}"
docker exec -e BAO_ADDR=http://127.0.0.1:8200 openbao bao operator unseal "$UNSEAL_KEY_1" > /dev/null
echo -e "${GREEN}✓ Unseal progress: 1/2${NC}"
docker exec -e BAO_ADDR=http://127.0.0.1:8200 openbao bao operator unseal "$UNSEAL_KEY_2" > /dev/null
echo -e "${GREEN}✓ OpenBao unsealed (2/2)${NC}"

# Export token for subsequent commands
export BAO_TOKEN="$ROOT_TOKEN"
export BAO_ADDR=http://127.0.0.1:8200

# Enable AppRole auth
echo -e "${YELLOW}⏳ Configuring AppRole authentication...${NC}"
docker exec -e BAO_ADDR=http://127.0.0.1:8200 -e BAO_TOKEN="$ROOT_TOKEN" openbao bao auth enable approle 2>/dev/null || echo -e "${YELLOW}AppRole already enabled${NC}"
echo -e "${GREEN}✓ AppRole enabled${NC}"

# Create policy for the application
echo -e "${YELLOW}⏳ Creating policy...${NC}"
docker exec -e BAO_ADDR=http://127.0.0.1:8200 -e BAO_TOKEN="$ROOT_TOKEN" openbao bao policy write myapp - <<EOF
path "secret/data/myapp/*" {
  capabilities = ["read", "list"]
}

path "secret/metadata/myapp/*" {
  capabilities = ["read", "list"]
}
EOF
echo -e "${GREEN}✓ Policy created${NC}"

# Create AppRole
echo -e "${YELLOW}⏳ Creating AppRole...${NC}"
docker exec -e BAO_ADDR=http://127.0.0.1:8200 -e BAO_TOKEN="$ROOT_TOKEN" openbao bao write auth/approle/role/myapp \
    token_policies="myapp" \
    token_ttl=1h \
    token_max_ttl=4h \
    secret_id_ttl=0 > /dev/null
echo -e "${GREEN}✓ AppRole created${NC}"

# Get RoleID and SecretID
echo -e "${YELLOW}⏳ Generating AppRole credentials...${NC}"
ROLE_ID=$(docker exec -e BAO_ADDR=http://127.0.0.1:8200 -e BAO_TOKEN="$ROOT_TOKEN" openbao bao read -field=role_id auth/approle/role/myapp/role-id)
SECRET_ID=$(docker exec -e BAO_ADDR=http://127.0.0.1:8200 -e BAO_TOKEN="$ROOT_TOKEN" openbao bao write -field=secret_id -f auth/approle/role/myapp/secret-id)

echo -e "${GREEN}✓ AppRole credentials generated${NC}"
echo ""

# Enable KV v2 secrets engine
echo -e "${YELLOW}⏳ Enabling KV v2 secrets engine...${NC}"
docker exec -e BAO_ADDR=http://127.0.0.1:8200 -e BAO_TOKEN="$ROOT_TOKEN" openbao bao secrets enable -version=2 -path=secret kv 2>/dev/null || echo -e "${YELLOW}KV secrets engine already enabled${NC}"
echo -e "${GREEN}✓ KV v2 secrets engine ready${NC}"

# Store default secrets (with placeholders for API keys)
echo -e "${YELLOW}⏳ Storing default secrets...${NC}"

# Store Xendit secrets (placeholders - update later)
docker exec -e BAO_ADDR=http://127.0.0.1:8200 -e BAO_TOKEN="$ROOT_TOKEN" openbao bao kv put secret/myapp/xendit \
    XENDIT_SECRET_KEY="your-xendit-secret-key" \
    XENDIT_CALLBACK_TOKEN="your-xendit-callback-token" > /dev/null

# Store OpenRouter secret (placeholder - update later)
docker exec -e BAO_ADDR=http://127.0.0.1:8200 -e BAO_TOKEN="$ROOT_TOKEN" openbao bao kv put secret/myapp/openrouter \
    OPENROUTER_API_KEY="your-openrouter-api-key" > /dev/null

# Store Database secret
docker exec -e BAO_ADDR=http://127.0.0.1:8200 -e BAO_TOKEN="$ROOT_TOKEN" openbao bao kv put secret/myapp/database \
    ORDER_DB_PASSWORD="postgres" \
    ORDER_DB_URL="postgres://orderpipelineadmin:postgres@postgres:5432/orderpipeline?sslmode=disable" > /dev/null

echo -e "${GREEN}✓ Default secrets stored successfully${NC}"
echo ""

# Append AppRole credentials to init.txt
cat >> "$CREDS_FILE" <<EOF

AppRole Credentials
========================================
Role ID: $ROLE_ID
Secret ID: $SECRET_ID
========================================

IMPORTANT: Add these to your .env file:
BAO_ROLE_ID=$ROLE_ID
BAO_SECRET_ID=$SECRET_ID
EOF

echo -e "${YELLOW}========================================${NC}"
echo -e "${YELLOW}Setup Complete!${NC}"
echo -e "${YELLOW}========================================${NC}"
echo ""
echo -e "${GREEN}✓ Secrets stored in OpenBao:${NC}"
echo -e "  - secret/myapp/database"
echo -e "  - secret/myapp/xendit (placeholder)"
echo -e "  - secret/myapp/openrouter (placeholder)"
echo ""
echo -e "${GREEN}✓ AppRole created${NC}"
echo ""
echo -e "${YELLOW}IMPORTANT: Save these credentials to .env:${NC}"
echo -e "${BLUE}BAO_ROLE_ID=$ROLE_ID${NC}"
echo -e "${BLUE}BAO_SECRET_ID=$SECRET_ID${NC}"
echo ""
echo -e "${YELLOW}Unseal keys saved to: init.txt${NC}"
echo -e "${RED}⚠️  Keep init.txt secure! You need it to unseal OpenBao.${NC}"
echo ""
echo -e "${YELLOW}To update API keys in OpenBao:${NC}"
echo -e "See DEPLOYMENT_GUIDE.md Step 5"
echo ""