# Supabase 部署指南

## 🚀 部署步骤

### 1. 创建 Supabase 项目

1. 访问 [supabase.com](https://supabase.com)
2. 注册账户并创建新项目
3. 记录以下信息：
   - **项目 URL**: `https://[project].supabase.co`
   - **数据库密码**: 自动生成的密码
   - **数据库连接字符串**: 在 Settings > Database 获取

### 2. 执行数据库迁移

在 Supabase Dashboard 的 SQL Editor 中执行 `supabase_migration.sql` 文件：

```sql
-- 复制 supabase_migration.sql 中的所有内容
-- 在 SQL Editor 中粘贴并执行
```

### 3. 配置环境变量

#### 本地开发
```bash
# 复制环境变量模板
cp .env.example .env

# 编辑 .env 文件
nano .env
```

#### Render.com 部署
在 Render.com Dashboard 中设置环境变量：

```bash
DATABASE_URL=postgresql://postgres:[password]@db.[project].supabase.co:5432/postgres
JWT_SECRET=your-super-secret-jwt-key
ADMIN_MODE=true
API_PORT=8080
```

### 4. 更新 Go 依赖

```bash
# 下载 PostgreSQL 驱动
go mod tidy

# 测试编译
go build -o nofx
```

### 5. 本地测试

```bash
# 使用 Supabase 数据库测试
export DATABASE_URL="postgresql://postgres:[password]@db.[project].supabase.co:5432/postgres"
export JWT_SECRET="your-test-jwt-secret"

./nofx
```

### 6. 部署到 Render.com

1. 推送代码到 GitHub
2. 在 Render.com 中创建新的 Web Service
3. 设置环境变量
4. 部署完成！

## 📋 配置说明

### 关键环境变量

| 变量名 | 说明 | 示例 |
|--------|------|------|
| `DATABASE_URL` | PostgreSQL 连接字符串 | `postgresql://postgres:pass@db.xxx.supabase.co:5432/postgres` |
| `JWT_SECRET` | JWT 签名密钥 | `your-super-secret-key` |
| `ADMIN_MODE` | 管理员模式 | `true/false` |
| `API_PORT` | API 端口 | `8080` |

### 数据库连接字符串格式

```bash
postgresql://postgres:[YOUR_PASSWORD]@db.[PROJECT_ID].supabase.co:5432/postgres
```

## 🛠️ 故障排除

### 常见问题

1. **连接失败**
   - 检查 DATABASE_URL 格式
   - 确认 Supabase 项目状态
   - 验证密码是否正确

2. **表不存在错误**
   - 确认执行了迁移脚本
   - 检查 SQL 语句是否执行成功

3. **权限错误**
   - 检查 Supabase RLS 策略
   - 确认数据库用户权限

### 调试命令

```bash
# 检查数据库连接
psql $DATABASE_URL -c "SELECT version();"

# 查看表结构
psql $DATABASE_URL -c "\dt users;"

# 检查环境变量
printenv | grep DATABASE_URL
```

## 🎉 完成验证

部署完成后，访问应用验证：

1. ✅ 用户注册/登录功能正常
2. ✅ 创建交易员功能正常
3. ✅ 策略配置持久化保存
4. ✅ 重启后数据不丢失

## 📞 支持

- [Supabase 文档](https://supabase.com/docs)
- [Render.com 文档](https://render.com/docs)
- 项目 Issues 页面