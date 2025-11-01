#!/bin/bash

# 启用 Supabase PostgreSQL 支持脚本

echo "🚀 启用 Supabase PostgreSQL 支持..."

# 1. 检查 PostgreSQL 驱动是否已启用
echo "📝 检查 PostgreSQL 驱动..."
if ! grep -q '_ "github.com/lib/pq"' config/database.go; then
    echo "启用 PostgreSQL 驱动..."
    sed -i '' 's|// _ "github.com/lib/pq"  // 暂时注释掉，等网络恢复后启用|_ "github.com/lib/pq"|g' config/database.go
fi

# 2. 检查 PostgreSQL 连接是否已启用
echo "🔗 检查 PostgreSQL 连接..."
if grep -q "需要启用 PostgreSQL 驱动" config/database.go; then
    echo "启用 PostgreSQL 连接..."
    sed -i '' 's|// db, err = sql.Open("postgres", dbURL)  // 等网络恢复后启用|db, err = sql.Open("postgres", dbURL)|g' config/database.go
    sed -i '' '/log.Printf("⚠️  网络问题，暂时使用 SQLite 数据库")/d' config/database.go
    sed -i '' '/db, err = sql.Open("sqlite3", dbPath)/d' config/database.go
fi

# 3. 更新 go.mod 下载依赖
echo "📦 下载 PostgreSQL 驱动..."
go mod tidy

# 4. 重新编译
echo "🔨 重新编译项目..."
go build -o nofx .

echo "✅ Supabase PostgreSQL 支持已启用！"
echo "🎯 现在可以配置 DATABASE_URL 环境变量来使用 Supabase"
echo ""
echo "📋 下一步:"
echo "1. 设置 DATABASE_URL 环境变量"
echo "2. 在 Supabase 中执行 supabase_migration.sql"
echo "3. 启动应用测试"
echo ""
echo "💡 示例环境变量:"
echo 'export DATABASE_URL="postgresql://postgres:password@db.project.supabase.co:5432/postgres"'