# Order Processing Pipeline - Deployment Guide

Complete guide for running the Order Processing Pipeline with OpenBao secret management and OpenFGA authorization.

## Table of Contents
- [Prerequisites](#prerequisites)
- [Quick Start](#quick-start)
- [First-Time Setup](#first-time-setup)
- [Daily Operations](#daily-operations)
- [Verification](#verification)
- [Troubleshooting](#troubleshooting)
- [Reference](#reference)

---

## Prerequisites

### Required Software
- **Docker Desktop**: Latest version
- **Docker Compose**: v2.0+
- **Homebrew** (macOS): For CLI tools

### Install CLI Tools
```bash
# OpenFGA CLI (for authorization model management)
brew install openfga/tap/fga

# ngrok (optional, for Xendit webhook testing)
brew install ngrok
```

---

## Quick Start

**For existing setups with `init.txt` and `.env` configured:**

```bash
# 1. Start all services
docker compose --profile openfga up -d

# 2. Unseal OpenBao (required after every restart)
./scripts/unseal_openbao.sh

# 3. Access the application
open http://localhost:3000
```

**That's it!** Skip to [Verification](#verification) to test the system.

---

## First-Time Setup

**Complete these steps only once when setting up the system from scratch.**

### Step 1: Create Data Directory

```bash
mkdir -p data/raft
```

### Step 2: Create Environment File

```bash
# Copy example file
cp .env.example .env

# Edit .env and add these placeholders (we'll fill them later)
cat >> .env << 'EOF'
# OpenBao AppRole (will be generated in next steps)
BAO_ROLE_ID=
BAO_SECRET_ID=

# OpenFGA (use the default store ID)
OPENFGA_STORE_ID=01K9VA0NN6ZMTZ0AW0783K4B72

# Xendit (non-secret URLs)
XENDIT_SUCCESS_URL=http://localhost:3000
XENDIT_FAILURE_URL=http://localhost:3000

# Database
ORDER_DB_PASSWORD=postgres
EOF
```

### Step 3: Start PostgreSQL (Required for Next Steps)

```bash
# Start just PostgreSQL first
docker compose up -d postgres

# Wait for it to be ready
sleep 5
```

### Step 4: Initialize OpenBao

```bash
# Start OpenBao
docker compose up -d openbao

# Wait for it to be ready
sleep 10

# Initialize OpenBao (creates 3 unseal keys, needs 2 to unseal, and root token)
./scripts/init_openbao_docker.sh
```

**Output:**
```
✓ OpenBao initialized successfully
========================================
IMPORTANT: Save these credentials securely!
========================================
Unseal Key 1: <key1>
Unseal Key 2: <key2>
Unseal Key 3: <key3>
Root Token:   <token>
========================================

✓ Credentials saved to: ./init.txt
⚠ Make sure this file is in .gitignore!

✓ OpenBao unsealed (2/2)
✓ AppRole enabled
✓ Policy created
✓ AppRole created
✓ AppRole credentials generated
✓ KV v2 secrets engine ready
✓ Default secrets stored successfully

========================================
Setup Complete!
========================================

✓ Secrets stored in OpenBao:
  - secret/myapp/database
  - secret/myapp/xendit (placeholder)
  - secret/myapp/openrouter (placeholder)

✓ AppRole created

IMPORTANT: Save these credentials to .env:
BAO_ROLE_ID=<role-id>
BAO_SECRET_ID=<secret-id>

Unseal keys saved to: init.txt
⚠️  Keep init.txt secure! You need it to unseal OpenBao.
```

### Step 5: Update .env with AppRole Credentials

```bash
# Copy the BAO_ROLE_ID and BAO_SECRET_ID from the output above
# Edit .env file manually or use sed:

nano .env

# Replace the empty values:
# BAO_ROLE_ID=<paste-role-id-here>
# BAO_SECRET_ID=<paste-secret-id-here>
```

### Step 6: Update API Keys in OpenBao (Optional)

If you have real API keys, update them in OpenBao:

```bash
# Get root token from init.txt
export BAO_TOKEN=$(grep "Initial Root Token:" init.txt | awk '{print $NF}')

# Update Xendit secrets (replace with your actual keys)
docker exec -e BAO_ADDR=http://127.0.0.1:8200 -e BAO_TOKEN=$BAO_TOKEN \
  openbao bao kv put secret/myapp/xendit \
  XENDIT_SECRET_KEY="your-actual-xendit-secret-key" \
  XENDIT_CALLBACK_TOKEN="your-actual-callback-token"

# Update OpenRouter API key (replace with your actual key)
docker exec -e BAO_ADDR=http://127.0.0.1:8200 -e BAO_TOKEN=$BAO_TOKEN \
  openbao bao kv put secret/myapp/openrouter \
  OPENROUTER_API_KEY="your-actual-openrouter-api-key"
```

### Step 7: Setup OpenFGA Authorization

OpenFGA requires:
1. **PostgreSQL database** (`openfga`) - Created automatically by `openfga-migrator`
2. **Authorization model** - Uploaded by `seed_openfga.sh`
3. **Authorization tuples** (permissions) - Seeded by `seed_openfga.sh`

```bash
# - Creates the 'openfga' database in PostgreSQL
./script/setup_openfga_postgres.sh
# This script does everything:
# - Starts openfga-migrator (runs database migrations)
# - Starts openfga service
# - Uploads authorization model from policy/authz.fga
# - Seeds user permissions (Bob, Alice, Anonymous)
./scripts/seed_openfga.sh
```

**Output:**
```
======================================
OpenFGA Setup with PostgreSQL
======================================

ℹ Checking OpenFGA database...
✓ PostgreSQL is running
✓ Database 'openfga' already exists

ℹ Starting OpenFGA services...
✓ Migrations completed successfully
✓ OpenFGA is ready

ℹ Verifying database tables...
 Schema |        Name         | Type  |       Owner       
--------+---------------------+-------+-------------------
 public | authorization_model | table | orderpipelineadmin
 public | store               | table | orderpipelineadmin
 public | tuple               | table | orderpipelineadmin
...

ℹ Using store ID: 01K9VA0NN6ZMTZ0AW0783K4B72
ℹ Uploading authorization model...
✓ Authorization model uploaded
ℹ Seeding authorization tuples...
✓ Authorization tuples seeded
ℹ Testing authorization...
✓ Authorization test passed

======================================
OpenFGA Setup Complete!
======================================

✓ PostgreSQL database configured
✓ OpenFGA tables migrated
✓ Authorization model uploaded
✓ User permissions seeded
✓ Data persistence verified

OpenFGA API: http://localhost:8081
OpenFGA Playground: http://localhost:8081/playground
```

### Step 8: Start All Application Services

```bash
# Start all remaining services
# This will:
# - Start Kafka, Restate, Jaeger, MailHog
# - Run 'migrator' (seeds merchant_items table)
# - Start 'app' and 'emailworker'
# - Auto-register app with Restate (via restate-registrar)
docker compose --profile openfga up -d
```

**What happens automatically:**
1. **Infrastructure services** start (kafka, restate, jaeger, mailhog)
2. **Database migrations** run (`migrator` container seeds products)
3. **Application services** start (app, emailworker)
4. **Restate registration** happens automatically (restate-registrar)

Wait for all services to be ready (~15-30 seconds):

```bash
# Check all services are running
docker compose ps

# You should see all services "Up" or "Up (healthy)"
```

**First-time setup complete!** Proceed to [Verification](#verification).

---

## Daily Operations

### Starting the System

```bash
# 1. Start all services (including OpenFGA profile)
docker compose --profile openfga up -d

# 2. Unseal OpenBao (NO LOGIN REQUIRED - app uses AppRole)
./scripts/unseal_openbao.sh

# 3. Restart app to reconnect to unsealed OpenBao
docker compose restart app emailworker
```


### Checking Service Status

```bash
# View all services
docker compose ps

# Check OpenBao status
docker exec -e BAO_ADDR=http://127.0.0.1:8200 openbao bao status

# Check OpenFGA status
./scripts/check_openfga_status.sh

# View logs
docker compose logs -f app
docker logs openbao
docker logs openfga
```

### Common Tasks

```bash
# Restart application after code changes
docker compose restart app emailworker

# Rebuild application image
docker compose build app
docker compose up -d app

# Access application database
docker exec -it -e PGPASSWORD=postgres postgres \
  psql -U orderpipelineadmin -d orderpipeline

# Access OpenFGA database
docker exec -it -e PGPASSWORD=postgres postgres \
  psql -U orderpipelineadmin -d openfga

# View OpenBao secrets (requires root token from init.txt)
export BAO_TOKEN=$(grep "Initial Root Token:" init.txt | awk '{print $NF}')
docker exec -e BAO_ADDR=http://127.0.0.1:8200 -e BAO_TOKEN=$BAO_TOKEN \
  openbao bao kv get secret/myapp/xendit
```

---

## Verification

### 1. Check All Services Running

```bash
docker compose ps
```

Expected: All services show `Up` or `Up (healthy)`.

### 2. Check OpenBao Unsealed

```bash
docker exec -e BAO_ADDR=http://127.0.0.1:8200 openbao bao status
```

Expected: `Sealed: false`

### 3. Test Web UI

```bash
open http://localhost:3000
```

You should see:
- ✅ Products loaded from database
- ✅ Role selector (Anonymous/Bob/Alice)
- ✅ Shopping cart functionality

### 4. Test Authorization

```bash
# Bob (merchant) should have access
curl "http://localhost:3000/authz/check?object=store:merchant-001&relation=merchant&principal=user:bob"
# Expected: {"allowed":true}

# Anonymous should NOT have access
curl "http://localhost:3000/authz/check?object=store:merchant-001&relation=merchant&principal=user:anonymous"
# Expected: {"allowed":false}
```

### 5. Test Secret Loading from OpenBao

```bash
docker logs app 2>&1 | grep "Secrets loaded"
# Expected: "✓ Secrets loaded successfully from OpenBao"
```

### 6. Test OpenFGA Persistence

```bash
# Check tuples are stored in PostgreSQL
docker exec -e PGPASSWORD=postgres postgres \
  psql -U orderpipelineadmin -d openfga \
  -c "SELECT COUNT(*) FROM tuple;"
# Expected: 3 (or more)

# Restart OpenFGA and verify data persists
docker compose restart openfga
sleep 5

curl -X POST http://localhost:8081/stores/01K9VA0NN6ZMTZ0AW0783K4B72/check \
  -H "Content-Type: application/json" \
  -d '{"tuple_key":{"user":"user:bob","relation":"merchant","object":"store:merchant-001"}}'
# Expected: {"allowed":true}
```

### 7. Access UIs

- **Application**: http://localhost:3000
- **Jaeger Tracing**: http://localhost:16686
- **MailHog (Email)**: http://localhost:8025
- **OpenFGA Playground**: http://localhost:8081/playground

---

## Troubleshooting

### OpenBao Issues

#### Error: "OpenBao unavailable" or "connection refused"

```bash
# Check if OpenBao container is running
docker compose ps openbao

# Check OpenBao status
docker exec -e BAO_ADDR=http://127.0.0.1:8200 openbao bao status

# If sealed, unseal it
./scripts/unseal_openbao.sh

# Restart app to reconnect
docker compose restart app emailworker
```

#### Error: "permission denied" accessing secrets

```bash
# Verify AppRole credentials in .env
grep BAO_ .env

# Should show:
# BAO_ROLE_ID=<uuid>
# BAO_SECRET_ID=<uuid>

# If missing or wrong, re-run initialization
./scripts/init_openbao_docker.sh

# Update .env with new credentials and restart
docker compose restart app emailworker
```

#### App falls back to .env instead of using OpenBao

```bash
# This means OpenBao is sealed or unreachable
# Check OpenBao status
docker exec -e BAO_ADDR=http://127.0.0.1:8200 openbao bao status

# If sealed: true, unseal it
./scripts/unseal_openbao.sh

# Restart app
docker compose restart app emailworker

# Check logs for "✓ Secrets loaded successfully from OpenBao"
docker logs app 2>&1 | grep "Secrets"
```

### OpenFGA Issues

#### Error: "No authorization model found"

```bash
# Re-upload authorization model and seed tuples
./scripts/seed_openfga.sh
```

#### OpenFGA data lost after restart

```bash
# Check if using PostgreSQL (not in-memory)
docker exec openfga env | grep DATASTORE_ENGINE
# Expected: OPENFGA_DATASTORE_ENGINE=postgres

# Check if tuples exist in database
docker exec -e PGPASSWORD=postgres postgres \
  psql -U orderpipelineadmin -d openfga \
  -c "SELECT COUNT(*) FROM tuple;"

# If 0, re-seed
./scripts/seed_openfga.sh
```

#### No tuples found (not seeded)

```bash
# Re-run the setup script
./scripts/seed_openfga.sh
```

### Database Issues

#### Error: "database password authentication failed"

```bash
# Check password in OpenBao
export BAO_TOKEN=$(grep "Initial Root Token:" init.txt | awk '{print $NF}')
docker exec -e BAO_ADDR=http://127.0.0.1:8200 -e BAO_TOKEN=$BAO_TOKEN \
  openbao bao kv get secret/myapp/database

# Should show: ORDER_DB_PASSWORD=postgres

# If wrong, update it:
docker exec -e BAO_ADDR=http://127.0.0.1:8200 -e BAO_TOKEN=$BAO_TOKEN \
  openbao bao kv put secret/myapp/database \
  ORDER_DB_PASSWORD=postgres \
  ORDER_DB_URL=postgres://orderpipelineadmin:postgres@postgres:5432/orderpipeline?sslmode=disable

# Restart app to reload secrets
docker compose restart app
```

#### No products showing in UI

```bash
# Check database has data
docker exec -e PGPASSWORD=postgres postgres \
  psql -U orderpipelineadmin -d orderpipeline \
  -c "SELECT COUNT(*) FROM merchant_items;"

# If 0, re-run migrations
docker compose restart migrator
sleep 5
docker compose restart app
```

### Application Issues

#### Port already in use

```bash
# Find and kill process using port 3000
lsof -ti:3000 | xargs kill -9

# Restart services
docker compose --profile openfga up -d
```

#### Container keeps restarting

```bash
# Check logs for errors
docker logs app --tail 50

# Common issues:
# - OpenBao sealed → Run ./scripts/unseal_openbao.sh
# - Database not ready → Wait 10 seconds: sleep 10 && docker compose restart app
# - Missing secrets → Check .env has BAO_ROLE_ID and BAO_SECRET_ID
```

---

## Reference

### Service Startup Order

Docker Compose handles dependencies automatically via `depends_on`:

1. **PostgreSQL** - Database for both app and OpenFGA
2. **OpenBao** - Secret management (must be unsealed manually after init)
3. **Infrastructure** - Kafka, Restate, Jaeger, MailHog
4. **OpenFGA Setup**:
   - `setup_openfga_postgres.sh` - Creates `openfga` database
   - `openfga-migrator` - Runs database migrations
   - `openfga` - Authorization service starts
   - `seed_openfga.sh` - Uploads model + seeds permissions
5. **Application Migrations** - `migrator` seeds merchant_items
6. **Application** - `app` and `emailworker` start
7. **Auto-registration** - `restate-registrar` registers app with Restate

### Architecture

```
┌─────────────────────────────────────────────────────────┐
│                    Docker Compose Stack                  │
├─────────────────────────────────────────────────────────┤
│ postgres   - Database (orderpipeline + openfga)         │
│ openbao    - Secret Management (port 8200)              │
│ kafka      - Message Queue (port 9092)                  │
│ restate    - Workflow Engine (port 8080, 9070)          │
│ jaeger     - Observability (port 16686)                 │
│ mailhog    - Email Testing (port 1025, 8025)            │
│ openfga    - Authorization (port 8081)                  │
│ app        - Main Application (port 3000, 9081)         │
│ emailworker- Email Worker                               │
└─────────────────────────────────────────────────────────┘
```

### Secret Management Flow

```
Application Startup
        ↓
    config.Load()
        ↓
  Check if OpenBao is available
        ↓
  Authenticate with AppRole
  (BAO_ROLE_ID + BAO_SECRET_ID from .env)
        ↓
    Fetch Secrets from OpenBao:
    • XENDIT_SECRET_KEY
    • XENDIT_CALLBACK_TOKEN
    • OPENROUTER_API_KEY
    • ORDER_DB_PASSWORD
    • ORDER_DB_URL
        ↓
   ✅ Secrets Loaded from OpenBao
   (if OpenBao unavailable, fallback to .env)
```

### Authorization Flow

```
HTTP Request
     ↓
Extract Principal from Cookie/Header
(user:bob, user:alice, user:anonymous)
     ↓
Call OpenFGA Check API
     ↓
OpenFGA evaluates against authorization model:
• User
• Relation (merchant, admin)
• Object (store:merchant-001)
     ↓
Returns: {"allowed": true/false}
     ↓
Allow/Deny Request
```

### Environment Variables

**Required in `.env`:**
```bash
# OpenBao AppRole (from init_openbao_docker.sh output)
BAO_ROLE_ID=<uuid-from-init-script>
BAO_SECRET_ID=<uuid-from-init-script>

# OpenFGA
OPENFGA_STORE_ID= <openfga_store_id>
# Database
ORDER_DB_PASSWORD=postgres

# Xendit (non-secret URLs)
XENDIT_SUCCESS_URL=http://localhost:3000
XENDIT_FAILURE_URL=http://localhost:3000
```

**Stored in OpenBao by init script:**
```bash
# secret/myapp/database
ORDER_DB_PASSWORD=postgres
ORDER_DB_URL=postgres://orderpipelineadmin:postgres@postgres:5432/orderpipeline?sslmode=disable

# secret/myapp/xendit (update with real values)
XENDIT_SECRET_KEY=<your-key>
XENDIT_CALLBACK_TOKEN=<your-token>

# secret/myapp/openrouter (update with real value)
OPENROUTER_API_KEY=<your-key>
```

### Useful Commands

```bash
# Service Management
docker compose --profile openfga up -d        # Start all services
docker compose --profile openfga down         # Stop all services
docker compose restart <service>              # Restart specific service
docker compose logs -f <service>              # Follow logs

# OpenBao
./scripts/unseal_openbao.sh                   # Unseal OpenBao (NO LOGIN NEEDED)
docker exec -e BAO_ADDR=http://127.0.0.1:8200 openbao bao status

# OpenFGA
./scripts/seed_openfga.sh                     # Re-setup OpenFGA

# Database Queries
# Application database
docker exec -it -e PGPASSWORD=postgres postgres \
  psql -U orderpipelineadmin -d orderpipeline

# OpenFGA database
docker exec -it -e PGPASSWORD=postgres postgres \
  psql -U orderpipelineadmin -d openfga

# Check tuples in OpenFGA
docker exec -e PGPASSWORD=postgres postgres \
  psql -U orderpipelineadmin -d openfga \
  -c "SELECT user_key, relation, object_type, object_id FROM tuple;"
```

### Authorization Roles

| Role      | User ID        | Permissions                              |
|-----------|----------------|------------------------------------------|
| Anonymous | user:anonymous | Browse shop, place orders               |
| Merchant  | user:bob       | Add inventory to store:merchant-001      |
| Admin     | user:alice     | View all resources, no merchant access   |

---

## Security Notes

### OpenBao Security
- **Seals on restart**: Must unseal after every container restart using `./scripts/unseal_openbao.sh`
- **No manual login required**: Application authenticates automatically via AppRole (BAO_ROLE_ID + BAO_SECRET_ID)
- **Unseal keys**: Stored in `init.txt` (keep secure!)
- **Threshold**: Requires 2 out of 3 keys to unseal
- **AppRole**: Machine-to-machine authentication

### OpenFGA Persistence
- **PostgreSQL**: Authorization data stored in `openfga` database
- **Survives restarts**: Tuples and models persist across container restarts
- **Separate database**: Isolated from application data (`orderpipeline`)

### Best Practices
- ✅ Never commit `init.txt` to version control (.gitignore it)
- ✅ Never commit `.env` with real secrets to version control
- ✅ Use OpenBao for all production secrets
- ✅ Rotate `BAO_SECRET_ID` regularly in production
- ✅ Use HTTPS/TLS in production
- ✅ Keep `init.txt` in secure backup (needed to unseal)

---

## Support & Documentation

- **OpenBao**: https://openbao.org/docs/
- **OpenFGA**: https://openfga.dev/docs
- **Restate**: https://docs.restate.dev/
- **Xendit**: https://developers.xendit.co/

---

**Version**: 2.2.0  
**Last Updated**: November 17, 2025