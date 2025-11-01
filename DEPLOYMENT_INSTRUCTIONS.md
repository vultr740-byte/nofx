# Render.com 部署指南

## 🚨 当前网络问题解决方案

由于网络问题无法下载 PostgreSQL 驱动，请按以下步骤操作：

### 方法1：在 Render.com 上直接部署

1. **推送代码到 GitHub**
   ```bash
   git add .
   git commit -m "准备 Supabase 部署"
   git push origin main
   ```

2. **在 Render.com 上设置环境变量**
   ```
   DATABASE_URL=postgresql://postgres:[password]@db.[project].supabase.co:5432/postgres
   JWT_SECRET=your-super-secret-jwt-key
   ADMIN_MODE=true
   ```

3. **Render.com 会自动处理依赖下载**
   - Render.com 有更好的网络连接
   - 会自动下载 `github.com/lib/pq`
   - 编译时会使用 PostgreSQL 驱动

### 方法2：本地网络恢复后

1. **运行启用脚本**
   ```bash
   ./enable-supabase.sh
   ```

2. **本地测试**
   ```bash
   export DATABASE_URL="your-supabase-url"
   ./nofx
   ```

## 📋 Supabase 数据库设置

### 在 Supabase Dashboard 中执行迁移：

1. **进入 SQL Editor**
2. **执行 supabase_migration.sql 的内容**

```sql
-- 这是完整的迁移脚本内容，复制到 Supabase SQL Editor 执行
```

## 🔧 验证部署

部署成功后应该看到：
```
📋 连接到 Supabase PostgreSQL 数据库
✅ Supabase 数据库连接成功，跳过表创建
```

## 🎯 功能测试

1. **用户注册** - 应该存储到 Supabase
2. **创建交易员** - 策略应该持久化
3. **重启应用** - 数据应该保持

## ❓ 如果遇到问题

1. **检查环境变量**
   ```bash
   # 在 Render.com Dashboard 确认环境变量设置正确
   ```

2. **检查 Supabase 连接**
   ```bash
   # 在本地测试连接
   psql $DATABASE_URL -c "SELECT version();"
   ```

3. **检查表是否存在**
   ```sql
   -- 在 Supabase SQL Editor 中执行
   SELECT table_name FROM information_schema.tables
   WHERE table_schema = 'public';
   ```

## 📞 支持

- Render.com 部署文档
- Supabase 文档
- GitHub Issues