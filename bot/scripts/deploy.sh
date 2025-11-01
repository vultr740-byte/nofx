#!/bin/bash

# NOFX Telegram Bot Deployment Script

set -e

echo "🚀 Starting NOFX Telegram Bot deployment..."

# Check if .env file exists
if [ ! -f .env ]; then
    echo "❌ .env file not found. Please copy .env.example to .env and configure it."
    exit 1
fi

# Load environment variables
source .env

# Check required environment variables
required_vars=("BOT_TOKEN" "WEBHOOK_SECRET" "GO_API_BASE_URL" "BOT_API_TOKEN" "BOT_API_SECRET")

for var in "${required_vars[@]}"; do
    if [ -z "${!var}" ]; then
        echo "❌ Required environment variable $var is not set"
        exit 1
    fi
done

echo "✅ Environment variables validated"

# Build and deploy with Docker
echo "🐳 Building Docker image..."
docker build -t nofx-bot .

echo "🚀 Starting container..."
docker run -d \
    --name nofx-telegram-bot \
    --restart unless-stopped \
    --env-file .env \
    -p 3000:3000 \
    nofx-bot

echo "✅ Bot deployed successfully!"
echo "📊 Health check: curl http://localhost:3000/health"

# Wait a moment for the container to start
sleep 5

# Health check
if curl -f http://localhost:3000/health > /dev/null 2>&1; then
    echo "✅ Bot is healthy and running"
else
    echo "⚠️  Bot may not be running properly. Check logs with: docker logs nofx-telegram-bot"
fi