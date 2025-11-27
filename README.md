# Order Processing Pipeline

A Restate-based order processing system with Xendit payment integration, AP2 protocol support, and automated webhook handling.

## 🚀 Running the Application

### Prerequisites

- **Go 1.21+** - [Download here](https://golang.org/dl/)
- **PostgreSQL 12+** - [Download here](https://www.postgresql.org/download/)
- **Restate Runtime** - [Installation guide](https://restate.dev/docs/get_started/install)
- **ngrok** - [Download here](https://ngrok.com/download)
- **Xendit Account** - [Sign up here](https://dashboard.xendit.co/) (free)

### Quick Setup

1. **Clone and install dependencies:**
   ```bash
   git clone <your-repo-url>
   cd order-processing-pipeline
   go mod download
   ```

2. **Set up PostgreSQL database:**
   ```bash
   # Create database and user
   sudo -u postgres psql
   CREATE DATABASE orderpipeline;
   CREATE USER orderpipelineadmin WITH PASSWORD 'your_password';
   GRANT ALL PRIVILEGES ON DATABASE orderpipeline TO orderpipelineadmin;
   \q
   
   # Run migrations
   psql -d orderpipeline -U orderpipelineadmin -f db/migrations/000001_init_core.up.sql
   psql -d orderpipeline -U orderpipelineadmin -f db/migrations/000002_merchants_items.up.sql
   psql -d orderpipeline -U orderpipelineadmin -f db/migrations/000003_merchants_items.up.sql
   psql -d orderpipeline -U orderpipelineadmin -f db/migrations/000004_payments_invoice.up.sql
   psql -d orderpipeline -U orderpipelineadmin -f db/migrations/000005_add_awakeable_id.up.sql
   ```

3. **Configure environment:**
   ```bash
   cp .env.example .env
   # Edit .env with your actual values
   ```

4. **Start Restate Runtime:**
   ```bash
   restate dev
   # In another terminal:
   restate deployments register http://localhost:9081
   ```

5. **Start ngrok:**
   ```bash
   chmod +x scripts/setup_ngrok.sh
   ./scripts/setup_ngrok.sh
   ```

6. **Configure Xendit webhook:**
   - Go to [Xendit Dashboard](https://dashboard.xendit.co/) → Settings → Webhooks
   - Set webhook URL to: `https://your-ngrok-url.ngrok-free.dev/api/webhooks/xendit`
   - Copy verification token to `.env` file

7. **Run the application:**
   ```bash
   go run ./cmd/server
   ```

8. **Open the UI:**
   - Visit: http://localhost:3000

## 🔧 Environment Variables

Create a `.env` file with these variables:

```bash
# Database Configuration
ORDER_DB_HOST=localhost
ORDER_DB_PORT=5432
ORDER_DB_NAME=orderpipeline
ORDER_DB_USER=orderpipelineadmin
ORDER_DB_PASSWORD=your_password

# Xendit Configuration
XENDIT_SECRET_KEY=your_xendit_secret_key
XENDIT_CALLBACK_TOKEN=your_webhook_verification_token
XENDIT_SUCCESS_URL=https://your-ngrok-url.ngrok-free.dev
XENDIT_FAILURE_URL=https://your-ngrok-url.ngrok-free.dev

# Restate Runtime
RESTATE_RUNTIME_URL=http://127.0.0.1:8080
```

## 🚨 Troubleshooting

### Database Connection Failed
```bash
# Check PostgreSQL is running
pg_isready
# Test connection
psql -d orderpipeline -U orderpipelineadmin
```

### Restate Connection Failed
```bash
# Check Restate is running
curl http://localhost:8080/health
# Check registered services
restate deployments list
```

### Webhook Not Working
```bash
# Check ngrok is running
curl http://localhost:4040/api/tunnels
# Verify webhook URL in Xendit dashboard
```

## 🤖 Continuous Deployment

- GitHub Actions workflow `.github/workflows/cd.yml` runs on pushes to `master`, releases, or manual dispatch. It executes `go test ./...`, builds the Docker image defined in `Dockerfile`, and publishes it to GitHub Container Registry (`ghcr.io`) under `ghcr.io/<owner>/<repo>`.
- To additionally publish to Google Container Registry when your billing/API issue is resolved, add a repository variable `GCP_PROJECT_ID` and a repository secret `GCP_SA_KEY`. The secret must contain a JSON service account key with `roles/storage.admin` (or a narrower role that grants `storage.objects.create` and `storage.objects.delete`) on the target project. The workflow will automatically push images to `gcr.io/<GCP_PROJECT_ID>/order-processing-pipeline`.
- If you prefer a different image name or registry path, update the `IMAGE_NAME` environment value in `cd.yml`.
- Set any runtime environment variables (database URLs, Kafka brokers, etc.) on the target platform before deploying the container produced by the workflow.

## 🔌 AP2 Integration

The system now includes AP2 (Payment Protocol) integration for seamless checkout experiences:

### MCP Tools
- `checkout_cart(customer_id)` - Create Xendit invoice for cart checkout
- `view_cart(customer_id)` - View cart contents
- `add_to_cart(customer_id, merchant_id, items)` - Add items to cart
- `update_cart_item(customer_id, product_id, quantity)` - Update item quantity
- `remove_from_cart(customer_id, product_ids)` - Remove items from cart

### AP2 Endpoints
- `POST /ap2/intents` - Create payment intent
- `POST /ap2/authorize` - Authorize payment
- `POST /ap2/execute` - Execute payment and get invoice link
- `GET /ap2/status/{payment_id}` - Get payment status
- `POST /ap2/refunds` - Process refunds

### Setup
```bash
# Set AP2 adapter URL
export AP2_BASE=http://127.0.0.1:7010

# Run AP2 adapter (separate service)
# Run MCP server
cd mcp-server
python main.py
```

See [AP2 Integration Guide](docs/AP2_INTEGRATION.md) for detailed documentation.

## 📚 API Endpoints

- `POST /api/checkout` - Create new order
- `GET /api/orders` - List all orders
- `GET /api/orders/{id}` - Get specific order
- `POST /api/webhooks/xendit` - Xendit webhook handler
- `GET /api/merchants/{id}/items` - Get merchant items

## 🎯 Features

- ✅ Order creation and processing
- ✅ Xendit payment integration
- ✅ AP2 payment protocol support
- ✅ MCP server with cart management tools
- ✅ **RAG-powered semantic search** (Langchain + Pinecone)
- ✅ Automated webhook handling
- ✅ Real-time order tracking
- ✅ Modern web UI
- ✅ Restate workflow orchestration
- ✅ OpenBao secret management

## 🔍 RAG (Semantic Search)

The system includes a powerful RAG (Retrieval-Augmented Generation) module for semantic search of merchant items:

### Features
- **Semantic Search** - Find products using natural language queries
- **Vector Embeddings** - Uses HuggingFace's nomic-embed-text-v1.5 model
- **Pinecone Vector DB** - Scalable vector storage and search
- **OpenBao Integration** - Secure secret management
- **MCP Tools** - Search directly from MCP clients

### Quick Start

```bash
# Install dependencies
pip install -e .

# Configure environment
export VAULT_ADDR=http://localhost:8200
export BAO_ROLE_ID=<your-role-id>
export BAO_SECRET_ID=<your-secret-id>

# Store secrets in OpenBao
vault kv put secret/myapp/pinecone \
  PINECONE_API_KEY="your-key" \
  PINECONE_ENVIRONMENT="us-east-1"

vault kv put secret/myapp/database \
  ORDER_DB_PASSWORD="your-password"

# Run setup and tests
cd rag/
python setup_and_test.py

# Ingest merchant items
python ingest_data.py ingest

# Search items
python ingest_data.py search "italian pasta"
```

### MCP Tools

```python
# Search merchant items
search_merchant_items(
    query="wireless headphones",
    merchant_id="m_001",  # optional
    k=5
)

# Update search index
ingest_merchant_data(clear_existing=False)
```

### Example Searches

```bash
# By product type
search_merchant_items(query="headphones", k=5)

# By cuisine
search_merchant_items(query="italian food", k=5)

# Filter by merchant
search_merchant_items(query="chocolate", merchant_id="m_001", k=3)

# By features
search_merchant_items(query="wireless audio device", k=5)
```

See [RAG Documentation](rag/README.md) and [Quick Start Guide](rag/QUICKSTART.md) for detailed setup.

---

**Happy coding! 🎉**
