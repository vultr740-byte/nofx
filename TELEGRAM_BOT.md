# NOFX Telegram Bot 使用指南

## 🤖 简介

NOFX Telegram Bot 是一个基于 Telegram 的 Hyperliquid 交易机器人，提供便捷的账户管理和交易查询功能。

## ✨ 功能特性

- **🚀 自动创建账号**: 使用 `/start` 命令自动生成 Hyperliquid 账号
- **💰 余额查询**: 使用 `/balance` 查看现货和合约余额
- **📊 持仓查询**: 使用 `/positions` 查看当前持仓信息
- **🔑 动态配置 AI 密钥**: 在 `/settings` 中更新 AI API KEY
- **💳 充值**: 使用 `/deposit` 获取充值入口（Arbitrum 直充 / 跨链充值 USDC）
- **📦 充值状态**: 使用 `/deposit_status` 查询跨链充值进度
- **🔒 安全可靠**: 使用 Agent Wallet 模式，保障资金安全

## 🛠️ 配置步骤

### 1. 创建 Telegram Bot

1. 在 Telegram 中搜索 `@BotFather`
2. 发送 `/newbot` 创建新机器人
3. 按提示设置机器人名称和用户名
4. 获取 Bot Token

### 2. 配置环境变量

在 `.env` 中填入 Bot Token（服务默认开启 Telegram Bot）：

```env
# 设置 Bot Token（从 BotFather 获取）
TELEGRAM_BOT_TOKEN=your-telegram-bot-token-here

# 调试模式（可选）
TELEGRAM_DEBUG=false
```

### 3. 启动服务

```bash
# 开发模式
go run main.go

# 或使用 Docker
docker compose up -d
```

## 📱 使用指南

### 基础命令

| 命令 | 功能 | 说明 |
|------|------|------|
| `/start` | 创建/查看账号 | 自动生成 Hyperliquid 账号 |
| `/help` | 帮助信息 | 显示所有可用命令 |
| `/balance` | 余额查询 | 查看现货和合约余额 |
| `/positions` | 持仓查询 | 查看当前持仓信息 |
| `/deposit` | 充值 | 获取充值入口（Arbitrum 直充 / 跨链充值 USDC） |
| `/deposit_status` | 充值状态 | 查询跨链充值状态：`/deposit_status <depositAddress> [depositMemo]` |
| `/settings` | ⚙️ 设置 | 导出 Agent 私钥等操作 |

### 使用流程

1. **首次使用**
   ```
   /start → 系统自动生成 Agent Key 和钱包地址
   ```

2. **充值资金**
   ```
   /deposit → 获取充值地址 → 转账 USDC
   ```

3. **查询账户**
   ```
   /balance → 查看余额
   /positions → 查看持仓
   ```

## 🔐 安全说明

### Agent Wallet 模式

- **Agent Key**: 仅用于交易签名，余额应接近 0
- **Main Wallet**: 存储资金，永不暴露私钥
- **安全验证**: 系统会检查 Agent Wallet 余额，确保安全

### 重要提醒

- ⚠️ 妥善保管 Agent Key，不要泄露给他人
- ⚠️ Agent Wallet 余额应保持在 0 USDC 附近
- ⚠️ 主钱包资金安全，请勿暴露私钥
- ⚠️ 如有疑问，请通过官方渠道联系客服
- ⚠️ 如需备份 Agent 私钥，可在 `/settings` 中导出，复制后请立即删除聊天记录

## 💰 充值说明

### 支持方式

#### 方式 1：Arbitrum 直充

直接从 Arbitrum 网络转 USDC 到你的钱包地址（`/deposit` 可复制）。

#### 方式 2：跨链充值（NEAR Intents 1Click）

如果你的 USDC 在其他网络（EVM/Solana/Sui），可以通过 `/deposit` 选择来源网络与金额，系统会生成一个“专属充值地址（可能需要 Memo）”，你把 USDC 转到该地址后，1Click 会自动跨链把 USDC 发送到你在 Arbitrum 的收款地址。

你可以用 `/deposit_status` 查询进度；显示“成功”后，再执行 `/balance` 触发自动充值到 Hyperliquid（若已启用）。

### 最小充值

- Hyperliquid 最小充值：**20 USDC**
- 跨链充值会产生费用/滑点，建议输入金额略高于 20 USDC，确保最终 Arbitrum 到账 ≥ 20 USDC。
- 自动充值到 Hyperliquid 由系统代付 Gas（用户地址无需预存 ETH）。

### 充值步骤

1. 使用 `/deposit` 获取充值地址
2. 从其他交易所或钱包转账 USDC
3. 等待网络确认
4. 使用 `/balance` 查看到账情况

## 📊 账户信息解读

### 余额信息

- **总资产**: 现货余额 + 合约净值
- **现货余额**: 仅现货账户的 USDC
- **合约净值**: 合约账户总价值
- **可用余额**: 可用于开仓的资金
- **未实现盈亏**: 当前持仓的盈亏状况

### 持仓信息

- **方向**: 多头 (🟢) 或空头 (🔴)
- **数量**: 持仓数量
- **入场价**: 开仓时的价格
- **标记价**: 当前市场价格
- **盈亏**: 未实现盈亏和百分比

## 🔧 故障排除

### 常见问题

1. **Bot 无响应**
   - 检查 `TELEGRAM_BOT_TOKEN` 是否正确
   - 查看服务器日志

2. **查询失败**
   - 检查网络连接
   - 确认 Hyperliquid 服务正常
   - 验证账号配置

3. **充值未到账**
   - 确认网络为 Arbitrum One
   - 检查转账地址是否正确
   - 等待网络确认（通常 2-5 分钟）

### 日志查看

```bash
# 查看实时日志
docker compose logs -f nofx

# 查看特定日志
docker compose logs nofx | grep telegram
```

## 🆘 技术支持

如遇到问题，请：

1. 查看服务器日志
2. 确认配置正确
3. 检查网络连接
4. 联系技术支持

---

**⚠️ 风险提示**:
- 自动交易有风险，请谨慎操作
- 建议小额资金测试
- 请勿投入超过可承受损失的资金
