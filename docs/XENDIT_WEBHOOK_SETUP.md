# Xendit Webhook Integration Setup Guide

## Overview

This guide explains how to configure Xendit webhooks to automatically process payment confirmations instead of using the manual simulation button.

## Implementation Details

### 1. Webhook Endpoint

The webhook handler is implemented at `/api/webhooks/xendit` in `cmd/server/main.go`:

- **Method**: POST
- **Authentication**: Uses `x-callback-token` header verification
- **Payload**: JSON with Xendit invoice data
- **Response**: 200 OK with `{"status": "received"}`

### 2. Environment Variables

Add these environment variables to your `.env` file:

```bash
# Xendit Configuration
XENDIT_SECRET_KEY=your_xendit_secret_key
XENDIT_CALLBACK_TOKEN=your_webhook_verification_token
XENDIT_SUCCESS_URL=https://your-ngrok-url.ngrok-free.dev
XENDIT_FAILURE_URL=https://your-ngrok-url.ngrok-free.dev

# Restate Runtime (if different from default)
RESTATE_RUNTIME_URL=http://127.0.0.1:8080
```

### 3. Ngrok Setup

Use the provided script to start ngrok:

```bash
# Make the script executable (already done)
chmod +x scripts/setup_ngrok.sh

# Start ngrok tunnel
./scripts/setup_ngrok.sh

# Or with custom subdomain
./scripts/setup_ngrok.sh missy-internarial-rubye
```

This will expose your local server at `https://missy-internarial-rubye.ngrok-free.dev` (or similar).

## Xendit Dashboard Configuration

### Step 1: Access Webhook Settings

1. Log in to your [Xendit Dashboard](https://dashboard.xendit.co/)
2. Navigate to **Settings** → **Webhooks**
3. Select the **Invoice** product

### Step 2: Configure Webhook URL

1. In the **Webhook URL** field, enter your ngrok URL:
   ```
   https://missy-internarial-rubye.ngrok-free.dev/api/webhooks/xendit
   ```

2. **Important**: Make sure to include the full path `/api/webhooks/xendit`

### Step 3: Get Webhook Verification Token

1. Click on **View Webhook Verification Token**
2. Enter your password when prompted
3. Copy the verification token
4. Add it to your `.env` file as `XENDIT_CALLBACK_TOKEN`

### Step 4: Enable Invoice Events

Make sure these events are enabled:
- ✅ **Invoice Paid** - Triggers when payment is completed
- ✅ **Invoice Expired** - Triggers when payment expires
- ✅ **Invoice Failed** - Triggers when payment fails

### Step 5: Test the Webhook

1. Click **Save and Test** in the Xendit Dashboard
2. Check your server logs for the test webhook
3. Verify the response is successful

### Step 6: Enable Auto-Retry (Recommended)

- Enable **Auto-Retry for Failed Webhooks** to ensure reliability
- This will retry failed webhook deliveries automatically

## How It Works

### Payment Flow

1. **Order Creation**: Customer creates order via `/api/checkout`
2. **Invoice Generation**: System creates Xendit invoice with `external_id` = `paymentId`
3. **Payment**: Customer pays via Xendit invoice
4. **Webhook Callback**: Xendit sends POST to `/api/webhooks/xendit`
5. **Payment Processing**: System marks payment as completed and continues workflow

### Webhook Payload Structure

Xendit sends a JSON payload like this:

```json
{
  "id": "xendit_invoice_id",
  "external_id": "payment_id",
  "status": "PAID",
  "amount": 10000,
  "paid_at": "2024-01-01T12:00:00.000Z",
  "payment_method": "CREDIT_CARD",
  "payment_channel": "CREDIT_CARD",
  "payment_destination": "BCA"
}
```

### Security

- **Token Verification**: The webhook verifies the `x-callback-token` header
- **HTTPS Only**: Ngrok provides HTTPS encryption
- **Idempotency**: Multiple webhook calls for the same payment are handled safely

## Testing

### Manual Test

1. Create an order via the web UI
2. Copy the invoice URL and pay it manually
3. Check server logs for webhook processing
4. Verify order status updates automatically

### Debugging

Check server logs for:
- `[Xendit Webhook]` messages
- Webhook payload details
- Payment processing status
- Awakeable resolution

## Troubleshooting

### Common Issues

1. **Webhook not received**:
   - Check ngrok is running and accessible
   - Verify webhook URL in Xendit Dashboard
   - Check firewall/network settings

2. **Authentication failed**:
   - Verify `XENDIT_CALLBACK_TOKEN` matches dashboard
   - Check token is correctly set in environment

3. **Order not found**:
   - Verify `external_id` matches `payment_id` in database
   - Check database connection

4. **Awakeable not resolved**:
   - Verify `awakeable_id` is stored in orders table
   - Check Restate runtime is accessible

### Logs to Monitor

```bash
# Server logs
tail -f server.log | grep "Xendit Webhook"

# Ngrok logs
# Check ngrok web interface at http://localhost:4040
```

## Migration from Manual Simulation

Once webhooks are working:

1. **Remove** the simulate payment button from the UI
2. **Update** the UI to show real-time payment status
3. **Test** thoroughly with real payments
4. **Monitor** webhook delivery success rates

The manual simulation endpoint (`/api/orders/{id}/simulate_payment_success`) can remain for testing purposes but should not be used in production.
