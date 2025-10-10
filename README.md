# Order Processing Pipeline

A Restate-based order processing system with Xendit payment integration and automated webhook handling.

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

## 📚 API Endpoints

- `POST /api/checkout` - Create new order
- `GET /api/orders` - List all orders
- `GET /api/orders/{id}` - Get specific order
- `POST /api/webhooks/xendit` - Xendit webhook handler
- `GET /api/merchants/{id}/items` - Get merchant items

## 🎯 Features

- ✅ Order creation and processing
- ✅ Xendit payment integration
- ✅ Automated webhook handling
- ✅ Real-time order tracking
- ✅ Modern web UI
- ✅ Restate workflow orchestration

---

**Happy coding! 🎉**