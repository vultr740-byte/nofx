# HIP-3 兼容改动复盘（2025-12-24）

## 资产 ID 映射
- 规则：`assetId = 100000 + dexIndex*10000 + index_in_meta`。
- 范围：仅为带冒号的 HIP-3 资产补充映射，保留主 perp 的原生映射，避免多 DEX 展平导致错路由。
- 位置：`ensureAssetMap()` (`trader/hyperliquid_trader.go`).

## 价格精度
- 统一按 Tick & Lot：先截 5 位有效数字，再截到 `maxDecimals = 6 - szDecimals`（perp）。整数直通。
- 去除 pxDecimals 依赖及 HIP-3 固定 0.1 tick 特例。
- 位置：`roundPriceForCoin` / `getMaxPriceDecimals` (`trader/hyperliquid_trader.go`).

## 自动开启 DEX 抽象模式
- 背景：HIP-3 保证金与主 perp 分账，未开启抽象会报 “Insufficient margin”。
- 机制：在 `SetLeverage` 检测到 HIP-3 时调用 `enableDexAbstractionOnce()`，用 agent 签名 action `agentEnableDexAbstraction` POST `/exchange`；失败仅日志，不阻塞下单。
- 位置：`SetLeverage` & `enableDexAbstractionOnce` (`trader/hyperliquid_trader.go`).

## SDK 升级
- Go SDK 升级到 `github.com/sonirico/go-hyperliquid v0.26.0`，可读 `DexAbstractionEnabled`（webData3），暂无官方启用封装，暂用自实现。

## 保证金行为
- 抽象关闭：主 perp 与各 HIP-3 DEX 独立余额。
- 抽象开启：HIP-3（USDC 抵押）直接用主 perp USDC；其他抵押走 spot。

## 提交
- `Fix HIP-3 assetId mapping to official formula`
- `Auto-enable HIP-3 dex abstraction before orders`
- `Bump go-hyperliquid to v0.26.0`

## 后续建议
- 若官方 SDK 增加 `AgentEnableDexAbstraction`，替换自实现。
- `/balance` 可显示 `DexAbstractionEnabled` 状态与各 dex 余额，减少困惑。
- 自实现 action 如官方签名格式变更需同步更新。
