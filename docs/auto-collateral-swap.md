# Auto Collateral Swap for HIP-3 DEX

## 目标
当用户在非 USDC 抵押的 HIP‑3 DEX（flx=USDH、vntl=USDH、hyna=USDE）下单时，自动用现有 USDC 兑换所需抵押资产，再开仓，用户无感知。

## 范围外
- 不做跨链/借贷，仅限 Hyperliquid 主网现货对。
- 不新增多条 WS 连接。

## 现状与依赖
- `meta` 可得各 dex 的 `collateralToken`。
- `spotMeta.tokens` 可映射 `token.index -> {name, decimals, tokenId}`。
- 需要存在 `<collateral>/<USDC>` 现货交易对（市价可成交）。
- SDK 暂未封装 Spot 下单，需构造/签名 spot order action（与 perp 同签名流程）。

## 需求拆解
1) 启动阶段  
   - 拉取 `meta`：dex = "", "xyz", "flx", "vntl", "hyna"，缓存 `dexCollateral[dex]`。  
   - 拉取 `spotMeta`，构建 `tokenIndex -> tokenInfo`。  
   - 日志打印：dex、抵押资产名、index、decimals。

2) 下单前检查  
   - 解析 symbol→dex。  
   - 估算保证金 `needed`（数量×价格÷杠杆×1.02）。  
   - 查询目标抵押余额；不足则进入自动兑换，否则直接下单。

3) 自动兑换流程  
   - 确认现货对 `<collateral>/USDC` 存在；否则提示“请直接充值 <token>”。  
   - mid 价取 `allMids`/现货 ticker；滑点保护 0.2%。  
   - 购买数量=缺口/midPx×(1+0.002)。  
   - 构造 Spot 市价买单（IOC/市价，slippageCap 0.5%）。  
   - 轮询 `spotOpenOrders`/`spotUserState`，成交比例≥95%，超时 5s。  
   - 若成交不足/失败：中止开仓并提示。

4) 开仓  
   - 使用最新抵押余额走原 perp 流程。  
   - 若仍不足，失败并提示剩余差额。

5) 安全与阈值  
   - 开关：`autoCollateralSwap`（默认开）。  
   - 最大单次兑换额（USDC）例如 10,000。  
   - 最小成交比例 95%；最大滑点 0.5%。  
   - 仅允许稳定币白名单（USDC/USDH/USDE）。

6) 用户提示  
   - 日志/Telegram 输出 dex、抵押资产、兑换 USDC 金额、预估价、实际成交量。  
   - `/deposit` 文案按 dex 返回抵押资产名。

7) 回退  
   - 无现货对或无价：报“不支持自动兑换，需充值 <token>”。  
   - 部分成交未开仓：已购抵押资产保留，不卖回。

## 接口/数据
- Info POST:  
  - `{"type":"meta","dex":"<dex>"}`  
  - `{"type":"spotMeta"}`  
  - `{"type":"allMids"}` 或现货 ticker  
- Spot 下单：市价买入 `<collateral>` 支付 USDC（需签名 action）。  
- 账户：`SpotUserState` 查抵押余额；`clearinghouseState` 查保证金/仓位。

## 测试计划（真实接口）
- 主网测试钱包（少量 USDC）。  
- 用例：  
  1) dex=flx 无 USDH 余额 → 自动兑换后开小仓成功。  
  2) dex=hyna USDC 不足 → 报 USDC 余额不足。  
  3) 无现货对 → 报不支持自动兑换。  
  4) 滑点过大 → 中止并提示。  
  5) 部分成交 <95% → 中止，不开仓。  
- 验证：抵押余额增加、USDC 减少；/positions 可见仓位；日志含兑换详情。

## 实施次序
1) 启动期 collateral/token 映射与日志。  
2) 封装 Spot 市价下单（action + 成交轮询）。  
3) 下单流程接入自动兑换（参数检查、滑点保护）。  
4) 文案/错误提示与 `/deposit` 适配。  
5) 按测试计划跑真实接口并记录日志路径。
