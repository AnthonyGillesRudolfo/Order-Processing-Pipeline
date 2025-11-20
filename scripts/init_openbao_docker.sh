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

echo -e "${YELLOW}⏳ Initializing OpenBao...${NC}"

# Initialize OpenBao
INIT_OUTPUT=$(docker exec openbao bao operator init -key-shares=1 -key-threshold=1 -format=json)

# Parse the output
UNSEAL_KEY=$(echo "$INIT_OUTPUT" | grep -o '"unseal_keys_b64":\["[^"]*"' | cut -d'"' -f4)
ROOT_TOKEN=$(echo "$INIT_OUTPUT" | grep -o '"root_token":"[^"]*"' | cut -d'"' -f4)

if [ -z "$UNSEAL_KEY" ] || [ -z "$ROOT_TOKEN" ]; then
    echo -e "${RED}✗ Failed to parse initialization output${NC}"
    exit 1
fi

echo -e "${GREEN}✓ OpenBao initialized successfully${NC}"
echo ""
echo -e "${YELLOW}========================================${NC}"
echo -e "${YELLOW}IMPORTANT: Save these credentials securely!${NC}"
echo -e "${YELLOW}========================================${NC}"
echo -e "Unseal Key: ${GREEN}$UNSEAL_KEY${NC}"
echo -e "Root Token: ${GREEN}$ROOT_TOKEN${NC}"
echo -e "${YELLOW}========================================${NC}"
echo ""

# Save credentials to a file (make sure it's in .gitignore)
CREDS_FILE="./openbao-credentials.txt"
cat > "$CREDS_FILE" <<EOF
OpenBao Credentials (Generated: $(date))
========================================
Unseal Key: $UNSEAL_KEY
Root Token: $ROOT_TOKEN
========================================

IMPORTANT: Keep these credentials safe!
- The unseal key is needed to unseal OpenBao after restart
- The root token has full access to OpenBao

This file should NEVER be committed to version control!
EOF

echo -e "${GREEN}✓ Credentials saved to: $CREDS_FILE${NC}"
echo -e "${YELLOW}⚠ Make sure this file is in .gitignore!${NC}"
echo ""

# Unseal OpenBao
echo -e "${YELLOW}⏳ Unsealing OpenBao...${NC}"
docker exec -e BAO_ADDR=http://127.0.0.1:8200 openbao bao operator unseal "$UNSEAL_KEY" > /dev/null
echo -e "${GREEN}✓ OpenBao unsealed${NC}"

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

# Store secrets
echo -e "${YELLOW}⏳ Storing secrets...${NC}"
echo ""
echo -e "${BLUE}Please provide your secrets:${NC}"
echo ""

read -p "Enter XENDIT_SECRET_KEY: " XENDIT_SECRET_KEY
read -p "Enter XENDIT_CALLBACK_TOKEN: " XENDIT_CALLBACK_TOKEN
read -p "Enter OPENROUTER_API_KEY: " OPENROUTER_API_KEY
read -p "Enter ORDER_DB_PASSWORD: " ORDER_DB_PASSWORD

echo ""

# Store Xendit secrets
docker exec -e BAO_ADDR=http://127.0.0.1:8200 -e BAO_TOKEN="$ROOT_TOKEN" openbao bao kv put secret/myapp/xendit \
    XENDIT_SECRET_KEY="$XENDIT_SECRET_KEY" \
    XENDIT_CALLBACK_TOKEN="$XENDIT_CALLBACK_TOKEN" > /dev/null

# Store OpenRouter secret
docker exec -e BAO_ADDR=http://127.0.0.1:8200 -e BAO_TOKEN="$ROOT_TOKEN" openbao bao kv put secret/myapp/openrouter \
    OPENROUTER_API_KEY="$OPENROUTER_API_KEY" > /dev/null

# Store Database secret
docker exec -e BAO_ADDR=http://127.0.0.1:8200 -e BAO_TOKEN="$ROOT_TOKEN" openbao bao kv put secret/myapp/database \
    ORDER_DB_PASSWORD="$ORDER_DB_PASSWORD" > /dev/null

echo -e "${GREEN}✓ All secrets stored successfully${NC}"
echo ""

# Append AppRole credentials to credentials file
cat >> "$CREDS_FILE" <<EOF

AppRole Credentials
========================================
Role ID: $ROLE_ID
Secret ID: $SECRET_ID
========================================

Add these to your .env file or docker-compose environment:
BAO_ROLE_ID=$ROLE_ID
BAO_SECRET_ID=$SECRET_ID
EOF

echo -e "${YELLOW}========================================${NC}"
echo -e "${YELLOW}Setup Complete!${NC}"
echo -e "${YELLOW}========================================${NC}"
echo ""
echo -e "${GREEN}AppRole Credentials:${NC}"
echo -e "Role ID: ${BLUE}$ROLE_ID${NC}"
echo -e "Secret ID: ${BLUE}$SECRET_ID${NC}"
echo ""
echo -e "${YELLOW}Next Steps:${NC}"
echo -e "1. Add these environment variables to your ${GREEN}.env${NC} file:"
echo -e "   ${BLUE}BAO_ROLE_ID=$ROLE_ID${NC}"
echo -e "   ${BLUE}BAO_SECRET_ID=$SECRET_ID${NC}"
echo ""
echo -e "2. Or export them in your shell:"
echo -e "   ${BLUE}export BAO_ROLE_ID=$ROLE_ID${NC}"
echo -e "   ${BLUE}export BAO_SECRET_ID=$SECRET_ID${NC}"
echo ""
echo -e "3. Rebuild your application:"
echo -e "   ${BLUE}docker compose build app emailworker${NC}"
echo ""
echo -e "4. Start your services:"
echo -e "   ${BLUE}docker compose up -d${NC}"
echo ""
echo -e "${GREEN}✓ OpenBao Web UI available at: http://localhost:8200${NC}"
echo -e "${YELLOW}  Login with root token: $ROOT_TOKEN${NC}"
echo ""
