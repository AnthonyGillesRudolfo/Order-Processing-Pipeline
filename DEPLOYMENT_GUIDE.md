# Order Processing Pipeline - Complete Deployment Guide

This guide will walk you through running the entire Order Processing Pipeline with OpenBao secret management, OpenFGA authorization, and all supporting services.

## Table of Contents
- [Prerequisites](#prerequisites)
- [Architecture Overview](#architecture-overview)
- [Quick Start](#quick-start)
- [Detailed Setup](#detailed-setup)
- [Initial OpenBao Setup (First-Time Only)](#initial-openbao-setup-first-time-only)
- [Verification](#verification)
- [Troubleshooting](#troubleshooting)

---

## Prerequisites

### Required Software
- **Docker Desktop**: Latest version
- **Docker Compose**: v2.0 or higher
- **Homebrew** (macOS): For installing CLI tools
- **ngrok** (optional): For Xendit webhook testing

### Required CLI Tools
```bash
# Install OpenBao CLI
brew install openbao/tap/fga

# Install ngrok (optional, for payment webhooks)
brew install ngrok
```

### Environment Files
Ensure you have these files in your project root:
- `.env` - Main environment configuration
- `.bao.env` - OpenBao AppRole credentials (if exists)
- `init.txt` - OpenBao unseal keys and root token (if exists)

---

## Architecture Overview

### Services Stack
```
┌─────────────────────────────────────────────────────────┐
│                    Docker Compose Stack                  │
├─────────────────────────────────────────────────────────┤
│ • openbao       - Secret Management (port 8200)         │
│ • postgres      - Database (port 5432)                  │
│ • kafka         - Message Queue (port 9092)             │
│ • restate       - Workflow Engine (port 8080)           │
│ • jaeger        - Observability (port 16686)            │
│ • mailhog       - Email Testing (port 8025)             │
│ • openfga       - Authorization (port 8081)             │
│ • app           - Main Application (port 3000)          │
│ • emailworker   - Email Worker                          │
│ • mcp           - Model Context Protocol                │
└─────────────────────────────────────────────────────────┘
```

### Secret Management Flow
```
Application Startup
        ↓
    config.Load()
        ↓
  ┌─────────────────┐
  │ Try OpenBao     │ ←── BAO_ROLE_ID + BAO_SECRET_ID
  └────────┬────────┘
           │
    ┌──────┴───────┐
    │  Success?    │
    └──┬───────┬───┘
  Yes  │       │  No
       │       │
       ↓       ↓
  ✅ Load   ⚠️ Fallback
  Secrets   to .env
  from      variables
  OpenBao
```

---

## Quick Start

### 1. Start All Services
```bash
# Navigate to project directory
cd /path/to/Order-Processing-Pipeline

# create data/raft
mkdir -p data/raft

# Start all services
docker compose up -d
```

### 2. Initialize and Unseal OpenBao

#### First-Time Setup: Initialize OpenBao (Only if `init.txt` doesn't exist)

If you're setting up OpenBao for the first time, you need to initialize it:

```bash
# Check if OpenBao is already initialized
docker exec -e BAO_ADDR=http://127.0.0.1:8200 openbao bao status

# If it shows "Error initializing: Vault is not initialized", then initialize:
docker exec -e BAO_ADDR=http://127.0.0.1:8200 openbao bao operator init \
  -key-shares=3 \
  -key-threshold=2 \
  > init.txt

# IMPORTANT: Save init.txt securely! It contains:
# - 3 unseal keys (you need any 2 to unseal)
# - Root token (for admin operations)
```

After initialization, you'll need to:
1. Create the AppRole for application authentication
2. Store secrets in OpenBao

**See the [Initial OpenBao Setup](#initial-openbao-setup-first-time-only) section below for complete first-time setup instructions.**

#### Regular Operation: Unseal OpenBao (Required after every restart)

OpenBao seals itself on every restart for security. You must unseal it before the app can access secrets.

```bash
# Method 1: Using the unseal script
./scripts/unseal_openbao.sh

# Method 2: Manual unseal with both keys
docker exec -e BAO_ADDR=http://127.0.0.1:8200 openbao bao operator unseal 7HAjrGorV1AQ6C94r1QPYPLZ5Qoh48sZGcgE+Z2e/+x6
docker exec -e BAO_ADDR=http://127.0.0.1:8200 openbao bao operator unseal Di28RncdsWKvVeokCbr1BXndlFnIWMrG/+nO2DNOY9//
```

**Note**: The keys shown above are from the existing `init.txt`. If you initialized OpenBao yourself, use the keys from your own `init.txt` file.

### 3. Restart Application
After unsealing OpenBao, restart the app to load secrets:

```bash
docker compose restart app emailworker
```

### 4. Access the Application
- **Web UI**: http://localhost:3000
- **Jaeger Tracing**: http://localhost:16686
- **MailHog**: http://localhost:8025
- **OpenFGA Playground**: http://localhost:3001/playground

---

## Detailed Setup

### Step 1: Environment Configuration

#### Check `.env` File
Ensure these variables are set in your `.env` file:

```bash
# OpenBao Configuration
BAO_ROLE_ID=beb28548-e26f-a60a-9f53-fe7b0b14b6d2
BAO_SECRET_ID=d7c5808a-6d24-d58f-eb42-23322e2634f6

# OpenFGA Configuration
OPENFGA_STORE_ID=01K9VA0NN6ZMTZ0AW0783K4B72

# Xendit Configuration (non-secret)
XENDIT_SUCCESS_URL=http://localhost:3000
XENDIT_FAILURE_URL=http://localhost:3000

# Database Configuration
ORDER_DB_PASSWORD=postgres

# Note: XENDIT_SECRET_KEY, XENDIT_CALLBACK_TOKEN, OPENROUTER_API_KEY 
# are now stored in OpenBao and loaded at runtime
```

### Step 2: Start Infrastructure Services

```bash
# Start services in order
docker compose up -d postgres kafka openbao openfga restate jaeger mailhog

# Wait for services to be healthy (about 10 seconds)
sleep 10

# Check service health
docker compose ps
```

Expected output:
```
NAME        STATUS                    PORTS
postgres    Up (healthy)             0.0.0.0:5432->5432/tcp
kafka       Up                       0.0.0.0:9092->9092/tcp
openbao     Up (healthy)             0.0.0.0:8200->8200/tcp
openfga     Up                       0.0.0.0:8080-8081->8080-8081/tcp
restate     Up                       0.0.0.0:8080->8080/tcp
jaeger      Up                       0.0.0.0:16686->16686/tcp
mailhog     Up                       0.0.0.0:8025->8025/tcp
```

### Step 3: Initialize OpenBao Secrets

#### Check OpenBao Status
```bash
docker exec -e BAO_ADDR=http://127.0.0.1:8200 openbao bao status
```

If it shows `Sealed: true`, unseal it:
```bash
# Unseal with key 1
docker exec -e BAO_ADDR=http://127.0.0.1:8200 openbao bao operator unseal 7HAjrGorV1AQ6C94r1QPYPLZ5Qoh48sZGcgE+Z2e/+x6

# Unseal with key 2
docker exec -e BAO_ADDR=http://127.0.0.1:8200 openbao bao operator unseal Di28RncdsWKvVeokCbr1BXndlFnIWMrG/+nO2DNOY9//
```

#### Verify Secrets Exist
```bash
# Set token for CLI access
export BAO_TOKEN=s.D3xErUZJKEc8o8qIFBJJnpo0

# Check Xendit secrets
docker exec -e BAO_ADDR=http://127.0.0.1:8200 -e BAO_TOKEN=$BAO_TOKEN \
  openbao bao kv get secret/myapp/xendit

# Check Database password
docker exec -e BAO_ADDR=http://127.0.0.1:8200 -e BAO_TOKEN=$BAO_TOKEN \
  openbao bao kv get secret/myapp/database

# Check OpenRouter API key
docker exec -e BAO_ADDR=http://127.0.0.1:8200 -e BAO_TOKEN=$BAO_TOKEN \
  openbao bao kv get secret/myapp/openrouter
```

### Step 4: Initialize OpenFGA Authorization

#### Upload Authorization Model
```bash
# Install FGA CLI (if not already installed)
brew install openfga/tap/fga

# Upload the authorization model
fga model write \
  --store-id 01K9VA0NN6ZMTZ0AW0783K4B72 \
  --api-url http://localhost:8081 \
  --file policy/authz.fga
```

Expected output:
```json
{
  "authorization_model_id":"01K9Y58VC4SM82FBGZWS6SQPQX"
}
```

#### Seed Authorization Tuples
```bash
# Add user:bob as merchant of org:acme
curl -X POST http://localhost:8081/stores/01K9VA0NN6ZMTZ0AW0783K4B72/write \
  -H "Content-Type: application/json" \
  -d '{
    "writes": {
      "tuple_keys": [
        {"user": "user:bob", "relation": "merchant", "object": "org:acme"},
        {"user": "org:acme", "relation": "org", "object": "store:merchant-001"},
        {"user": "user:alice", "relation": "admin", "object": "org:acme"}
      ]
    }
  }'
```

#### Verify Authorization
```bash
# Test: bob should have merchant access
curl -X POST http://localhost:8081/stores/01K9VA0NN6ZMTZ0AW0783K4B72/check \
  -H "Content-Type: application/json" \
  -d '{
    "tuple_key": {
      "user": "user:bob",
      "relation": "merchant",
      "object": "store:merchant-001"
    }
  }'
```

Expected: `{"allowed":true}`

### Step 5: Run Database Migrations

Migrations run automatically via the `migrator` service, but you can verify:

```bash
# Check migration logs
docker logs migrator

# Expected output should show:
# "No change detected; success." or migration completed successfully
```

### Step 6: Start Application Services

```bash
# Start the application
docker compose up -d app emailworker

# Check logs for secret loading
docker logs app 2>&1 | grep -i "secrets\|openbao"
```

Expected output:
```
✓ Secrets loaded successfully from OpenBao
[Merchant m_001] Loaded 5 items from database
```

### Step 7: Register with Restate

```bash
# Register the Restate services
docker exec restate restate deployments register http://app:9081

# Verify registration
docker exec restate restate deployments list
```

### Step 8: Setup ngrok (Optional - for Xendit webhooks)

If you want to test real payment webhooks:

```bash
# Start ngrok
ngrok http 3000

# Copy the HTTPS URL (e.g., https://abc123.ngrok-free.app)
# Update .env with the ngrok URL:
XENDIT_SUCCESS_URL=https://your-ngrok-url.ngrok-free.app
XENDIT_FAILURE_URL=https://your-ngrok-url.ngrok-free.app

# Restart app to pick up new URLs
docker compose restart app
```

---

## Verification

### 1. Check All Services Running
```bash
docker compose ps
```

All services should show `Up` or `Up (healthy)` status.

### 2. Test Web UI
Open http://localhost:3000 in your browser. You should see:
- Products loaded from database
- AuthZ controls (role selector)
- Basket and checkout functionality

### 3. Test Authorization

#### As Anonymous (No Access)
```bash
curl "http://localhost:3000/authz/check?object=store:merchant-001&relation=merchant&principal=user:anonymous"
```
Expected: `{"allowed":false}`

#### As Bob (Merchant - Has Access)
```bash
curl "http://localhost:3000/authz/check?object=store:merchant-001&relation=merchant&principal=user:bob"
```
Expected: `{"allowed":true}`

#### As Alice (Admin - No Merchant Access)
```bash
curl "http://localhost:3000/authz/check?object=store:merchant-001&relation=merchant&principal=user:alice"
```
Expected: `{"allowed":false}`

### 4. Test Secret Loading

#### Check App Uses OpenBao
```bash
docker logs app 2>&1 | grep "Secrets loaded"
```
Should show: `✓ Secrets loaded successfully from OpenBao`

#### Test Database Connection
```bash
docker logs app 2>&1 | grep "database"
```
Should show: `Successfully connected to PostgreSQL database: orderpipeline`

### 5. Test Order Flow

#### Place an Order via UI
1. Go to http://localhost:3000
2. Select "Anonymous" role
3. Add items to basket
4. Click "Checkout"
5. You'll be redirected to Xendit payment page
6. Complete the payment (use test mode)
7. After payment, you should be redirected back to the shop

#### Check Order in Database
```bash
docker exec -e PGPASSWORD=postgres postgres psql -U orderpipelineadmin -d orderpipeline -c "SELECT id, customer_id, status, total_amount FROM orders ORDER BY updated_at DESC LIMIT 5;"
```

---

## Initial OpenBao Setup (First-Time Only)

**Skip this section if you already have `init.txt` with unseal keys and root token.**

If you're setting up OpenBao from scratch, follow these steps once:

### 1. Initialize OpenBao

```bash
# Start OpenBao container
docker compose up -d openbao

# Wait for it to be ready
sleep 5

# Initialize with Shamir's Secret Sharing (3 keys, need 2 to unseal)
docker exec -e BAO_ADDR=http://127.0.0.1:8200 openbao bao operator init \
  -key-shares=3 \
  -key-threshold=2 \
  > init.txt

# Display the initialization output
cat init.txt
```

You'll see output like:
```
Unseal Key 1: <key1>
Unseal Key 2: <key2>
Unseal Key 3: <key3>

Initial Root Token: s.<token>

Vault initialized with 3 key shares and a key threshold of 2.
```

**⚠️ CRITICAL**: Save `init.txt` securely! Without these keys, you cannot unseal OpenBao.

### 2. Unseal OpenBao

```bash
# Extract keys from init.txt (or copy them manually)
KEY1=$(grep "Unseal Key 1:" init.txt | awk '{print $NF}')
KEY2=$(grep "Unseal Key 2:" init.txt | awk '{print $NF}')
ROOT_TOKEN=$(grep "Initial Root Token:" init.txt | awk '{print $NF}')

# Unseal with first key
docker exec -e BAO_ADDR=http://127.0.0.1:8200 openbao bao operator unseal $KEY1

# Unseal with second key
docker exec -e BAO_ADDR=http://127.0.0.1:8200 openbao bao operator unseal $KEY2

# Verify it's unsealed
docker exec -e BAO_ADDR=http://127.0.0.1:8200 openbao bao status
```

### 3. Enable KV v2 Secrets Engine

```bash
# Login with root token
docker exec -e BAO_ADDR=http://127.0.0.1:8200 -e BAO_TOKEN=$ROOT_TOKEN \
  openbao bao login $ROOT_TOKEN

# Enable KV v2 secrets engine at "secret/"
docker exec -e BAO_ADDR=http://127.0.0.1:8200 -e BAO_TOKEN=$ROOT_TOKEN \
  openbao bao secrets enable -version=2 -path=secret kv
```

### 4. Store Application Secrets

```bash
# Store Xendit secrets
docker exec -e BAO_ADDR=http://127.0.0.1:8200 -e BAO_TOKEN=$ROOT_TOKEN \
  openbao bao kv put secret/myapp/xendit \
  XENDIT_SECRET_KEY="your-xendit-secret-key" \
  XENDIT_CALLBACK_TOKEN="your-xendit-callback-token"

# Store OpenRouter API key
docker exec -e BAO_ADDR=http://127.0.0.1:8200 -e BAO_TOKEN=$ROOT_TOKEN \
  openbao bao kv put secret/myapp/openrouter \
  OPENROUTER_API_KEY="your-openrouter-api-key"

# Store database password
docker exec -e BAO_ADDR=http://127.0.0.1:8200 -e BAO_TOKEN=$ROOT_TOKEN \
  openbao bao kv put secret/myapp/database \
  ORDER_DB_PASSWORD="postgres"
```

### 5. Create AppRole for Application

```bash
# Enable AppRole auth method
docker exec -e BAO_ADDR=http://127.0.0.1:8200 -e BAO_TOKEN=$ROOT_TOKEN \
  openbao bao auth enable approle

# Create a policy for the app
docker exec -e BAO_ADDR=http://127.0.0.1:8200 -e BAO_TOKEN=$ROOT_TOKEN \
  openbao bao policy write myapp-read - <<EOF
path "secret/data/myapp/*" {
  capabilities = ["read"]
}
EOF

# Create the AppRole
docker exec -e BAO_ADDR=http://127.0.0.1:8200 -e BAO_TOKEN=$ROOT_TOKEN \
  openbao bao write auth/approle/role/myapp \
  token_policies="myapp-read" \
  token_ttl=1h \
  token_max_ttl=4h

# Get Role ID
ROLE_ID=$(docker exec -e BAO_ADDR=http://127.0.0.1:8200 -e BAO_TOKEN=$ROOT_TOKEN \
  openbao bao read -field=role_id auth/approle/role/myapp/role-id)

# Generate Secret ID
SECRET_ID=$(docker exec -e BAO_ADDR=http://127.0.0.1:8200 -e BAO_TOKEN=$ROOT_TOKEN \
  openbao bao write -field=secret_id -f auth/approle/role/myapp/secret-id)

# Display credentials
echo "BAO_ROLE_ID=$ROLE_ID"
echo "BAO_SECRET_ID=$SECRET_ID"
```

### 6. Update Environment Files

Add the AppRole credentials to your `.env` file:

```bash
# Add these lines to .env
echo "BAO_ROLE_ID=$ROLE_ID" >> .env
echo "BAO_SECRET_ID=$SECRET_ID" >> .env
```

### 7. Update Unseal Script

Update `scripts/unseal_openbao.sh` with your unseal keys:

```bash
#!/bin/bash
# Replace these with your keys from init.txt
KEY1="your-key-1"
KEY2="your-key-2"

docker exec -e BAO_ADDR=http://127.0.0.1:8200 openbao bao operator unseal $KEY1
docker exec -e BAO_ADDR=http://127.0.0.1:8200 openbao bao operator unseal $KEY2
```

**First-time setup complete!** Now proceed to Step 3 in the Quick Start guide to restart your application.

---

## Troubleshooting

### Issue: OpenBao Sealed

**Symptom**: App logs show "OpenBao unavailable"

**Solution**:
```bash
# Check seal status
docker exec -e BAO_ADDR=http://127.0.0.1:8200 openbao bao status

# If sealed, unseal with both keys
docker exec -e BAO_ADDR=http://127.0.0.1:8200 openbao bao operator unseal 7HAjrGorV1AQ6C94r1QPYPLZ5Qoh48sZGcgE+Z2e/+x6
docker exec -e BAO_ADDR=http://127.0.0.1:8200 openbao bao operator unseal Di28RncdsWKvVeokCbr1BXndlFnIWMrG/+nO2DNOY9//

# Restart app
docker compose restart app
```

### Issue: Database Password Authentication Failed

**Symptom**: App can't connect to database

**Solution**:
```bash
# Check password in OpenBao
docker exec -e BAO_ADDR=http://127.0.0.1:8200 -e BAO_TOKEN=s.D3xErUZJKEc8o8qIFBJJnpo0 \
  openbao bao kv get secret/myapp/database

# Should show: ORDER_DB_PASSWORD: postgres
# If different, update it:
docker exec -e BAO_ADDR=http://127.0.0.1:8200 -e BAO_TOKEN=s.D3xErUZJKEc8o8qIFBJJnpo0 \
  openbao bao kv put secret/myapp/database ORDER_DB_PASSWORD=postgres

# Restart app
docker compose restart app
```

### Issue: Authorization Always Allows

**Symptom**: All users can perform all actions

**Solution**:
```bash
# Check OPENFGA_STORE_ID is set in app
docker exec app sh -c 'echo $OPENFGA_STORE_ID'

# Should output: 01K9VA0NN6ZMTZ0AW0783K4B72
# If empty, check .env file and restart:
docker compose restart app
```

### Issue: Products Empty

**Symptom**: No products show in the UI

**Solution**:
```bash
# Check database has data
docker exec -e PGPASSWORD=postgres postgres psql -U orderpipelineadmin -d orderpipeline -c "SELECT COUNT(*) FROM merchant_items;"

# If 0 rows, run migrations again
docker compose restart migrator
docker compose restart app
```

### Issue: Payment Redirect Shows "Method Not Allowed"

**Symptom**: After payment, redirect fails

**Solution**:
```bash
# Check XENDIT_SUCCESS_URL in .env points to shop, not webhook
grep XENDIT_SUCCESS_URL .env

# Should be: XENDIT_SUCCESS_URL=http://localhost:3000
# NOT: XENDIT_SUCCESS_URL=http://localhost:3000/api/webhooks/xendit

# Update and restart if needed
docker compose restart app
```

### Issue: OpenFGA Errors - "No authorization model found"

**Symptom**: OpenFGA logs show model errors

**Solution**:
```bash
# Re-upload authorization model
fga model write \
  --store-id 01K9VA0NN6ZMTZ0AW0783K4B72 \
  --api-url http://localhost:8081 \
  --file policy/authz.fga

# Reseed tuples
curl -X POST http://localhost:8081/stores/01K9VA0NN6ZMTZ0AW0783K4B72/write \
  -H "Content-Type: application/json" \
  -d '{
    "writes": {
      "tuple_keys": [
        {"user": "user:bob", "relation": "merchant", "object": "org:acme"},
        {"user": "org:acme", "relation": "org", "object": "store:merchant-001"}
      ]
    }
  }'
```

---

## Daily Operations

### Starting the System

```bash
# 1. Start all services
docker compose up -d

# 2. Unseal OpenBao (REQUIRED after every restart)
./scripts/unseal_openbao.sh
# OR manually:
docker exec -e BAO_ADDR=http://127.0.0.1:8200 openbao bao operator unseal 7HAjrGorV1AQ6C94r1QPYPLZ5Qoh48sZGcgE+Z2e/+x6
docker exec -e BAO_ADDR=http://127.0.0.1:8200 openbao bao operator unseal Di28RncdsWKvVeokCbr1BXndlFnIWMrG/+nO2DNOY9//

# 3. Restart app services
docker compose restart app emailworker

# 4. Verify everything is running
docker compose ps
docker logs app 2>&1 | tail -20
```

### Stopping the System

```bash
# Graceful shutdown
docker compose down

# Remove volumes (WARNING: deletes all data)
docker compose down -v
```

### Viewing Logs

```bash
# All services
docker compose logs -f

# Specific service
docker logs -f app
docker logs -f openbao
docker logs -f openfga

# Search logs
docker logs app 2>&1 | grep -i "error\|secret"
```

### Accessing Services

```bash
# OpenBao CLI
docker exec -e BAO_ADDR=http://127.0.0.1:8200 -e BAO_TOKEN=s.D3xErUZJKEc8o8qIFBJJnpo0 openbao bao <command>

# PostgreSQL
docker exec -it -e PGPASSWORD=postgres postgres psql -U orderpipelineadmin -d orderpipeline

# App shell
docker exec -it app sh
```

---

## Security Notes

### OpenBao Unsealing
- OpenBao seals on every restart by design for security
- **Always unseal after container restarts**
- Requires 2 out of 3 unseal keys (Shamir threshold)
- Keys are in `init.txt` (keep this file secure!)

### AppRole Credentials
- `BAO_ROLE_ID`: Not secret, like a username
- `BAO_SECRET_ID`: Secret, like a password (rotatable)
- Stored in `.bao.env` and `.env`
- Used for machine-to-machine authentication

### Authorization
- Anonymous users: Can browse and shop
- Merchants (user:bob): Can add inventory
- Admins (user:alice): Can view all, no merchant access

---

## Architecture Details

### How Secrets Are Loaded

```
1. App starts → config.Load()
2. Tries OpenBao authentication
   ├─ Gets VAULT_ADDR, BAO_ROLE_ID, BAO_SECRET_ID from env
   ├─ Authenticates via AppRole
   └─ Receives authentication token
3. Fetches secrets from OpenBao
   ├─ secret/data/myapp/xendit → XENDIT_SECRET_KEY, XENDIT_CALLBACK_TOKEN
   ├─ secret/data/myapp/openrouter → OPENROUTER_API_KEY
   └─ secret/data/myapp/database → ORDER_DB_PASSWORD
4. Stores in config struct + exports to os.Setenv()
5. If OpenBao fails → Falls back to .env variables
```

### Authorization Flow

```
1. User makes request (with cookie/header)
2. Middleware extracts principal (user:bob, user:alice, user:anonymous)
3. Calls OpenFGA Check API
   ├─ User: principal
   ├─ Object: store:merchant-001
   └─ Relation: merchant
4. OpenFGA evaluates authorization model
5. Returns allowed: true/false
6. Middleware allows/denies request
```

---

## Common Commands Reference

```bash
# Service Management
docker compose up -d                          # Start all services
docker compose down                           # Stop all services
docker compose restart app                    # Restart specific service
docker compose ps                             # List running services
docker compose logs -f app                    # Follow logs

# OpenBao Operations
docker exec -e BAO_ADDR=http://127.0.0.1:8200 openbao bao status
docker exec -e BAO_ADDR=http://127.0.0.1:8200 openbao bao operator unseal <key>
docker exec -e BAO_ADDR=http://127.0.0.1:8200 -e BAO_TOKEN=<token> openbao bao kv get <path>

# OpenFGA Operations
fga model write --store-id <id> --api-url http://localhost:8081 --file policy/authz.fga
curl -X POST http://localhost:8081/stores/<id>/write -H "Content-Type: application/json" -d '{...}'
curl -X POST http://localhost:8081/stores/<id>/check -H "Content-Type: application/json" -d '{...}'

# Database Access
docker exec -e PGPASSWORD=postgres postgres psql -U orderpipelineadmin -d orderpipeline
docker exec -e PGPASSWORD=postgres postgres psql -U orderpipelineadmin -d orderpipeline -c "<query>"

# Restate Operations
docker exec restate restate deployments register http://app:9081
docker exec restate restate deployments list
```

---

## Support & Documentation

- **OpenBao Docs**: https://openbao.org/docs/
- **OpenFGA Docs**: https://openfga.dev/docs
- **Restate Docs**: https://docs.restate.dev/
- **Xendit Docs**: https://developers.xendit.co/

For issues, check the [Troubleshooting](#troubleshooting) section or review service logs.

---

**Last Updated**: November 13, 2025  
**Version**: 1.0.0
