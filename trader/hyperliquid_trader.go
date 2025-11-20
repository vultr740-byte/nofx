package trader

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/sonirico/go-hyperliquid"
)

// HyperliquidTrader Hyperliquid交易器
type HyperliquidTrader struct {
	exchange      *hyperliquid.Exchange
	ctx           context.Context
	walletAddr    string
	meta          *hyperliquid.Meta // 缓存meta信息（包含精度等）
	isCrossMargin bool              // 是否为全仓模式
}

// NewHyperliquidTrader 创建Hyperliquid交易器
func NewHyperliquidTrader(privateKeyHex string, walletAddr string, testnet bool) (*HyperliquidTrader, error) {
	// 去掉私钥的 0x 前缀（如果有，不区分大小写）
	privateKeyHex = strings.TrimPrefix(strings.ToLower(privateKeyHex), "0x")

	// 解析私钥
	privateKey, err := crypto.HexToECDSA(privateKeyHex)
	if err != nil {
		return nil, fmt.Errorf("解析私钥失败: %w", err)
	}

	// 选择API URL
	apiURL := hyperliquid.MainnetAPIURL
	if testnet {
		apiURL = hyperliquid.TestnetAPIURL
	}

	// Security enhancement: Implement Agent Wallet best practices
	// Reference: https://hyperliquid.gitbook.io/hyperliquid-docs/for-developers/api/nonces-and-api-wallets
	agentAddr := crypto.PubkeyToAddress(*privateKey.Public().(*ecdsa.PublicKey)).Hex()

	if walletAddr == "" {
		return nil, fmt.Errorf("❌ Configuration error: Main wallet address (hyperliquid_wallet_addr) not provided\n" +
			"🔐 Correct configuration pattern:\n" +
			"  1. hyperliquid_private_key = Agent Private Key (for signing only, balance should be ~0)\n" +
			"  2. hyperliquid_wallet_addr = Main Wallet Address (holds funds, never expose private key)\n" +
			"💡 Please create an Agent Wallet on Hyperliquid official website and authorize it before configuration:\n" +
			"   https://app.hyperliquid.xyz/ → Settings → API Wallets")
	}

	// Check if user accidentally uses main wallet private key (security risk)
	if strings.EqualFold(walletAddr, agentAddr) {
		log.Printf("⚠️⚠️⚠️ WARNING: Main wallet address (%s) matches Agent wallet address!", walletAddr)
		log.Printf("   This indicates you may be using your main wallet private key, which poses extremely high security risks!")
		log.Printf("   Recommendation: Immediately create a separate Agent Wallet on Hyperliquid official website")
		log.Printf("   Reference: https://hyperliquid.gitbook.io/hyperliquid-docs/for-developers/api/nonces-and-api-wallets")
	} else {
		log.Printf("✓ Using Agent Wallet mode (secure)")
		log.Printf("  └─ Agent wallet address: %s (for signing)", agentAddr)
		log.Printf("  └─ Main wallet address: %s (holds funds)", walletAddr)
	}

	ctx := context.Background()

	// 创建Exchange客户端（Exchange包含Info功能）
	exchange := hyperliquid.NewExchange(
		ctx,
		privateKey,
		apiURL,
		nil,        // Meta will be fetched automatically
		"",         // vault address (empty for personal account)
		walletAddr, // wallet address
		nil,        // SpotMeta will be fetched automatically
	)

	log.Printf("✓ Hyperliquid交易器初始化成功 (testnet=%v, wallet=%s)", testnet, walletAddr)

	// 获取meta信息（包含精度等配置）
	meta, err := exchange.Info().Meta(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取meta信息失败: %w", err)
	}

	// 🔍 Security check: Validate Agent wallet balance (should be close to 0)
	// Only check if using separate Agent wallet (not when main wallet is used as agent)
	if !strings.EqualFold(walletAddr, agentAddr) {
		agentState, err := exchange.Info().UserState(ctx, agentAddr)
		if err == nil && agentState != nil && agentState.CrossMarginSummary.AccountValue != "" {
			// Parse Agent wallet balance
			agentBalance, _ := strconv.ParseFloat(agentState.CrossMarginSummary.AccountValue, 64)

			if agentBalance > 100 {
				// Critical: Agent wallet holds too much funds
				log.Printf("🚨🚨🚨 CRITICAL SECURITY WARNING 🚨🚨🚨")
				log.Printf("   Agent wallet balance: %.2f USDC (exceeds safe threshold of 100 USDC)", agentBalance)
				log.Printf("   Agent wallet address: %s", agentAddr)
				log.Printf("   ⚠️  Agent wallets should only be used for signing and hold minimal/zero balance")
				log.Printf("   ⚠️  High balance in Agent wallet poses security risks")
				log.Printf("   📖 Reference: https://hyperliquid.gitbook.io/hyperliquid-docs/for-developers/api/nonces-and-api-wallets")
				log.Printf("   💡 Recommendation: Transfer funds to main wallet and keep Agent wallet balance near 0")
				return nil, fmt.Errorf("security check failed: Agent wallet balance too high (%.2f USDC), exceeds 100 USDC threshold", agentBalance)
			} else if agentBalance > 10 {
				// Warning: Agent wallet has some balance (acceptable but not ideal)
				log.Printf("⚠️  Notice: Agent wallet address (%s) has some balance: %.2f USDC", agentAddr, agentBalance)
				log.Printf("   While not critical, it's recommended to keep Agent wallet balance near 0 for security")
			} else {
				// OK: Agent wallet balance is safe
				log.Printf("✓ Agent wallet balance is safe: %.2f USDC (near zero as recommended)", agentBalance)
			}
		} else if err != nil {
			// Failed to query agent balance - log warning but don't block initialization
			log.Printf("⚠️  Could not verify Agent wallet balance (query failed): %v", err)
			log.Printf("   Proceeding with initialization, but please manually verify Agent wallet balance is near 0")
		}
	}

	return &HyperliquidTrader{
		exchange:      exchange,
		ctx:           ctx,
		walletAddr:    walletAddr,
		meta:          meta,
		isCrossMargin: true, // 默认使用全仓模式
	}, nil
}

// GetBalance 获取账户余额
func (t *HyperliquidTrader) GetBalance() (map[string]interface{}, error) {
	log.Printf("🔄 正在调用Hyperliquid API获取账户余额...")

	// ✅ Step 1: 查询 Spot 现货账户余额
	spotState, err := t.exchange.Info().SpotUserState(t.ctx, t.walletAddr)
	var spotUSDCBalance float64 = 0.0
	if err != nil {
		log.Printf("⚠️ 查询 Spot 余额失败（可能无现货资产）: %v", err)
	} else if spotState != nil && len(spotState.Balances) > 0 {
		for _, balance := range spotState.Balances {
			if balance.Coin == "USDC" {
				spotUSDCBalance, _ = strconv.ParseFloat(balance.Total, 64)
				log.Printf("✓ 发现 Spot 现货余额: %.2f USDC", spotUSDCBalance)
				break
			}
		}
	}

	// ✅ Step 2: 查询 Perpetuals 合约账户状态
	accountState, err := t.exchange.Info().UserState(t.ctx, t.walletAddr)
	if err != nil {
		log.Printf("❌ Hyperliquid Perpetuals API调用失败: %v", err)
		return nil, fmt.Errorf("获取账户信息失败: %w", err)
	}

	// 解析余额信息（MarginSummary字段都是string）
	result := make(map[string]interface{})

	// ✅ Step 3: 根据保证金模式动态选择正确的摘要（CrossMarginSummary 或 MarginSummary）
	var accountValue, totalMarginUsed float64
	var summaryType string
	var summary interface{}

	if t.isCrossMargin {
		// 全仓模式：使用 CrossMarginSummary
		accountValue, _ = strconv.ParseFloat(accountState.CrossMarginSummary.AccountValue, 64)
		totalMarginUsed, _ = strconv.ParseFloat(accountState.CrossMarginSummary.TotalMarginUsed, 64)
		summaryType = "CrossMarginSummary (全仓)"
		summary = accountState.CrossMarginSummary
	} else {
		// 逐仓模式：使用 MarginSummary
		accountValue, _ = strconv.ParseFloat(accountState.MarginSummary.AccountValue, 64)
		totalMarginUsed, _ = strconv.ParseFloat(accountState.MarginSummary.TotalMarginUsed, 64)
		summaryType = "MarginSummary (逐仓)"
		summary = accountState.MarginSummary
	}

	// 🔍 调试：打印API返回的完整摘要结构
	summaryJSON, _ := json.MarshalIndent(summary, "  ", "  ")
	log.Printf("🔍 [DEBUG] Hyperliquid API %s 完整数据:", summaryType)
	log.Printf("%s", string(summaryJSON))

	
	// ⚠️ 关键修复：从所有持仓中累加真正的未实现盈亏
	totalUnrealizedPnl := 0.0
	for _, assetPos := range accountState.AssetPositions {
		unrealizedPnl, _ := strconv.ParseFloat(assetPos.Position.UnrealizedPnl, 64)
		totalUnrealizedPnl += unrealizedPnl
	}

	// ✅ 正确理解Hyperliquid字段：
	// AccountValue = 总账户净值（已包含空闲资金+持仓价值+未实现盈亏）
	// TotalMarginUsed = 持仓占用的保证金（已包含在AccountValue中，仅用于显示）

	// ✅ Step 4: 优化的可用余额计算逻辑
	availableBalance := 0.0

	// 优先使用 Withdrawable 字段（官方提供的最准确值）
	if accountState.Withdrawable != "" {
		withdrawable, err := strconv.ParseFloat(accountState.Withdrawable, 64)
		if err == nil {
			availableBalance = withdrawable
			log.Printf("✓ 使用 Withdrawable 字段: %.2f USDC", availableBalance)
		} else {
			log.Printf("⚠️ Withdrawable 字段解析失败: %v", err)
		}
	} else {
		log.Printf("⚠️ Withdrawable 字段为空，使用降级计算")
	}

	// 降级方案：如果没有有效的 Withdrawable，使用更准确的计算
	if availableBalance == 0 {
		// AccountValue = 可用余额 + 占用保证金
		// 所以：可用余额 = AccountValue - 占用保证金
		availableBalance = accountValue - totalMarginUsed

		if availableBalance < 0 {
			log.Printf("⚠️ 计算的可用余额为负数 (%.2f)，重置为 0", availableBalance)
			availableBalance = 0
		} else {
			log.Printf("✓ 使用降级计算 (AccountValue - MarginUsed): %.2f USDC", availableBalance)
		}
	}

	// ✅ Step 5: 正确处理 Spot + Perpetuals 余额
	// 修复：AccountValue 已经是完整投资组合价值（包含现金+持仓+未实现盈亏）
	// 不应该再重复添加 totalMarginUsed，这会导致余额偏高
	totalWalletBalance := accountValue + spotUSDCBalance

	
	result["totalWalletBalance"] = totalWalletBalance    // 总资产（Perp + Spot）
	result["availableBalance"] = availableBalance        // 可用余额（仅 Perpetuals，不含 Spot）
	result["totalUnrealizedProfit"] = totalUnrealizedPnl // 未实现盈亏（仅来自 Perpetuals）
	result["spotBalance"] = spotUSDCBalance              // Spot 现货余额（单独返回）

	// 增强的调试日志：显示完整的余额字段映射
	log.Printf("🔍 [DEBUG] Hyperliquid 余额字段详情:")
	log.Printf("  • AccountValue (合约净值): %.2f USDC", accountValue)
	log.Printf("  • Withdrawable (可提现): %.2f USDC", availableBalance)
	log.Printf("  • TotalMarginUsed (占用保证金): %.2f USDC", totalMarginUsed)
	log.Printf("  • SpotUSDCBalance (现货余额): %.2f USDC", spotUSDCBalance)
	log.Printf("  • TotalUnrealizedPnL (未实现盈亏): %.2f USDC", totalUnrealizedPnl)
	log.Printf("")
	log.Printf("✅ 修复后的计算逻辑:")
	log.Printf("  • 总资产 = AccountValue + SpotUSDCBalance = %.2f + %.2f = %.2f USDC",
		accountValue, spotUSDCBalance, totalWalletBalance)
	log.Printf("")
	log.Printf("💰 账户总览:")
	log.Printf("  • 总资产 (Perp+Spot): %.2f USDC", totalWalletBalance)
	log.Printf("  • Perpetuals 可用余额: %.2f USDC", availableBalance)
	log.Printf("  • Spot 现货余额: %.2f USDC", spotUSDCBalance)
	log.Printf("  • 未实现盈亏: %.2f USDC", totalUnrealizedPnl)
	log.Printf("  ⭐ 与 Hyperliquid 官网对比: 总资产 %.2f USDC", totalWalletBalance)

	return result, nil
}

// GetPositions 获取所有持仓
func (t *HyperliquidTrader) GetPositions() ([]map[string]interface{}, error) {
	// 获取账户状态
	accountState, err := t.exchange.Info().UserState(t.ctx, t.walletAddr)
	if err != nil {
		return nil, fmt.Errorf("获取持仓失败: %w", err)
	}

	log.Printf("🔍 [DEBUG] Hyperliquid API返回 %d 个资产持仓", len(accountState.AssetPositions))

	var result []map[string]interface{}

	// 遍历所有持仓
	for i, assetPos := range accountState.AssetPositions {
		position := assetPos.Position

		// 记录原始数据用于调试
		log.Printf("🔍 [DEBUG] 资产持仓 %d: Coin=%s, Szi=%s, PositionValue=%s",
			i, position.Coin, position.Szi, position.PositionValue)

		// 增强的持仓数量解析
		posAmt, err := t.parsePositionSzi(position.Szi)
		if err != nil {
			log.Printf("❌ 跳过无效持仓 %d: %v", i, err)
			continue
		}

		if posAmt == 0 {
			log.Printf("⚪ 跳过零持仓 %s", position.Coin)
			continue
		}

		log.Printf("✅ 发现有效持仓 %s: 数量=%.6f", position.Coin, posAmt)

		posMap := make(map[string]interface{})

		// 标准化symbol格式（Hyperliquid使用如"BTC"，我们转换为"BTCUSDT"）
		symbol := position.Coin + "USDT"
		posMap["symbol"] = symbol

		// 持仓数量和方向 - 保留原始符号用于平仓判断
		if posAmt > 0 {
			posMap["side"] = "long"
		} else {
			posMap["side"] = "short"
		}
		posMap["quantity"] = posAmt // 保留原始符号（空头为负数，多头为正数）

		// 为了向后兼容，同时保留positionAmt字段（绝对值）
		posMap["positionAmt"] = math.Abs(posAmt)

		// 同时保留 Szi 原始值用于调试
		posMap["Szi"] = position.Szi

		// 价格信息（EntryPx和LiquidationPx是指针类型）
		var entryPrice, liquidationPx float64
		if position.EntryPx != nil {
			entryPrice, _ = strconv.ParseFloat(*position.EntryPx, 64)
		}
		if position.LiquidationPx != nil {
			liquidationPx, _ = strconv.ParseFloat(*position.LiquidationPx, 64)
		}

		positionValue, _ := strconv.ParseFloat(position.PositionValue, 64)
		unrealizedPnl, _ := strconv.ParseFloat(position.UnrealizedPnl, 64)

		// 计算mark price（positionValue / abs(posAmt)）
		var markPrice float64
		if posAmt != 0 {
			markPrice = positionValue / absFloat(posAmt)
		}

		posMap["entryPrice"] = entryPrice
		posMap["markPrice"] = markPrice
		posMap["unRealizedProfit"] = unrealizedPnl
		posMap["leverage"] = float64(position.Leverage.Value)
		posMap["liquidationPrice"] = liquidationPx

		result = append(result, posMap)
	}

	return result, nil
}

// SetMarginMode 设置仓位模式 (在SetLeverage时一并设置)
func (t *HyperliquidTrader) SetMarginMode(symbol string, isCrossMargin bool) error {
	// Hyperliquid的仓位模式在SetLeverage时设置，这里只记录
	t.isCrossMargin = isCrossMargin
	marginModeStr := "全仓"
	if !isCrossMargin {
		marginModeStr = "逐仓"
	}
	log.Printf("  ✓ %s 将使用 %s 模式", symbol, marginModeStr)
	return nil
}

// SetLeverage 设置杠杆
func (t *HyperliquidTrader) SetLeverage(symbol string, leverage int) error {
	// Hyperliquid symbol格式（去掉USDT后缀）
	coin := convertSymbolToHyperliquid(symbol)

	// 调用UpdateLeverage (leverage int, name string, isCross bool)
	// 第三个参数: true=全仓模式, false=逐仓模式
	_, err := t.exchange.UpdateLeverage(t.ctx, leverage, coin, t.isCrossMargin)
	if err != nil {
		return fmt.Errorf("设置杠杆失败: %w", err)
	}

	log.Printf("  ✓ %s 杠杆已切换为 %dx", symbol, leverage)
	return nil
}

// OpenLong 开多仓
func (t *HyperliquidTrader) OpenLong(symbol string, quantity float64, leverage int) (map[string]interface{}, error) {
	// 先取消该币种的所有委托单
	if err := t.CancelAllOrders(symbol); err != nil {
		log.Printf("  ⚠ 取消旧委托单失败: %v", err)
	}

	// 设置杠杆
	if err := t.SetLeverage(symbol, leverage); err != nil {
		return nil, err
	}

	// Hyperliquid symbol格式
	coin := convertSymbolToHyperliquid(symbol)

	// 获取当前价格（用于市价单）
	price, err := t.GetMarketPrice(symbol)
	if err != nil {
		return nil, err
	}

	// ⚠️ 关键：根据币种精度要求，四舍五入数量
	roundedQuantity := t.roundToSzDecimals(coin, quantity)
	log.Printf("  📏 数量精度处理: %.8f -> %.8f (szDecimals=%d)", quantity, roundedQuantity, t.getSzDecimals(coin))

	// ⚠️ 关键：价格也需要处理为5位有效数字
	aggressivePrice := t.roundPriceToSigfigs(price * 1.01)
	log.Printf("  💰 价格精度处理: %.8f -> %.8f (5位有效数字)", price*1.01, aggressivePrice)

	// 创建市价买入订单（使用IOC limit order with aggressive price）
	order := hyperliquid.CreateOrderRequest{
		Coin:  coin,
		IsBuy: true,
		Size:  roundedQuantity, // 使用四舍五入后的数量
		Price: aggressivePrice, // 使用处理后的价格
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{
				Tif: hyperliquid.TifIoc, // Immediate or Cancel (类似市价单)
			},
		},
		ReduceOnly: false,
	}

	_, err = t.exchange.Order(t.ctx, order, nil)
	if err != nil {
		return nil, fmt.Errorf("开多仓失败: %w", err)
	}

	log.Printf("✓ 开多仓成功: %s 数量: %.4f", symbol, roundedQuantity)

	result := make(map[string]interface{})
	result["orderId"] = 0 // Hyperliquid没有返回order ID
	result["symbol"] = symbol
	result["status"] = "FILLED"

	return result, nil
}

// OpenShort 开空仓
func (t *HyperliquidTrader) OpenShort(symbol string, quantity float64, leverage int) (map[string]interface{}, error) {
	// 先取消该币种的所有委托单
	if err := t.CancelAllOrders(symbol); err != nil {
		log.Printf("  ⚠ 取消旧委托单失败: %v", err)
	}

	// 设置杠杆
	if err := t.SetLeverage(symbol, leverage); err != nil {
		return nil, err
	}

	// Hyperliquid symbol格式
	coin := convertSymbolToHyperliquid(symbol)

	// 获取当前价格
	price, err := t.GetMarketPrice(symbol)
	if err != nil {
		return nil, err
	}

	// ⚠️ 关键：根据币种精度要求，四舍五入数量
	roundedQuantity := t.roundToSzDecimals(coin, quantity)
	log.Printf("  📏 数量精度处理: %.8f -> %.8f (szDecimals=%d)", quantity, roundedQuantity, t.getSzDecimals(coin))

	// ⚠️ 关键：价格也需要处理为5位有效数字
	aggressivePrice := t.roundPriceToSigfigs(price * 0.99)
	log.Printf("  💰 价格精度处理: %.8f -> %.8f (5位有效数字)", price*0.99, aggressivePrice)

	// 创建市价卖出订单
	order := hyperliquid.CreateOrderRequest{
		Coin:  coin,
		IsBuy: false,
		Size:  roundedQuantity, // 使用四舍五入后的数量
		Price: aggressivePrice, // 使用处理后的价格
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{
				Tif: hyperliquid.TifIoc,
			},
		},
		ReduceOnly: false,
	}

	_, err = t.exchange.Order(t.ctx, order, nil)
	if err != nil {
		return nil, fmt.Errorf("开空仓失败: %w", err)
	}

	log.Printf("✓ 开空仓成功: %s 数量: %.4f", symbol, roundedQuantity)

	result := make(map[string]interface{})
	result["orderId"] = 0
	result["symbol"] = symbol
	result["status"] = "FILLED"

	return result, nil
}

// CloseLong 平多仓
func (t *HyperliquidTrader) CloseLong(symbol string, quantity float64) (map[string]interface{}, error) {
	// 如果数量为0，获取当前持仓数量
	if quantity == 0 {
		positions, err := t.GetPositions()
		if err != nil {
			return nil, err
		}

		for _, pos := range positions {
			if pos["symbol"] == symbol && pos["side"] == "long" {
				quantity = pos["positionAmt"].(float64)
				break
			}
		}

		if quantity == 0 {
			return nil, fmt.Errorf("没有找到 %s 的多仓", symbol)
		}
	}

	// Hyperliquid symbol格式
	coin := convertSymbolToHyperliquid(symbol)

	// 获取当前价格
	price, err := t.GetMarketPrice(symbol)
	if err != nil {
		return nil, err
	}

	// ⚠️ 关键：根据币种精度要求，四舍五入数量
	roundedQuantity := t.roundToSzDecimals(coin, quantity)
	log.Printf("  📏 数量精度处理: %.8f -> %.8f (szDecimals=%d)", quantity, roundedQuantity, t.getSzDecimals(coin))

	// ⚠️ 关键：价格也需要处理为5位有效数字
	aggressivePrice := t.roundPriceToSigfigs(price * 0.99)
	log.Printf("  💰 价格精度处理: %.8f -> %.8f (5位有效数字)", price*0.99, aggressivePrice)

	// 创建平仓订单（卖出 + ReduceOnly）
	order := hyperliquid.CreateOrderRequest{
		Coin:  coin,
		IsBuy: false,
		Size:  roundedQuantity, // 使用四舍五入后的数量
		Price: aggressivePrice, // 使用处理后的价格
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{
				Tif: hyperliquid.TifIoc,
			},
		},
		ReduceOnly: true, // 只平仓，不开新仓
	}

	_, err = t.exchange.Order(t.ctx, order, nil)
	if err != nil {
		return nil, fmt.Errorf("平多仓失败: %w", err)
	}

	log.Printf("✓ 平多仓成功: %s 数量: %.4f", symbol, roundedQuantity)

	// 平仓后取消该币种的所有挂单
	if err := t.CancelAllOrders(symbol); err != nil {
		log.Printf("  ⚠ 取消挂单失败: %v", err)
	}

	result := make(map[string]interface{})
	result["orderId"] = 0
	result["symbol"] = symbol
	result["status"] = "FILLED"

	return result, nil
}

// CloseShort 平空仓
func (t *HyperliquidTrader) CloseShort(symbol string, quantity float64) (map[string]interface{}, error) {
	// 如果数量为0，获取当前持仓数量
	if quantity == 0 {
		positions, err := t.GetPositions()
		if err != nil {
			return nil, err
		}

		for _, pos := range positions {
			if pos["symbol"] == symbol && pos["side"] == "short" {
				quantity = pos["positionAmt"].(float64)
				break
			}
		}

		if quantity == 0 {
			return nil, fmt.Errorf("没有找到 %s 的空仓", symbol)
		}
	}

	// Hyperliquid symbol格式
	coin := convertSymbolToHyperliquid(symbol)

	// 获取当前价格
	price, err := t.GetMarketPrice(symbol)
	if err != nil {
		return nil, err
	}

	// ⚠️ 关键：根据币种精度要求，四舍五入数量
	roundedQuantity := t.roundToSzDecimals(coin, quantity)
	log.Printf("  📏 数量精度处理: %.8f -> %.8f (szDecimals=%d)", quantity, roundedQuantity, t.getSzDecimals(coin))

	// ⚠️ 关键：价格也需要处理为5位有效数字
	aggressivePrice := t.roundPriceToSigfigs(price * 1.01)
	log.Printf("  💰 价格精度处理: %.8f -> %.8f (5位有效数字)", price*1.01, aggressivePrice)

	// 创建平仓订单（买入 + ReduceOnly）
	order := hyperliquid.CreateOrderRequest{
		Coin:  coin,
		IsBuy: true,
		Size:  roundedQuantity, // 使用四舍五入后的数量
		Price: aggressivePrice, // 使用处理后的价格
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{
				Tif: hyperliquid.TifIoc,
			},
		},
		ReduceOnly: true,
	}

	_, err = t.exchange.Order(t.ctx, order, nil)
	if err != nil {
		return nil, fmt.Errorf("平空仓失败: %w", err)
	}

	log.Printf("✓ 平空仓成功: %s 数量: %.4f", symbol, roundedQuantity)

	// 平仓后取消该币种的所有挂单
	if err := t.CancelAllOrders(symbol); err != nil {
		log.Printf("  ⚠ 取消挂单失败: %v", err)
	}

	result := make(map[string]interface{})
	result["orderId"] = 0
	result["symbol"] = symbol
	result["status"] = "FILLED"

	return result, nil
}

// CancelStopOrders 取消该币种的止盈/止

// CancelStopLossOrders 仅取消止损单（Hyperliquid 暂无法区分止损和止盈，取消所有）
func (t *HyperliquidTrader) CancelStopLossOrders(symbol string) error {
	// Hyperliquid SDK 的 OpenOrder 结构不暴露 trigger 字段
	// 无法区分止损和止盈单，因此取消该币种的所有挂单
	log.Printf("  ⚠️ Hyperliquid 无法区分止损/止盈单，将取消所有挂单")
	return t.CancelStopOrders(symbol)
}

// CancelTakeProfitOrders 仅取消止盈单（Hyperliquid 暂无法区分止损和止盈，取消所有）
func (t *HyperliquidTrader) CancelTakeProfitOrders(symbol string) error {
	// Hyperliquid SDK 的 OpenOrder 结构不暴露 trigger 字段
	// 无法区分止损和止盈单，因此取消该币种的所有挂单
	log.Printf("  ⚠️ Hyperliquid 无法区分止损/止盈单，将取消所有挂单")
	return t.CancelStopOrders(symbol)
}

// CancelAllOrders 取消该币种的所有挂单
func (t *HyperliquidTrader) CancelAllOrders(symbol string) error {
	coin := convertSymbolToHyperliquid(symbol)

	// 获取所有挂单
	openOrders, err := t.exchange.Info().OpenOrders(t.ctx, t.walletAddr)
	if err != nil {
		return fmt.Errorf("获取挂单失败: %w", err)
	}

	// 取消该币种的所有挂单
	for _, order := range openOrders {
		if order.Coin == coin {
			_, err := t.exchange.Cancel(t.ctx, coin, order.Oid)
			if err != nil {
				log.Printf("  ⚠ 取消订单失败 (oid=%d): %v", order.Oid, err)
			}
		}
	}

	log.Printf("  ✓ 已取消 %s 的所有挂单", symbol)
	return nil
}

// CancelStopOrders 取消该币种的止盈/止损单（用于调整止盈止损位置）
func (t *HyperliquidTrader) CancelStopOrders(symbol string) error {
	coin := convertSymbolToHyperliquid(symbol)

	// 获取所有挂单
	openOrders, err := t.exchange.Info().OpenOrders(t.ctx, t.walletAddr)
	if err != nil {
		return fmt.Errorf("获取挂单失败: %w", err)
	}

	// 注意：Hyperliquid SDK 的 OpenOrder 结构不暴露 trigger 字段
	// 因此暂时取消该币种的所有挂单（包括止盈止损单）
	// 这是安全的，因为在设置新的止盈止损之前，应该清理所有旧订单
	canceledCount := 0
	for _, order := range openOrders {
		if order.Coin == coin {
			_, err := t.exchange.Cancel(t.ctx, coin, order.Oid)
			if err != nil {
				log.Printf("  ⚠ 取消订单失败 (oid=%d): %v", order.Oid, err)
				continue
			}
			canceledCount++
		}
	}

	if canceledCount == 0 {
		log.Printf("  ℹ %s 没有挂单需要取消", symbol)
	} else {
		log.Printf("  ✓ 已取消 %s 的 %d 个挂单（包括止盈/止损单）", symbol, canceledCount)
	}

	return nil
}

// GetMarketPrice 获取市场价格
func (t *HyperliquidTrader) GetMarketPrice(symbol string) (float64, error) {
	coin := convertSymbolToHyperliquid(symbol)

	// 获取所有市场价格
	allMids, err := t.exchange.Info().AllMids(t.ctx)
	if err != nil {
		return 0, fmt.Errorf("获取价格失败: %w", err)
	}

	// 查找对应币种的价格（allMids是map[string]string）
	if priceStr, ok := allMids[coin]; ok {
		priceFloat, err := strconv.ParseFloat(priceStr, 64)
		if err == nil {
			return priceFloat, nil
		}
		return 0, fmt.Errorf("价格格式错误: %v", err)
	}

	return 0, fmt.Errorf("未找到 %s 的价格", symbol)
}

// SetStopLoss 设置止损单
func (t *HyperliquidTrader) SetStopLoss(symbol string, positionSide string, quantity, stopPrice float64) error {
	coin := convertSymbolToHyperliquid(symbol)

	isBuy := positionSide == "SHORT" // 空仓止损=买入，多仓止损=卖出

	// ⚠️ 关键：根据币种精度要求，四舍五入数量
	roundedQuantity := t.roundToSzDecimals(coin, quantity)

	// ⚠️ 关键：价格也需要处理为5位有效数字
	roundedStopPrice := t.roundPriceToSigfigs(stopPrice)

	// 创建止损单（Trigger Order）
	order := hyperliquid.CreateOrderRequest{
		Coin:  coin,
		IsBuy: isBuy,
		Size:  roundedQuantity,  // 使用四舍五入后的数量
		Price: roundedStopPrice, // 使用处理后的价格
		OrderType: hyperliquid.OrderType{
			Trigger: &hyperliquid.TriggerOrderType{
				TriggerPx: roundedStopPrice,
				IsMarket:  true,
				Tpsl:      "sl", // stop loss
			},
		},
		ReduceOnly: true,
	}

	_, err := t.exchange.Order(t.ctx, order, nil)
	if err != nil {
		return fmt.Errorf("设置止损失败: %w", err)
	}

	log.Printf("  止损价设置: %.4f", roundedStopPrice)
	return nil
}

// SetTakeProfit 设置止盈单
func (t *HyperliquidTrader) SetTakeProfit(symbol string, positionSide string, quantity, takeProfitPrice float64) error {
	coin := convertSymbolToHyperliquid(symbol)

	isBuy := positionSide == "SHORT" // 空仓止盈=买入，多仓止盈=卖出

	// ⚠️ 关键：根据币种精度要求，四舍五入数量
	roundedQuantity := t.roundToSzDecimals(coin, quantity)

	// ⚠️ 关键：价格也需要处理为5位有效数字
	roundedTakeProfitPrice := t.roundPriceToSigfigs(takeProfitPrice)

	// 使用 Trigger 订单设置止盈
	order := hyperliquid.CreateOrderRequest{
		Coin:  coin,
		IsBuy: isBuy,
		Size:  roundedQuantity,        // 使用四舍五入后的数量
		Price: roundedTakeProfitPrice, // 兼容性保留
		OrderType: hyperliquid.OrderType{
			Trigger: &hyperliquid.TriggerOrderType{
				TriggerPx: roundedTakeProfitPrice,
				IsMarket:  true,
				Tpsl:      "tp", // take profit
			},
		},
		ReduceOnly: true,
	}

	_, err := t.exchange.Order(t.ctx, order, nil)
	if err != nil {
		return fmt.Errorf("设置止盈失败: %w", err)
	}

	log.Printf("  止盈价设置: %.4f", roundedTakeProfitPrice)
	return nil
}

// FormatQuantity 格式化数量到正确的精度
func (t *HyperliquidTrader) FormatQuantity(symbol string, quantity float64) (string, error) {
	coin := convertSymbolToHyperliquid(symbol)
	szDecimals := t.getSzDecimals(coin)

	// 使用szDecimals格式化数量
	formatStr := fmt.Sprintf("%%.%df", szDecimals)
	return fmt.Sprintf(formatStr, quantity), nil
}

// getSzDecimals 获取币种的数量精度
func (t *HyperliquidTrader) getSzDecimals(coin string) int {
	if t.meta == nil {
		log.Printf("⚠️  meta信息为空，使用默认精度4")
		return 4 // 默认精度
	}

	// 在meta.Universe中查找对应的币种
	for _, asset := range t.meta.Universe {
		if asset.Name == coin {
			return asset.SzDecimals
		}
	}

	log.Printf("⚠️  未找到 %s 的精度信息，使用默认精度4", coin)
	return 4 // 默认精度
}

// roundToSzDecimals 将数量四舍五入到正确的精度
func (t *HyperliquidTrader) roundToSzDecimals(coin string, quantity float64) float64 {
	szDecimals := t.getSzDecimals(coin)

	// 计算倍数（10^szDecimals）
	multiplier := 1.0
	for i := 0; i < szDecimals; i++ {
		multiplier *= 10.0
	}

	// 四舍五入
	return float64(int(quantity*multiplier+0.5)) / multiplier
}

// roundPriceToSigfigs 将价格四舍五入到5位有效数字
// Hyperliquid要求价格使用5位有效数字（significant figures）
func (t *HyperliquidTrader) roundPriceToSigfigs(price float64) float64 {
	if price == 0 {
		return 0
	}

	const sigfigs = 5 // Hyperliquid标准：5位有效数字

	// 计算价格的数量级
	var magnitude float64
	if price < 0 {
		magnitude = -price
	} else {
		magnitude = price
	}

	// 计算需要的倍数
	multiplier := 1.0
	for magnitude >= 10 {
		magnitude /= 10
		multiplier /= 10
	}
	for magnitude < 1 {
		magnitude *= 10
		multiplier *= 10
	}

	// 应用有效数字精度
	for i := 0; i < sigfigs-1; i++ {
		multiplier *= 10
	}

	// 四舍五入
	rounded := float64(int(price*multiplier+0.5)) / multiplier
	return rounded
}

// convertSymbolToHyperliquid 将标准symbol转换为Hyperliquid格式
// 例如: "BTCUSDT" -> "BTC"
func convertSymbolToHyperliquid(symbol string) string {
	// 去掉USDT后缀
	if len(symbol) > 4 && symbol[len(symbol)-4:] == "USDT" {
		return symbol[:len(symbol)-4]
	}
	return symbol
}

// parsePositionSzi 解析持仓数量字符串，增强错误处理
func (t *HyperliquidTrader) parsePositionSzi(szi string) (float64, error) {
	// 去除空格和特殊字符
	szi = strings.TrimSpace(szi)
	if szi == "" || szi == "0" {
		return 0, nil
	}

	// 处理可能的科学计数法
	posAmt, err := strconv.ParseFloat(szi, 64)
	if err != nil {
		log.Printf("⚠️ 解析持仓数量失败: %s, 错误: %v", szi, err)
		return 0, fmt.Errorf("解析持仓数量失败: %w", err)
	}

	return posAmt, nil
}

// absFloat 返回浮点数的绝对值
func absFloat(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
