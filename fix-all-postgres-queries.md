# PostgreSQL 查询修复清单

## 需要修复的函数（使用 convertQuery 辅助函数）

### ✅ 已修复
- [x] CreateUser() - 用户注册
- [x] UpdateUserOTPVerified() - OTP验证状态
- [x] UpdateTraderStatus() - 交易员状态
- [x] UpdateTraderCustomPrompt() - 自定义策略
- [x] DeleteTrader() - 删除交易员
- [x] GetUserByEmail() - 通过邮箱获取用户
- [x] GetUserByID() - 通过ID获取用户

### 🔧 还需修复
- [ ] GetSystemConfig() - 系统配置查询
- [ ] GetAIModels() - AI模型查询
- [ ] UpdateAIModel() - 更新AI模型
- [ ] GetExchanges() - 交易所查询
- [ ] UpdateExchange() - 更新交易所
- [ ] CreateTrader() - 创建交易员
- [ ] GetTraders() - 获取交易员列表
- [ ] GetTraderConfig() - 获取交易员配置

## 修复方法

对于每个函数，将直接的 SQL 查询替换为：
```go
query := d.convertQuery(`原始 SQL 查询`)
// 然后使用 query 变量执行
```

示例：
```go
// 修复前
err := d.db.QueryRow(`SELECT value FROM system_config WHERE key = ?`, key)

// 修复后
query := d.convertQuery(`SELECT value FROM system_config WHERE key = ?`)
err := d.db.QueryRow(query, key)
```