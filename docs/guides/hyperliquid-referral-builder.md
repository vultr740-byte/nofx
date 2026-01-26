# Hyperliquid Referral & Builder Fee 速查

本文整理 Hyperliquid 的 referral code（推荐码）与 builder fee（建造者费）规则差异、绑定要求，以及在本项目中的落地提示，便于产品/工程统一认知。

## 一句话结论
- **Referral code**：账户级别绑定，长期生效，主要用于拉新返佣与手续费折扣。
- **Builder fee**：订单级别收费，按单可选，收给应用/路由方，需要用户先授权 builder。

## Referral code（推荐码）绑定与要求
- **谁能创建推荐码**：通常需要累计交易量达到一定门槛（官方写的是 $10,000 交易量）才可创建推荐码。
- **绑定方式**：用户通过推荐链接或在 Referrals 页面输入推荐码进行绑定。
- **折扣/返佣（摘要）**：
  - 被推荐人：通常享受一定手续费折扣（官方写的是首 $25M 交易量内 4% 折扣）。
  - 推荐人：通常获得一定比例的手续费返佣（官方写的是首 $1B 交易量内）。
- **适用范围限制**：
  - 通常不适用于 vaults / sub‑accounts（它们被当作独立账户处理）。

## Builder fee（建造者费）绑定与要求
- **订单级别**：是否收 builder fee 取决于具体订单是否带 builder 参数。
- **需要授权**：用户必须先用**主钱包**批准某 builder 的最高费率（ApproveBuilderFee）。
- **额度与范围**：
  - Perps 上限 0.1%，Spot 上限 1%。
  - Spot 买入侧通常不适用（按官方说明）。
- **参数形式（示例）**：
  - 订单参数中可携带 `{ "b": builderAddress, "f": feeRate }`。
  - `f` 通常是“十分之一基点”（10 = 1bp）。
- **优先级关系**：
  - 同一笔订单中，builder fee 会覆盖 referral code 的优惠/返佣效果。

## 两者对比（要点）
- **作用范围**：Referral = 账户级别；Builder = 订单级别。
- **费用去向**：Referral 返佣给推荐人；Builder fee 直接给 builder（应用/路由方）。
- **触发方式**：Referral 绑定一次长期生效；Builder fee 需要逐笔订单显式添加并先授权。

## 本项目现状（代码层）
- 当前代码**未启用** referrer/builder fee：
  - `ApproveBuilderFee` 与 `SetReferrer` 相关能力存在于 SDK，但本项目未调用。
  - `hyenaBuilderInfo()` 目前返回 `nil`，并标注为暂时禁用。

## 落地建议（如需开启）
- **只要“账户级别折扣/返佣”**：做一次 `SetReferrer` 绑定即可，后续所有订单自动生效。
- **要“应用/路由方收益”**：
  1. 让用户主钱包执行 `ApproveBuilderFee`（设定最大费率）。
  2. 在下单动作中带上 builder 参数（b/f）。

## 参考链接（官方文档）
以下链接仅用于人工查证：
```
https://hyperliquid.gitbook.io/hyperliquid-docs/referrals
https://hyperliquid.gitbook.io/hyperliquid-docs/trading/builder-codes
```
