# NOFX 项目速览

- **定位**：多智能体 AI 交易操作系统，闭环覆盖“AI 决策 → 统一风控 → 低延迟执行 → 实盘/纸盘回测”，已跑通加密市场（Binance/Hyperliquid/Aster），目标扩展到全金融市场。
- **能力**：多模型自博弈与自进化（DeepSeek/Qwen/自定义）；统一数据与因子库；账户级风控（杠杆/仓位/风报比/去重）；自动精度与滑点控制；实时监控与完整决策日志。
- **技术栈/模块**：后端 Go（Gin, SQLite, TA-Lib, JWT/2FA）；前端 React+TypeScript（Vite, Tailwind, Recharts, Zustand）。核心目录：`api/`（HTTP API）、`trader/`（多交易所执行层）、`decision/`（AI 决策与提示词）、`market/`（行情与指标）、`manager/`（多交易员编排）、`config/`（DB/配置）、`decision_logs/`（决策日志）、`web/`（前端）。
- **部署文档**：根目录 `README.md`；中文架构 `docs/architecture/README.zh-CN.md`；快速开始/部署 `docs/getting-started/README.zh-CN.md`；更新记录 `CHANGELOG.zh-CN.md`。
- **Telegram Bot（Hyperliquid 助手）**：文档 `TELEGRAM_BOT.md`；命令 `/start` 生成账户与 Agent Key，`/balance`/`/positions` 查询资产与持仓，`/deposit` 获取 Arbitrum One USDC 充值地址（≥20 USDC），`/settings` 管理/导出 Agent Key 与 AI API Key；Agent Wallet 安全模式，支持可选 Arbitrum RPC/Gas 赞助（私钥+额度）。
