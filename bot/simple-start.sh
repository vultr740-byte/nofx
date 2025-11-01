#!/bin/bash

echo "🚀 NOFX Telegram Bot - 简化启动脚本"
echo "=================================="

# 检查必要的环境变量
if [ ! -f "../.env" ]; then
    echo "⚠️  主项目.env文件不存在，创建基础配置..."
    cat > ../.env << EOF
# Bot API认证配置
BOT_API_TOKEN=nofx_bot_api_token_2024
BOT_API_SECRET=nofx_bot_api_secret_2024
BOT_MAX_TIME_DRIFT=300

# 数据库配置
DATABASE_URL=postgresql://postgres:FZjEE9KzIU3d2nLk@db.sybkpnfhieppsvzcgags.supabase.co:5432/postgres
JWT_SECRET=Mk03vVzgY5sjAvN5cdeC585jXJV4EWTBAVK00qra60g
EOF
    echo "✅ 创建了主项目.env文件"
fi

echo ""
echo "📋 当前Bot配置："
echo "🤖 Bot Token: ${BOT_TOKEN:0:10}..."
echo "🔐 Webhook Secret: ${WEBHOOK_SECRET}"
echo "🌐 API Base URL: ${GO_API_BASE_URL}"
echo ""

# 启动Go服务器
echo "🚀 启动Go API服务器..."
echo "📡 按Ctrl+C停止服务器"
echo ""

# 加载环境变量并启动Go服务器
export BOT_API_TOKEN="${BOT_API_TOKEN}"
export BOT_API_SECRET="${BOT_API_SECRET}"
export BOT_MAX_TIME_DRIFT="300"
export DATABASE_URL="postgresql://postgres:FZjEE9KzIU3d2nLk@db.sybkpnfhieppsvzcgags.supabase.co:5432/postgres"
export JWT_SECRET="Mk03vVzgY5sjAvN5cdeC585jXJV4EWTBAVK00qra60g"

./nofx-api