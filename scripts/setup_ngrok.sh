#!/bin/bash

# Setup script for ngrok webhook tunneling
# This script helps configure ngrok to expose the local server for Xendit webhooks

echo "Setting up ngrok for Xendit webhook callbacks..."
echo ""

# Check if ngrok is installed
if ! command -v ngrok &> /dev/null; then
    echo "❌ ngrok is not installed. Please install it first:"
    echo "   - Visit https://ngrok.com/download"
    echo "   - Or use: brew install ngrok (on macOS)"
    echo "   - Or use: npm install -g ngrok"
    exit 1
fi

echo "✅ ngrok is installed"

# Check if ngrok is authenticated
if ! ngrok config check &> /dev/null; then
    echo "❌ ngrok is not authenticated. Please authenticate first:"
    echo "   - Sign up at https://ngrok.com/"
    echo "   - Run: ngrok config add-authtoken YOUR_AUTH_TOKEN"
    exit 1
fi

echo "✅ ngrok is authenticated"

# Start ngrok tunnel
echo ""
echo "🚀 Starting ngrok tunnel for port 3000..."
echo "   This will expose your local server at: https://YOUR_SUBDOMAIN.ngrok-free.dev"
echo "   Use this URL in your Xendit Dashboard webhook configuration"
echo ""
echo "Press Ctrl+C to stop the tunnel"
echo ""

# Start ngrok with the specified subdomain if provided
if [ ! -z "$1" ]; then
    echo "Using custom subdomain: $1"
    ngrok http 3000 --subdomain="$1"
else
    ngrok http 3000
fi
