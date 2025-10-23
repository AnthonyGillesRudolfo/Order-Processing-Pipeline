#!/bin/bash

# OpenTelemetry Integration Test Script
echo "🚀 Testing OpenTelemetry Integration with Jaeger"

# Start services
echo "📦 Starting services with docker-compose..."
docker-compose up -d

# Wait for services to be ready
echo "⏳ Waiting for services to start..."
sleep 10

# Check if Jaeger is accessible
echo "🔍 Checking Jaeger UI accessibility..."
if curl -s http://localhost:16686 > /dev/null; then
    echo "✅ Jaeger UI is accessible at http://localhost:16686"
else
    echo "❌ Jaeger UI is not accessible"
fi

# Check if OTLP endpoint is accessible
echo "🔍 Checking OTLP HTTP endpoint..."
if curl -s http://localhost:4318 > /dev/null; then
    echo "✅ OTLP HTTP endpoint is accessible at http://localhost:4318"
else
    echo "❌ OTLP HTTP endpoint is not accessible"
fi

# Build and run the Go application
echo "🔨 Building Go application..."
cd /Users/anthonygillesr/Order-Processing-Pipeline
go build -o order-processing-pipeline ./cmd/server

echo "🏃 Running Go application with OpenTelemetry..."
export OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318
./order-processing-pipeline &

# Wait a moment for the app to start
sleep 5

# Make some test API calls to generate traces
echo "📡 Making test API calls to generate traces..."

# Test checkout endpoint
curl -X POST http://localhost:3000/api/checkout \
  -H "Content-Type: application/json" \
  -d '{
    "customerId": "test-customer-123",
    "merchantId": "test-merchant-456",
    "items": [
      {
        "itemId": "item-1",
        "quantity": 2,
        "price": 10.50
      }
    ]
  }' || echo "Checkout test failed"

# Test orders list
curl http://localhost:3000/api/orders || echo "Orders list test failed"

echo "✅ Test API calls completed"

echo ""
echo "🎯 OpenTelemetry Integration Complete!"
echo ""
echo "📊 View traces in Jaeger UI: http://localhost:16686"
echo "🔍 Look for service: 'order-processing-pipeline'"
echo "📈 You should see traces for:"
echo "   - HTTP requests (checkout, orders, etc.)"
echo "   - Database operations (PostgreSQL queries)"
echo "   - Kafka message publishing"
echo ""
echo "🛑 To stop services: docker-compose down"
echo "🛑 To stop the Go app: kill %1"

