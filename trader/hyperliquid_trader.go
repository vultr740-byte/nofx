package trader

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/sonirico/go-hyperliquid"
)

type perpMetaResponse struct {
	Universe []struct {
		Name string `json:"name"`
	} `json:"universe"`
}

// 轻量化的 PerpMeta/Asset 结构用于解析 allPerpMetas
type PerpMetaLite struct {
	Universe []PerpMetaAssetLite `json:"universe"`
}

type PerpMetaAssetLite struct {
	Name       string `json:"name"`
	SzDecimals int    `json:"szDecimals"`
	PxDecimals *int   `json:"pxDecimals"`
}

// normalizeHip3Symbol 确保HIP-3符号前缀小写、后缀大写（如 xyz:TSLA）
func normalizeHip3Symbol(symbol string) string {
	if !strings.Contains(symbol, ":") {
		return symbol
	}
	parts := strings.SplitN(symbol, ":", 2)
	prefix := strings.ToLower(strings.TrimSpace(parts[0]))
	suffix := strings.ToUpper(strings.TrimSpace(parts[1]))
	return prefix + ":" + suffix
}

// infoAPIURL 根据网络返回 Info API 地址
func infoAPIURL(testnet bool) string {
	if testnet {
		return "https://api.hyperliquid-testnet.xyz/info"
	}
	return "https://api.hyperliquid.xyz/info"
}

// fetchPriceFromInfoAPI 调用 Info API allMids 获取价格（用于AllMids缺失时兜底）
func (t *HyperliquidTrader) fetchPriceFromInfoAPI(coin string) (float64, error) {
	coin = normalizeHip3Symbol(coin)
	payload := []byte(`{"type":"allMids"}`)
	req, err := http.NewRequest("POST", infoAPIURL(t.testnet), bytes.NewBuffer(payload))
	if err != nil {
		return 0, fmt.Errorf("创建 allMids 请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "NOFX-Hyperliquid-Price")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("调用 allMids 失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("读取 allMids 响应失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("allMids 返回状态码 %d: %s", resp.StatusCode, string(body))
	}

	var mids map[string]string
	if err := json.Unmarshal(body, &mids); err != nil {
		return 0, fmt.Errorf("解析 allMids 响应失败: %w", err)
	}

	if priceStr, ok := mids[coin]; ok {
		price, err := strconv.ParseFloat(priceStr, 64)
		if err != nil {
			return 0, fmt.Errorf("价格格式错误: %v", err)
		}
		return price, nil
	}

	return 0, fmt.Errorf("allMids 未找到价格: %s", coin)
}

// fetchPriceFromRecentTrades 调用 Info API recentTrades 获取最新成交价（用于HIP-3等特殊资产）
func (t *HyperliquidTrader) fetchPriceFromRecentTrades(coin string) (float64, error) {
	coin = normalizeHip3Symbol(coin)
	payload := []byte(fmt.Sprintf(`{"type":"recentTrades","coin":%q}`, coin))
	req, err := http.NewRequest("POST", infoAPIURL(t.testnet), bytes.NewBuffer(payload))
	if err != nil {
		return 0, fmt.Errorf("创建 recentTrades 请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "NOFX-Hyperliquid-RecentTrades")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("调用 recentTrades 失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("读取 recentTrades 响应失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("recentTrades 返回状态码 %d: %s", resp.StatusCode, string(body))
	}

	// 响应为数组，元素字段可能是 px 或 price
	var trades []map[string]interface{}
	if err := json.Unmarshal(body, &trades); err != nil {
		return 0, fmt.Errorf("解析 recentTrades 响应失败: %w", err)
	}
	if len(trades) == 0 {
		return 0, fmt.Errorf("recentTrades 返回空结果")
	}

	// 尝试解析 px 或 price
	parsePrice := func(v interface{}) (float64, bool) {
		switch val := v.(type) {
		case string:
			if f, err := strconv.ParseFloat(val, 64); err == nil {
				return f, true
			}
		case float64:
			return val, true
		}
		return 0, false
	}

	if f, ok := parsePrice(trades[0]["px"]); ok {
		return f, nil
	}
	if f, ok := parsePrice(trades[0]["price"]); ok {
		return f, nil
	}

	// 部分节点返回 pricePx
	if f, ok := parsePrice(trades[0]["pricePx"]); ok {
		return f, nil
	}

	return 0, fmt.Errorf("recentTrades 未找到价格字段")
}

// fetchPerpMetaAsset 通过 allPerpMetas 获取资产名和精度（支持主网/测试网切换）
// 使用轻量结构体避免依赖 SDK 内部类型
func (t *HyperliquidTrader) fetchPerpMetaAsset(coin string, forceMainnet bool) (string, *PerpMetaAssetLite, error) {
	coin = normalizeHip3Symbol(coin)
	payload := []byte(`{"type":"allPerpMetas"}`)
	endpoint := infoAPIURL(t.testnet)
	if forceMainnet {
		endpoint = infoAPIURL(false)
	}

	req, err := http.NewRequest("POST", endpoint, bytes.NewBuffer(payload))
	if err != nil {
		return "", nil, fmt.Errorf("创建 InfoAPI 请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "NOFX-Hyperliquid-Resolve")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("调用 InfoAPI 失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, fmt.Errorf("读取 InfoAPI 响应失败: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("InfoAPI 返回错误状态码 %d: %s", resp.StatusCode, string(body))
	}

	var metas []PerpMetaLite
	if err := json.Unmarshal(body, &metas); err != nil {
		return "", nil, fmt.Errorf("解析 InfoAPI 响应失败: %w", err)
	}

	log.Printf("🔍 [HIP-3 API] 处理 allPerpMetas 响应，共 %d 个 meta 块", len(metas))
	for _, meta := range metas {
		log.Printf("🔍 [HIP-3 API] 处理 meta 块，包含 %d 个资产", len(meta.Universe))
		for _, asset := range meta.Universe {
			name := normalizeHip3Symbol(asset.Name)
			if !strings.Contains(name, ":") {
				continue
			}
			parts := strings.SplitN(name, ":", 2)
			if len(parts) != 2 {
				continue
			}
			if strings.EqualFold(parts[1], coin) {
				// 详细日志记录 API 返回的两个精度值
				if asset.PxDecimals != nil {
					log.Printf("🔍 [HIP-3 API] %s - SzDecimals: %d, PxDecimals: %d",
							   name, asset.SzDecimals, *asset.PxDecimals)
				} else {
					log.Printf("🔍 [HIP-3 API] %s - SzDecimals: %d, PxDecimals: nil (将使用SzDecimals作为价格精度)",
							   name, asset.SzDecimals)
				}
				t.hip3Meta[name] = asset
				return name, &asset, nil
			}
		}
	}

	return "", nil, fmt.Errorf("InfoAPI 未找到交易对: %s", coin)
}

// ResolveNonCryptoSymbol 使用 Info API (allPerpMetas) 为非加密资产获取带前缀的HIP-3符号
// preferMainnet: 强制使用主网 Info API（与 /stocks 列表一致）
func (t *HyperliquidTrader) ResolveNonCryptoSymbol(symbol string, preferMainnet bool) (string, error) {
	base := convertSymbolToHyperliquid(strings.ToUpper(symbol))
	// 直传冒号格式则直接返回
	if strings.Contains(base, ":") {
		norm := normalizeHip3Symbol(base)
		return norm, nil
	}

	mapped, asset, err := t.fetchPerpMetaAsset(base, preferMainnet)
	if err != nil {
		return "", err
	}
	if asset != nil {
		t.hip3Meta[normalizeHip3Symbol(mapped)] = *asset
	}
	return normalizeHip3Symbol(mapped), nil
}

// resolveFromInfoAPI 使用 Info API 的 allPerpMetas 兜底匹配 HIP-3 股票（与 /stocks 列表一致）
// forceMainnet: 为非加密资产匹配时可强制使用主网 Info API（与 /stocks 保持一致）
func (t *HyperliquidTrader) resolveFromInfoAPI(coin string, forceMainnet bool) (string, error) {
	mapped, _, err := t.fetchPerpMetaAsset(coin, forceMainnet)
	return mapped, err
}

// GetRecentTradePrice 使用 recentTrades 接口获取最新成交价（适用于 HIP-3 股票等非加密资产）
func (t *HyperliquidTrader) GetRecentTradePrice(coin string) (float64, error) {
	return t.fetchPriceFromRecentTrades(coin)
}

// HyperliquidTrader Hyperliquid交易器
type HyperliquidTrader struct {
	exchange         *hyperliquid.Exchange
	ctx              context.Context
	walletAddr       string
	meta             *hyperliquid.Meta // 缓存meta信息（包含精度等）
	testnet          bool              // 当前是否为测试网
	hip3Meta         map[string]PerpMetaAssetLite
	isCrossMargin    bool // 是否为全仓模式
	orderMu          sync.Mutex
	stopLossOrders   map[string]orderRef // symbol -> 最近一次止损挂单
	takeProfitOrders map[string]orderRef // symbol -> 最近一次止盈挂单
}

type orderRef struct {
	oid   int64
	cloid string
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
		exchange:         exchange,
		ctx:              ctx,
		walletAddr:       walletAddr,
		meta:             meta,
		hip3Meta:         make(map[string]PerpMetaAssetLite),
		testnet:          testnet,
		isCrossMargin:    true, // 默认使用全仓模式
		stopLossOrders:   make(map[string]orderRef),
		takeProfitOrders: make(map[string]orderRef),
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

	// ✅ Step 3: 总资产使用 MarginSummary 的 accountValue（包含占用保证金）
	var accountValue, totalMarginUsed, totalNtlPos float64
	var summaryType string
	var summary interface{}

	accountValue, _ = strconv.ParseFloat(accountState.MarginSummary.AccountValue, 64)
	totalMarginUsed, _ = strconv.ParseFloat(accountState.MarginSummary.TotalMarginUsed, 64)
	totalNtlPos, _ = strconv.ParseFloat(accountState.MarginSummary.TotalNtlPos, 64)
	summaryType = "MarginSummary (默认对齐 JS)"
	summary = accountState.MarginSummary

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

	// ✅ Step 4: 可用余额直接使用 Withdrawable 字段
	availableBalance := 0.0

	if accountState.Withdrawable != "" {
		withdrawable, err := strconv.ParseFloat(accountState.Withdrawable, 64)
		if err == nil {
			availableBalance = withdrawable
			log.Printf("✓ 使用 Withdrawable 字段: %.2f USDC", availableBalance)
		} else {
			log.Printf("⚠️ Withdrawable 字段解析失败: %v", err)
			availableBalance = 0
		}
	} else {
		log.Printf("⚠️ Withdrawable 字段为空")
		availableBalance = 0
	}

	// ✅ Step 5: 采用 JavaScript 计算方式
	// 直接使用 accountValue 作为总资产，避免重复计算现货余额
	totalWalletBalance := accountValue

	result["totalWalletBalance"] = totalWalletBalance    // 总资产（使用 accountValue）
	result["availableBalance"] = availableBalance        // 可用余额（Withdrawable 字段）
	result["totalUnrealizedProfit"] = totalUnrealizedPnl // 未实现盈亏（仅来自 Perpetuals）
	result["spotBalance"] = spotUSDCBalance              // Spot 现货余额（单独返回）
	result["totalMarginUsed"] = totalMarginUsed          // 占用保证金
	result["totalPosition"] = totalNtlPos                // 总持仓名义价值

	// 增强的调试日志：显示完整的余额字段映射
	log.Printf("🔍 [DEBUG] Hyperliquid 余额字段详情 (JavaScript 方式):")
	log.Printf("  • AccountValue (总资产): %.2f USDC", accountValue)
	log.Printf("  • Withdrawable (可提现): %.2f USDC", availableBalance)
	log.Printf("  • TotalMarginUsed (占用保证金): %.2f USDC", totalMarginUsed)
	log.Printf("  • SpotUSDCBalance (现货余额): %.2f USDC", spotUSDCBalance)
	log.Printf("  • TotalUnrealizedPnL (未实现盈亏): %.2f USDC", totalUnrealizedPnl)
	log.Printf("  • TotalNtlPos (总持仓): %.2f USDC", totalNtlPos)
	log.Printf("")
	log.Printf("✅ JavaScript 计算方式:")
	log.Printf("  • 总资产 = AccountValue = %.2f USDC", totalWalletBalance)
	log.Printf("  • 现货余额单独展示: %.2f USDC", spotUSDCBalance)
	log.Printf("")
	log.Printf("💰 账户总览:")
	log.Printf("  • 总资产 (AccountValue): %.2f USDC", totalWalletBalance)
	log.Printf("  • 可用余额 (Withdrawable): %.2f USDC", availableBalance)
	log.Printf("  • 现货余额 (Spot): %.2f USDC", spotUSDCBalance)
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

	// 预先获取触发类挂单，用于止盈/止损信息
	frontendOrders, err := t.exchange.Info().FrontendOpenOrders(t.ctx, t.walletAddr)
	if err != nil {
		log.Printf("⚠️ 获取前端挂单失败，止盈止损信息将缺失: %v", err)
		frontendOrders = nil
	}

	// 额外获取普通挂单，用于兜底（部分 reduce-only 限价单没有触发标记）
	openOrders, err := t.exchange.Info().OpenOrders(t.ctx, t.walletAddr)
	if err != nil {
		log.Printf("⚠️ 获取 OpenOrders 失败，无法兜底识别 reduce-only 限价单: %v", err)
		openOrders = nil
	}

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

		// 匹配止盈/止损触发单（使用 Hyperliquid 前端字段判断类型）
		var stopLossPx, takeProfitPx float64
		positionSide := "LONG"
		if posAmt < 0 {
			positionSide = "SHORT"
		}
		for _, ord := range frontendOrders {
			if !strings.EqualFold(ord.Coin, position.Coin) {
				continue
			}
			// 只看触发类或显式标记为TP/SL的委托
			if !(ord.IsTrigger || ord.IsPositionTpSl) {
				continue
			}
			triggerPx := ord.TriggerPx
			if triggerPx <= 0 {
				continue
			}

			orderKind := detectOrderKind(ord, positionSide)

			// 新增兜底：部分触发单（尤其是 reduce-only）可能没有TP/SL标记
			if orderKind == "" && triggerPx > 0 && (ord.ReduceOnly || ord.IsTrigger || ord.IsPositionTpSl) {
				log.Printf("📋 订单 OID=%d 无法直接识别，使用价格启发式 (OrderType='%s', IsPositionTpSl=%t, TriggerCondition='%s', ReduceOnly=%t)",
					ord.Oid, ord.OrderType, ord.IsPositionTpSl, ord.TriggerCondition, ord.ReduceOnly)
				if kind := t.classifyOrderByPriceHeuristic(ord, positionSide, triggerPx); kind != "" {
					orderKind = kind
					log.Printf("✅ 订单 OID=%d 启发式分类成功: %s", ord.Oid, orderKind)
				}
			}

			if orderKind == "" {
				log.Printf("📋 跳过订单 OID=%d: 无法确定订单类型 (OrderType='%s', IsPositionTpSl=%t, TriggerCondition='%s', ReduceOnly=%t)",
					ord.Oid, ord.OrderType, ord.IsPositionTpSl, ord.TriggerCondition, ord.ReduceOnly)
				continue
			}

			// 对于无法确定类型的订单，使用启发式方法判断
			if orderKind == "unknown" {
				log.Printf("📋 订单 OID=%d 需要启发式判断 (OrderType='%s', TriggerCondition='%s')",
					ord.Oid, ord.OrderType, ord.TriggerCondition)
				if kind := t.classifyOrderByPriceHeuristic(ord, positionSide, triggerPx); kind != "" {
					orderKind = kind
					log.Printf("✅ 订单 OID=%d 启发式判断成功: %s", ord.Oid, orderKind)
				} else {
					log.Printf("❌ 订单 OID=%d 启发式判断失败，跳过", ord.Oid)
					continue // 启发式判断也失败，跳过
				}
			} else {
				log.Printf("📋 订单 OID=%d 直接识别: %s (OrderType='%s')", ord.Oid, orderKind, ord.OrderType)
			}

			if positionSide == "LONG" {
				if orderKind == "sl" {
					// price向下触发，取最靠近当前价格的最高触发价
					if stopLossPx == 0 || triggerPx > stopLossPx {
						stopLossPx = triggerPx
					}
				} else {
					// take profit 向上触发，取最靠近当前价格的最低触发价
					if takeProfitPx == 0 || triggerPx < takeProfitPx {
						takeProfitPx = triggerPx
					}
				}
			} else { // SHORT
				if orderKind == "sl" {
					// price向上触发，取最靠近当前价格的最低触发价
					if stopLossPx == 0 || triggerPx < stopLossPx {
						stopLossPx = triggerPx
					}
				} else {
					// take profit 向下触发，取最靠近当前价格的最高触发价
					if takeProfitPx == 0 || triggerPx > takeProfitPx {
						takeProfitPx = triggerPx
					}
				}
			}
		}
		if stopLossPx > 0 {
			posMap["stopLoss"] = stopLossPx
		}
		if takeProfitPx > 0 {
			posMap["takeProfit"] = takeProfitPx
		}

		// 🔄 兜底：如果仍未识别出止盈/止损，再尝试根据 reduce-only 限价挂单推断
		if (stopLossPx == 0 || takeProfitPx == 0) && len(openOrders) > 0 {
			refPx := entryPrice
			if refPx == 0 {
				refPx = markPrice
			}

			bestSL := stopLossPx
			bestTP := takeProfitPx

			for _, ord := range openOrders {
				if !strings.EqualFold(ord.Coin, position.Coin) {
					continue
				}
				side := strings.ToUpper(ord.Side)

				// 判断是否为减少仓位方向的单子（无 reduceOnly 字段时的近似判断）
				isClosing := (posAmt > 0 && side == "A") || (posAmt < 0 && side == "B")
				if !isClosing {
					continue
				}

				px := ord.LimitPx
				if px <= 0 {
					continue
				}

				if posAmt > 0 { // LONG: 卖出平仓
					if px < refPx { // 更低价格 -> 更像止损，取最接近参考价的最高价
						if bestSL == 0 || px > bestSL {
							bestSL = px
						}
					} else if px > refPx { // 更高价格 -> 更像止盈，取最接近的最低价
						if bestTP == 0 || px < bestTP {
							bestTP = px
						}
					}
				} else { // SHORT: 买入平仓
					if px > refPx { // 更高价格 -> 止损，取最接近的最低价
						if bestSL == 0 || px < bestSL {
							bestSL = px
						}
					} else if px < refPx { // 更低价格 -> 止盈，取最接近的最高价
						if bestTP == 0 || px > bestTP {
							bestTP = px
						}
					}
				}
			}

			if stopLossPx == 0 && bestSL > 0 {
				stopLossPx = bestSL
				posMap["stopLoss"] = stopLossPx
				log.Printf("✅ %s 通过限价挂单兜底识别到止损价: %.4f", symbol, stopLossPx)
			}
			if takeProfitPx == 0 && bestTP > 0 {
				takeProfitPx = bestTP
				posMap["takeProfit"] = takeProfitPx
				log.Printf("✅ %s 通过限价挂单兜底识别到止盈价: %.4f", symbol, takeProfitPx)
			}
		}

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
	coin, err := t.resolveCoin(symbol)
	if err != nil {
		return err
	}

	// 调用UpdateLeverage (leverage int, name string, isCross bool)
	// 第三个参数: true=全仓模式, false=逐仓模式
	_, err = t.exchange.UpdateLeverage(t.ctx, leverage, coin, t.isCrossMargin)
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
	coin, err := t.resolveCoin(symbol)
	if err != nil {
		return nil, err
	}

	// 获取当前价格（用于市价单）
	price, err := t.GetMarketPrice(symbol)
	if err != nil {
		return nil, err
	}

	// ⚠️ 关键：根据币种精度要求，四舍五入数量
	roundedQuantity := t.roundToSzDecimals(coin, quantity)
	log.Printf("  📏 数量精度处理: %.8f -> %.8f (szDecimals=%d)", quantity, roundedQuantity, t.getSzDecimals(coin))

	// ⚠️ 关键：价格精度处理（优先使用pxDecimals，按要求截断到步长）
	var aggressivePrice float64
	var priceMultiplier float64

	// 根据资产类型调整激进定价策略
	if t.isStockAsset(coin) {
		priceMultiplier = 1.02 // 股票使用2%溢价（更保守以避免价格验证失败）
		log.Printf("🎯 [HIP-3] 股票资产使用保守定价策略: 1.02倍 (2%溢价)")
	} else {
		priceMultiplier = 1.01 // 加密货币使用原策略
		log.Printf("📈 [HIP-3] 加密货币使用标准定价策略: 1.01倍")
	}

	aggressivePrice = t.roundPriceForCoin(coin, price*priceMultiplier, true)
	log.Printf("  💰 价格精度处理: %.8f * %.3f -> %.8f -> %.8f", price, priceMultiplier, price*priceMultiplier, aggressivePrice)

	// 价格预验证
	if err := t.validateOrderPrice(coin, aggressivePrice, true); err != nil {
		return nil, fmt.Errorf("价格验证失败: %w", err)
	}

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

	// ✅ 使用带重试机制的价格执行
	if t.isStockAsset(coin) {
		log.Printf("🔄 [HIP-3] 股票资产使用重试机制: %s", coin)
		err = t.executeOrderWithRetry(&order, 3) // 最多重试3次
	} else {
		log.Printf("📈 [HIP-3] 加密货币使用标准执行: %s", coin)
		_, err = t.exchange.Order(t.ctx, order, nil)
	}

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
	coin, err := t.resolveCoin(symbol)
	if err != nil {
		return nil, err
	}

	// 获取当前价格
	price, err := t.GetMarketPrice(symbol)
	if err != nil {
		return nil, err
	}

	// ⚠️ 关键：根据币种精度要求，四舍五入数量
	roundedQuantity := t.roundToSzDecimals(coin, quantity)
	log.Printf("  📏 数量精度处理: %.8f -> %.8f (szDecimals=%d)", quantity, roundedQuantity, t.getSzDecimals(coin))

	// ⚠️ 关键：价格精度处理
	var aggressivePrice float64
	var priceMultiplier float64

	// 根据资产类型调整激进定价策略
	if t.isStockAsset(coin) {
		priceMultiplier = 0.98 // 股票使用2%折扣（更保守以避免价格验证失败）
		log.Printf("🎯 [HIP-3] 股票资产使用保守定价策略: 0.98倍 (2%折扣)")
	} else {
		priceMultiplier = 0.99 // 加密货币使用原策略
		log.Printf("📈 [HIP-3] 加密货币使用标准定价策略: 0.99倍")
	}

	aggressivePrice = t.roundPriceForCoin(coin, price*priceMultiplier, true)
	log.Printf("  💰 价格精度处理: %.8f * %.3f -> %.8f -> %.8f", price, priceMultiplier, price*priceMultiplier, aggressivePrice)

	// 价格预验证
	if err := t.validateOrderPrice(coin, aggressivePrice, false); err != nil {
		return nil, fmt.Errorf("价格验证失败: %w", err)
	}

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

	// ✅ 使用带重试机制的价格执行
	if t.isStockAsset(coin) {
		log.Printf("🔄 [HIP-3] 股票资产使用重试机制: %s", coin)
		err = t.executeOrderWithRetry(&order, 3) // 最多重试3次
	} else {
		log.Printf("📈 [HIP-3] 加密货币使用标准执行: %s", coin)
		_, err = t.exchange.Order(t.ctx, order, nil)
	}

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
	coin, err := t.resolveCoin(symbol)
	if err != nil {
		return nil, err
	}

	// 获取当前价格
	price, err := t.GetMarketPrice(symbol)
	if err != nil {
		return nil, err
	}

	// ⚠️ 关键：根据币种精度要求，四舍五入数量
	roundedQuantity := t.roundToSzDecimals(coin, quantity)
	log.Printf("  📏 数量精度处理: %.8f -> %.8f (szDecimals=%d)", quantity, roundedQuantity, t.getSzDecimals(coin))

	// ⚠️ 关键：价格也需要处理为5位有效数字
	aggressivePrice := t.roundPriceForCoin(coin, price*0.99, true)
	log.Printf("  💰 价格精度处理: %.8f -> %.8f", price*0.99, aggressivePrice)

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
	coin, err := t.resolveCoin(symbol)
	if err != nil {
		return nil, err
	}

	// 获取当前价格
	price, err := t.GetMarketPrice(symbol)
	if err != nil {
		return nil, err
	}

	// ⚠️ 关键：根据币种精度要求，四舍五入数量
	roundedQuantity := t.roundToSzDecimals(coin, quantity)
	log.Printf("  📏 数量精度处理: %.8f -> %.8f (szDecimals=%d)", quantity, roundedQuantity, t.getSzDecimals(coin))

	// ⚠️ 关键：价格也需要处理为5位有效数字
	aggressivePrice := t.roundPriceForCoin(coin, price*1.01, true)
	log.Printf("  💰 价格精度处理: %.8f -> %.8f", price*1.01, aggressivePrice)

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

// CancelStopLossOrders 仅取消止损单（实时查询并分类）
func (t *HyperliquidTrader) CancelStopLossOrders(symbol string) error {
	positionSide, err := t.getPositionSide(symbol)
	if err != nil {
		return err
	}

	triggerOrders, err := t.triggerOrdersBySymbol(symbol)
	if err != nil {
		return fmt.Errorf("获取触发挂单失败: %w", err)
	}

	if len(triggerOrders) == 0 {
		log.Printf("  ℹ %s 无触发挂单", symbol)
		return nil
	}

	log.Printf("  🔍 %s 发现 %d 个触发挂单，开始分类...", symbol, len(triggerOrders))

	coin, err := t.resolveCoin(symbol)
	if err != nil {
		return err
	}
	canceled := 0
	for _, ord := range triggerOrders {
		// 详细分类日志
		orderKind := classifyTpSl(ord, positionSide)
		log.Printf("    📋 订单 OID=%d: OrderType='%s', IsPositionTpSl=%t, TriggerCondition='%s', 分类结果='%s'",
			ord.Oid, ord.OrderType, ord.IsPositionTpSl, ord.TriggerCondition, orderKind)

		if orderKind != "sl" {
			log.Printf("      ⏭️  跳过非止损单 (%s)", orderKind)
			continue
		}

		log.Printf("      🎯 准备取消止损单 (OID=%d)", ord.Oid)
		if _, err := t.exchange.Cancel(t.ctx, coin, ord.Oid); err != nil {
			log.Printf("      ⚠ 取消止损单失败 (oid=%d): %v", ord.Oid, err)
			continue
		}
		log.Printf("      ✅ 成功取消止损单 (OID=%d)", ord.Oid)
		canceled++
	}

	if canceled == 0 {
		log.Printf("  ℹ %s 未找到可取消的止损单", symbol)
	} else {
		log.Printf("  ✓ 已取消 %s 的 %d 个止损单", symbol, canceled)
	}

	t.clearOrderRef(symbol, true)
	return nil
}

// CancelTakeProfitOrders 仅取消止盈单（实时查询并分类）
func (t *HyperliquidTrader) CancelTakeProfitOrders(symbol string) error {
	positionSide, err := t.getPositionSide(symbol)
	if err != nil {
		return err
	}

	triggerOrders, err := t.triggerOrdersBySymbol(symbol)
	if err != nil {
		return fmt.Errorf("获取触发挂单失败: %w", err)
	}

	coin, err := t.resolveCoin(symbol)
	if err != nil {
		return err
	}
	canceled := 0
	for _, ord := range triggerOrders {
		if classifyTpSl(ord, positionSide) != "tp" {
			continue
		}
		if _, err := t.exchange.Cancel(t.ctx, coin, ord.Oid); err != nil {
			log.Printf("  ⚠ 取消止盈单失败 (oid=%d): %v", ord.Oid, err)
			continue
		}
		canceled++
	}

	if canceled == 0 {
		log.Printf("  ℹ %s 未找到可取消的止盈单", symbol)
	} else {
		log.Printf("  ✓ 已取消 %s 的 %d 个止盈单", symbol, canceled)
	}

	t.clearOrderRef(symbol, false)
	return nil
}

// CancelAllOrders 取消该币种的所有挂单
func (t *HyperliquidTrader) CancelAllOrders(symbol string) error {
	coin, err := t.resolveCoin(symbol)
	if err != nil {
		return err
	}

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
	t.clearOrderRef(symbol, true)
	t.clearOrderRef(symbol, false)
	return nil
}

// CancelStopOrders 取消该币种的止盈/止损单（用于调整止盈止损位置）
func (t *HyperliquidTrader) CancelStopOrders(symbol string) error {
	coin, err := t.resolveCoin(symbol)
	if err != nil {
		return err
	}

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

	t.clearOrderRef(symbol, true)
	t.clearOrderRef(symbol, false)

	return nil
}

// cancelOrderByRef 使用已知的 oid 或 cloid 取消单个挂单
func (t *HyperliquidTrader) cancelOrderByRef(symbol string, ref orderRef) error {
	coin := convertSymbolToHyperliquid(symbol)

	if ref.cloid != "" {
		if _, err := t.exchange.CancelByCloid(t.ctx, coin, ref.cloid); err != nil {
			return err
		}
		log.Printf("  ✓ 已通过 cloid 取消 %s 的挂单 (cloid=%s)", symbol, ref.cloid)
		return nil
	}

	if ref.oid > 0 {
		if _, err := t.exchange.Cancel(t.ctx, coin, ref.oid); err != nil {
			return err
		}
		log.Printf("  ✓ 已取消 %s 的挂单 (oid=%d)", symbol, ref.oid)
		return nil
	}

	return fmt.Errorf("未找到 %s 的订单标识，无法取消", symbol)
}

// buildCloid 生成可追踪的 cloid，用于后续精准取消挂单
func (t *HyperliquidTrader) buildCloid(symbol, kind string) string {
	b := make([]byte, 16) // 16 bytes => 32 hex chars
	if _, err := rand.Read(b); err != nil {
		// 理论上不会失败，失败则退化为时间戳
		return fmt.Sprintf("%s%x", kind, time.Now().UnixNano())
	}
	hexPart := hex.EncodeToString(b)
	return "0x" + hexPart
}

func (t *HyperliquidTrader) rememberStopLossOrder(symbol string, status hyperliquid.OrderStatus, cloid string) {
	t.orderMu.Lock()
	defer t.orderMu.Unlock()
	t.stopLossOrders[symbol] = orderRef{
		oid:   extractOid(status),
		cloid: cloid,
	}
}

func (t *HyperliquidTrader) rememberTakeProfitOrder(symbol string, status hyperliquid.OrderStatus, cloid string) {
	t.orderMu.Lock()
	defer t.orderMu.Unlock()
	t.takeProfitOrders[symbol] = orderRef{
		oid:   extractOid(status),
		cloid: cloid,
	}
}

func (t *HyperliquidTrader) getOrderRef(symbol string, isStopLoss bool) (orderRef, bool) {
	t.orderMu.Lock()
	defer t.orderMu.Unlock()
	if isStopLoss {
		ref, ok := t.stopLossOrders[symbol]
		return ref, ok
	}
	ref, ok := t.takeProfitOrders[symbol]
	return ref, ok
}

func (t *HyperliquidTrader) clearOrderRef(symbol string, isStopLoss bool) {
	t.orderMu.Lock()
	defer t.orderMu.Unlock()
	if isStopLoss {
		delete(t.stopLossOrders, symbol)
		return
	}
	delete(t.takeProfitOrders, symbol)
}

func extractOid(status hyperliquid.OrderStatus) int64 {
	if status.Resting != nil {
		return status.Resting.Oid
	}
	if status.Filled != nil {
		return int64(status.Filled.Oid)
	}
	return 0
}

// triggerOrdersBySymbol 获取当前币种的触发类挂单（仅 ReduceOnly）
func (t *HyperliquidTrader) triggerOrdersBySymbol(symbol string) ([]hyperliquid.FrontendOpenOrder, error) {
	coin := convertSymbolToHyperliquid(symbol)
	orders, err := t.exchange.Info().FrontendOpenOrders(t.ctx, t.walletAddr)
	if err != nil {
		return nil, err
	}

	var filtered []hyperliquid.FrontendOpenOrder
	for _, ord := range orders {
		if ord.Coin != coin {
			continue
		}
		if !ord.ReduceOnly {
			continue
		}
		if !(ord.IsTrigger || ord.IsPositionTpSl) {
			continue
		}
		filtered = append(filtered, ord)
	}
	return filtered, nil
}

// ListActiveTpSlOrders 列出某个币种的当前触发类止盈/止损单（仅ReduceOnly）
func (t *HyperliquidTrader) ListActiveTpSlOrders(symbol string) ([]hyperliquid.FrontendOpenOrder, error) {
	return t.triggerOrdersBySymbol(symbol)
}

// classifyTpSl 统一的订单类型分类函数
// 封装 detectOrderKind，提供更清晰的接口
func classifyTpSl(ord hyperliquid.FrontendOpenOrder, positionSide string) string {
	return detectOrderKind(ord, positionSide)
}

// detectOrderKind 智能检测订单类型（止盈/止损）
// 使用多层检测策略，基于 v0.24.0 SDK 的特性进行优化
func detectOrderKind(ord hyperliquid.FrontendOpenOrder, positionSide string) string {
	// 第1层：直接检查 OrderType 字段（最高优先级）
	if kind := checkOrderTypeField(ord); kind != "" {
		return kind
	}

	// 第2层：对于明确标记为TP/SL的订单，使用触发条件判断
	if ord.IsPositionTpSl {
		if kind := checkTriggerCondition(ord, positionSide); kind != "" {
			return kind
		}
		// 无法通过触发条件判断，标记为需要启发式处理
		return "unknown"
	}

	// 第3层：对于非TP/SL标记的触发单，尝试触发条件判断
	if ord.IsTrigger {
		if kind := checkTriggerCondition(ord, positionSide); kind != "" {
			return kind
		}
	}

	// 完全无法判断
	return ""
}

// checkOrderTypeField 检查 OrderType 字段
func checkOrderTypeField(ord hyperliquid.FrontendOpenOrder) string {
	orderType := strings.ToLower(strings.TrimSpace(ord.OrderType))

	// 精确匹配（最高优先级）
	switch orderType {
	case "tp":
		return "tp"
	case "sl":
		return "sl"
	}

	// 包含匹配（中等优先级）
	switch {
	case strings.Contains(orderType, "tp") && !strings.Contains(orderType, "sl"):
		return "tp"
	case strings.Contains(orderType, "sl") && !strings.Contains(orderType, "tp"):
		return "sl"
	}

	// 特殊值处理
	switch orderType {
	case "takeprofit", "take_profit":
		return "tp"
	case "stoploss", "stop_loss":
		return "sl"
	}

	return ""
}

// checkTriggerCondition 通过触发条件判断订单类型
func checkTriggerCondition(ord hyperliquid.FrontendOpenOrder, positionSide string) string {
	cond := normalizeTriggerCond(ord.TriggerCondition)
	if cond == "" {
		return ""
	}

	return classifyByCond(strings.ToUpper(positionSide), cond, ord.IsTrigger)
}

func normalizeTriggerCond(cond string) string {
	c := strings.TrimSpace(strings.ToLower(cond))
	switch c {
	case "<", "<=", "lte":
		return "<="
	case ">", ">=", "gte":
		return ">="
	default:
		return c
	}
}

func classifyByCond(side, cond string, isTrigger bool) string {
	isDown := cond == "<" || cond == "<="
	isUp := cond == ">" || cond == ">="

	if !isDown && !isUp {
		// 修复：对于模糊的触发条件，不要轻易分类为止损单
		// 返回空字符串，让上层逻辑使用更可靠的方式判断
		return ""
	}

	switch side {
	case "LONG":
		if isDown {
			return "sl"
		}
		if isUp {
			return "tp"
		}
	case "SHORT":
		if isUp {
			return "sl"
		}
		if isDown {
			return "tp"
		}
	}
	return ""
}

// getPositionSide 获取持仓方向（LONG/SHORT）
func (t *HyperliquidTrader) getPositionSide(symbol string) (string, error) {
	positions, err := t.GetPositions()
	if err != nil {
		return "", err
	}
	for _, pos := range positions {
		sym, _ := pos["symbol"].(string)
		side, _ := pos["side"].(string)
		if sym == symbol && side != "" {
			return strings.ToUpper(side), nil
		}
	}
	return "", fmt.Errorf("持仓不存在: %s", symbol)
}

// GetMarketPrice 获取市场价格
func (t *HyperliquidTrader) GetMarketPrice(symbol string) (float64, error) {
	coin, err := t.resolveCoin(symbol)
	if err != nil {
		return 0, err
	}

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

	// 使用 Info API allMids 兜底获取价格
	if priceFloat, err := t.fetchPriceFromInfoAPI(coin); err == nil {
		log.Printf("🔄 使用 InfoAPI allMids 获取价格成功: %s = %.6f", coin, priceFloat)
		return priceFloat, nil
	} else {
		log.Printf("⚠️ InfoAPI allMids 获取价格失败: %v", err)
	}

	// 使用 recentTrades 兜底获取最新成交价
	if priceFloat, err := t.fetchPriceFromRecentTrades(coin); err == nil {
		log.Printf("🔄 使用 recentTrades 获取价格成功: %s = %.6f", coin, priceFloat)
		return priceFloat, nil
	} else {
		log.Printf("⚠️ recentTrades 获取价格失败: %v", err)
	}

	return 0, fmt.Errorf("未找到 %s 的价格", coin)
}

// SetStopLoss 设置止损单
func (t *HyperliquidTrader) SetStopLoss(symbol string, positionSide string, quantity, stopPrice float64) error {
	coin := convertSymbolToHyperliquid(symbol)

	isBuy := positionSide == "SHORT" // 空仓止损=买入，多仓止损=卖出
	slCloid := t.buildCloid(symbol, "sl")

	// ⚠️ 关键：根据币种精度要求，四舍五入数量
	roundedQuantity := t.roundToSzDecimals(coin, quantity)

	// ⚠️ 关键：价格精度处理
	roundedStopPrice := t.roundPriceForCoin(coin, stopPrice, false)

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
		ReduceOnly:    true,
		ClientOrderID: &slCloid,
	}

	status, err := t.exchange.Order(t.ctx, order, nil)
	if err != nil {
		return fmt.Errorf("设置止损失败: %w", err)
	}

	t.rememberStopLossOrder(symbol, status, slCloid)
	log.Printf("  止损价设置: %.4f (cloid=%s oid=%d)", roundedStopPrice, slCloid, extractOid(status))
	return nil
}

// SetTakeProfit 设置止盈单
func (t *HyperliquidTrader) SetTakeProfit(symbol string, positionSide string, quantity, takeProfitPrice float64) error {
	coin := convertSymbolToHyperliquid(symbol)

	isBuy := positionSide == "SHORT" // 空仓止盈=买入，多仓止盈=卖出

	// ⚠️ 关键：根据币种精度要求，四舍五入数量
	roundedQuantity := t.roundToSzDecimals(coin, quantity)

	// ⚠️ 关键：价格精度处理
	roundedTakeProfitPrice := t.roundPriceForCoin(coin, takeProfitPrice, false)

	// 使用 Trigger 订单设置止盈
	tpCloid := t.buildCloid(symbol, "tp")

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
		ReduceOnly:    true,
		ClientOrderID: &tpCloid,
	}

	status, err := t.exchange.Order(t.ctx, order, nil)
	if err != nil {
		return fmt.Errorf("设置止盈失败: %w", err)
	}

	t.rememberTakeProfitOrder(symbol, status, tpCloid)
	log.Printf("  止盈价设置: %.4f (cloid=%s oid=%d)", roundedTakeProfitPrice, tpCloid, extractOid(status))
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
	// 严格从 HIP-3 API 获取 SzDecimals，不使用任何默认值
	normalizedCoin := normalizeHip3Symbol(coin)

	// 首先检查 HIP-3 缓存
	if t.hip3Meta != nil {
		if asset, ok := t.hip3Meta[normalizedCoin]; ok {
			log.Printf("✅ [HIP-3 API] %s 使用缓存的 SzDecimals: %d", normalizedCoin, asset.SzDecimals)
			return asset.SzDecimals
		}
	}

	// 如果未缓存，尝试刷新获取
	if norm, _, err := t.fetchPerpMetaAsset(coin, true); err == nil && norm != "" {
		if asset, ok := t.hip3Meta[norm]; ok {
			log.Printf("✅ [HIP-3 API] %s 使用刷新的 SzDecimals: %d", norm, asset.SzDecimals)
			return asset.SzDecimals
		}
	} else if err != nil {
		log.Printf("❌ [HIP-3 API] 获取 %s SzDecimals 失败: %v", coin, err)
	}

	// 不使用任何默认值 - 如果 API 无法提供数据，这是一个严重的配置错误
	log.Printf("🚨 [HIP-3 API] 严重错误: 无法从 API 获取 %s (%s) 的 SzDecimals", coin, normalizedCoin)
	panic(fmt.Sprintf("SzDecimals not found in HIP-3 API response for %s. API configuration may be incorrect.", coin))
}

// GetMeta 获取meta信息
func (t *HyperliquidTrader) GetMeta() *hyperliquid.Meta {
	return t.meta
}

// GetAllAssets 获取所有资产信息
func (t *HyperliquidTrader) GetAllAssets() ([]interface{}, error) {
	if t.meta == nil {
		return nil, fmt.Errorf("meta信息为空")
	}

	if t.meta.Universe == nil {
		return nil, fmt.Errorf("资产信息为空")
	}

	// 转换为通用接口类型
	var result []interface{}
	for _, asset := range t.meta.Universe {
		result = append(result, asset)
	}

	return result, nil
}

// roundToSzDecimals 将数量四舍五入到正确的精度
func (t *HyperliquidTrader) roundToSzDecimals(coin string, quantity float64) float64 {
	szDecimals := t.getSzDecimals(coin)

	// 计算倍数（10^szDecimals）
	multiplier := 1.0
	for i := 0; i < szDecimals; i++ {
		multiplier *= 10.0
	}

	// 截断到步长，避免向上取整导致无效数量
	return math.Floor(quantity*multiplier) / multiplier
}

// getPxDecimals 获取价格小数精度（优先使用API的PxDecimals，SzDecimals作为后备）
func (t *HyperliquidTrader) getPxDecimals(coin string) (int, bool) {
	normalizedCoin := normalizeHip3Symbol(coin)

	// Check HIP-3 cache first - 优先使用 PxDecimals
	if t.hip3Meta != nil {
		if asset, ok := t.hip3Meta[normalizedCoin]; ok {
			if asset.PxDecimals != nil {
				log.Printf("✅ [HIP-3] %s 使用 API PxDecimals: %d 位小数", normalizedCoin, *asset.PxDecimals)
				return *asset.PxDecimals, true
			} else {
				// PxDecimals 为 nil，使用 SzDecimals 作为后备
				log.Printf("🔄 [HIP-3] %s PxDecimals 为 nil，使用 SzDecimals: %d 位小数", normalizedCoin, asset.SzDecimals)
				return asset.SzDecimals, true
			}
		}
	}

	// 如果未缓存，尝试刷新获取
	if norm, _, err := t.fetchPerpMetaAsset(coin, true); err == nil && norm != "" {
		if asset, ok := t.hip3Meta[norm]; ok {
			if asset.PxDecimals != nil {
				log.Printf("✅ [HIP-3] %s 刷新后使用 PxDecimals: %d 位小数", norm, *asset.PxDecimals)
				return *asset.PxDecimals, true
			} else {
				// PxDecimals 为 nil，使用 SzDecimals 作为后备
				log.Printf("🔄 [HIP-3] %s 刷新后 PxDecimals 为 nil，使用 SzDecimals: %d 位小数", norm, asset.SzDecimals)
				return asset.SzDecimals, true
			}
		}
	} else if err != nil {
		log.Printf("❌ [HIP-3] 获取 %s 价格精度失败: %v", coin, err)
		return 0, false
	}

	log.Printf("❌ [HIP-3] %s 未找到任何精度信息", coin)
	return 0, false
}

// isStockAsset 检测是否为 HIP-3 股票资产
func (t *HyperliquidTrader) isStockAsset(coin string) bool {
	normalizedCoin := normalizeHip3Symbol(coin)
	isStock := strings.Contains(normalizedCoin, ":")

	if isStock {
		log.Printf("🔍 [HIP-3] 检测到股票资产: %s", normalizedCoin)
	}

	return isStock
}

// logPriceDetails 详细记录价格处理信息用于调试
func (t *HyperliquidTrader) logPriceDetails(symbol, coin string, price float64, context string) {
	log.Printf("🔍 [%s] 价格详情 for %s/%s:", context, symbol, coin)
	log.Printf("   • 原始价格: %.8f", price)

	if pxDec, ok := t.getPxDecimals(coin); ok {
		log.Printf("   • PxDecimals: %d 位小数", pxDec)
	} else {
		log.Printf("   • PxDecimals: 未找到")
	}

	if t.isStockAsset(coin) {
		stockPrice := t.roundPriceForStock(price, false)
		log.Printf("   • 股票舍入价格: %.8f", stockPrice)
		log.Printf("   • 资产类型: HIP-3 股票")
	} else {
		sigfigPrice := t.roundPriceToSigfigs(price, false)
		log.Printf("   • 有效数字舍入价格: %.8f", sigfigPrice)
		log.Printf("   • 资产类型: 加密货币")
	}
}

// roundPriceForCoin 根据精度（pxDecimals 或5位有效数字）处理价格；truncate=true 时截断到步长
func (t *HyperliquidTrader) roundPriceForCoin(coin string, price float64, truncate bool) float64 {
	if price == 0 {
		return 0
	}

	// Use specific pxDecimals when available
	if pxDec, ok := t.getPxDecimals(coin); ok {
		multiplier := math.Pow10(pxDec)
		var result float64
		if truncate {
			result = math.Floor(price*multiplier) / multiplier
		} else {
			result = math.Round(price*multiplier) / multiplier
		}
		log.Printf("🎯 [HIP-3] 使用 pxDecimals %d: %.8f -> %.8f", pxDec, price, result)
		return result
	}

	// Stock-specific fallback
	if t.isStockAsset(coin) {
		result := t.roundPriceForStock(price, truncate)
		log.Printf("📊 [HIP-3] 股票价格舍入: %.8f -> %.8f", price, result)
		return result
	}

	// Crypto fallback
	result := t.roundPriceToSigfigs(price, truncate)
	log.Printf("📈 [HIP-3] 加密货币价格舍入: %.8f -> %.8f", price, result)
	return result
}

// roundPriceToSigfigs 将价格四舍五入到5位有效数字
// Hyperliquid要求价格使用5位有效数字（significant figures）
func (t *HyperliquidTrader) roundPriceToSigfigs(price float64, truncate bool) float64 {
	if price == 0 {
		return 0
	}

	const sigfigs = 5 // 默认有效数字

	// 计算价格的数量级
	magnitude := math.Abs(price)

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

	// 四舍五入或截断
	if truncate {
		return math.Floor(price*multiplier) / multiplier
	}
	return math.Round(price*multiplier) / multiplier
}

// roundPriceForStock 股票专用价格舍入方法 - 强制2位小数以符合Hyperliquid步长要求
func (t *HyperliquidTrader) roundPriceForStock(price float64, truncate bool) float64 {
	if price == 0 {
		return 0
	}

	// 强制使用2位小数，覆盖动态逻辑，确保符合Hyperliquid步长要求
	multiplier := 100.0 // 强制2位小数
	var result float64
	if truncate {
		result = math.Floor(price*multiplier) / multiplier
	} else {
		result = math.Round(price*multiplier) / multiplier
	}

	log.Printf("📐 [HIP-3] 股票价格强制2位小数舍入: %.8f -> %.8f", price, result)
	return result
}

// validateOrderPrice 验证订单价格是否合理
func (t *HyperliquidTrader) validateOrderPrice(coin string, price float64, isBuy bool) error {
	// Get current market price for validation
	symbol := convertSymbolFromHyperliquid(coin)
	marketPrice, err := t.GetMarketPrice(symbol)
	if err != nil {
		log.Printf("❌ [HIP-3] 无法获取市场价格进行验证: %v", err)
		return fmt.Errorf("failed to get market price for validation: %w", err)
	}

	// Check price deviation limits
	maxDeviation := 0.10 // 10% max deviation for crypto
	if t.isStockAsset(coin) {
		maxDeviation = 0.05 // 5% for stocks
	}

	deviation := math.Abs(price-marketPrice) / marketPrice
	if deviation > maxDeviation {
		log.Printf("❌ [HIP-3] 价格偏差过大: 市场价格=%.6f, 订单价格=%.6f, 偏差=%.2f%% > 限制%.1f%%",
			marketPrice, price, deviation*100, maxDeviation*100)
		return fmt.Errorf("price deviation %.2f%% exceeds maximum %.1f%% for %s",
			deviation*100, maxDeviation*100, coin)
	}

	log.Printf("✅ [HIP-3] 价格验证通过: 市场价格=%.6f, 订单价格=%.6f, 偏差=%.2f%%",
		marketPrice, price, deviation*100)

	// Price step validation for stocks (critical fix)
	if t.isStockAsset(coin) {
		if err := t.validatePriceStep(coin, price); err != nil {
			log.Printf("❌ [HIP-3] %v", err)
			return err
		}
	}

	return nil
}

// validatePriceStep 验证价格是否为有效步长的整数倍（针对股票资产）
func (t *HyperliquidTrader) validatePriceStep(coin string, price float64) error {
	if t.isStockAsset(coin) {
		// 检查是否为0.01的整数倍（2位小数）
		remainder := math.Mod(price*100, 1)
		if remainder > 1e-10 {
			log.Printf("❌ [HIP-3] 股票价格步长验证失败: %s 价格=%.8f, 余数=%.10f", coin, price, remainder)
			return fmt.Errorf("股票价格步长验证失败: %s 价格 %.8f 必须是0.01的整数倍 (当前余数: %.10f)", coin, price, remainder)
		}
		log.Printf("✅ [HIP-3] 股票价格步长验证通过: %s %.8f 是有效的0.01步长", coin, price)
	}
	return nil
}

// executeOrderWithRetry 执行带重试机制的订单（针对股票价格验证失败）
func (t *HyperliquidTrader) executeOrderWithRetry(order *hyperliquid.CreateOrderRequest, maxRetries int) error {
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			log.Printf("🔄 [HIP-3] 订单重试第 %d 次: %s 价格=%.8f", attempt+1, order.Coin, order.Price)
		}

		// 执行订单
		_, err := t.exchange.Order(t.ctx, *order, nil)
		if err == nil {
			log.Printf("✅ [HIP-3] 订单执行成功: %s @ %.8f (尝试次数: %d)", order.Coin, order.Price, attempt+1)
			return nil // 成功
		}

		// 检查是否是价格相关错误且是股票资产
		errStr := strings.ToLower(err.Error())
		isPriceError := strings.Contains(errStr, "invalid price") ||
			strings.Contains(errStr, "price precision") ||
			strings.Contains(errStr, "price step")

		if isPriceError && t.isStockAsset(order.Coin) && attempt < maxRetries-1 {
			log.Printf("⚠️ [HIP-3] 股票价格错误，尝试调整价格重试: %v", err)

			// 调整价格（增加更保守的偏差）
			newPrice := t.adjustPriceForRetry(order.Price, attempt+1, order.IsBuy)
			if newPrice != order.Price {
				order.Price = newPrice
				log.Printf("🔧 [HIP-3] 价格调整: %.8f -> %.8f", order.Price, newPrice)
				continue // 重试
			} else {
				log.Printf("❌ [HIP-3] 无法进一步调整价格，返回错误")
				break
			}
		}

		// 非价格错误或已达到最大重试次数
		log.Printf("❌ [HIP-3] 订单执行失败: %v", err)
		return err
	}

	return fmt.Errorf("order failed after %d attempts", maxRetries)
}

// adjustPriceForRetry 为重试调整价格（逐步增加偏差）
func (t *HyperliquidTrader) adjustPriceForRetry(currentPrice float64, attempt int, isBuy bool) float64 {
	// 基础偏差，每次重试增加
	var additionalDeviation float64
	switch attempt {
	case 1:
		additionalDeviation = 0.01 // 1%
	case 2:
		additionalDeviation = 0.02 // 2%
	case 3:
		additionalDeviation = 0.03 // 3%
	default:
		additionalDeviation = 0.04 // 4%
	}

	var adjustedPrice float64
	if isBuy {
		// 买单：提高价格以增加成交概率
		adjustedPrice = currentPrice * (1.0 + additionalDeviation)
		log.Printf("🔧 [HIP-3] 买单价格调整: %.8f * %.3f -> %.8f", currentPrice, 1.0+additionalDeviation, adjustedPrice)
	} else {
		// 卖单：降低价格以增加成交概率
		adjustedPrice = currentPrice * (1.0 - additionalDeviation)
		log.Printf("🔧 [HIP-3] 卖单价格调整: %.8f * %.3f -> %.8f", currentPrice, 1.0-additionalDeviation, adjustedPrice)
	}

	// 强制2位小数舍入（适用于所有股票资产）
	multiplier := 100.0
	result := math.Round(adjustedPrice*multiplier) / multiplier

	log.Printf("📐 [HIP-3] 重试价格舍入: %.8f -> %.8f", adjustedPrice, result)
	return result
}

// diagnosePriceIssues 价格处理诊断工具
func (t *HyperliquidTrader) diagnosePriceIssues(symbol string) error {
	log.Printf("🧪 [HIP-3] 开始价格处理诊断: %s", symbol)

	// Test symbol resolution
	coin, err := t.resolveCoin(symbol)
	if err != nil {
		log.Printf("❌ 符号解析失败: %s -> %v", symbol, err)
		return fmt.Errorf("symbol resolution failed: %w", err)
	}
	log.Printf("✅ 符号解析: %s -> %s", symbol, coin)

	// Test asset type detection
	isStock := t.isStockAsset(coin)
	log.Printf("🔍 资产类型检测: %s -> 股票=%t", coin, isStock)

	// Test price fetching from multiple sources
	prices := make(map[string]float64)

	// Test AllMids
	if allMids, err := t.exchange.Info().AllMids(t.ctx); err == nil {
		if priceStr, ok := allMids[coin]; ok {
			if price, err := strconv.ParseFloat(priceStr, 64); err == nil {
				prices["AllMids"] = price
				log.Printf("📊 AllMids价格: %s = %.6f", coin, price)
			}
		}
	} else {
		log.Printf("⚠️ AllMids API失败: %v", err)
	}

	// Test recentTrades (for stocks)
	if isStock {
		if price, err := t.fetchPriceFromRecentTrades(coin); err == nil {
			prices["recentTrades"] = price
			log.Printf("📈 recentTrades价格: %s = %.6f", coin, price)
		} else {
			log.Printf("⚠️ recentTrades API失败: %v", err)
		}
	}

	// Test Info API
	if price, err := t.fetchPriceFromInfoAPI(coin); err == nil {
		prices["InfoAPI"] = price
		log.Printf("💾 InfoAPI价格: %s = %.6f", coin, price)
	} else {
		log.Printf("⚠️ InfoAPI失败: %v", err)
	}

	if len(prices) == 0 {
		return fmt.Errorf("所有价格源都失败 for %s", coin)
	}

	// Test precision handling
	for source, price := range prices {
		roundedPrice := t.roundPriceForCoin(coin, price, false)
		log.Printf("📐 [%s] 价格舍入: %.6f -> %.6f", source, price, roundedPrice)
	}

	// Test precision detection
	if pxDec, ok := t.getPxDecimals(coin); ok {
		log.Printf("🎯 价格精度: %d 位小数", pxDec)
	} else {
		log.Printf("📊 价格精度: 未找到，使用默认策略")
	}

	log.Printf("✅ [HIP-3] 价格处理诊断完成")
	return nil
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

// convertSymbolFromHyperliquid 将Hyperliquid格式转换回标准symbol
// 例如: "BTC" -> "BTCUSDT", "xyz:TSLA" -> "xyz:TSLA"
func convertSymbolFromHyperliquid(coin string) string {
	// 对于HIP-3股票资产，直接返回
	if strings.Contains(coin, ":") {
		return coin
	}

	// 对于加密货币，添加USDT后缀
	return coin + "USDT"
}

// resolveCoin 支持HIP-3股票符号（带冒号），优先直接匹配，否则尝试通过AllMids匹配后缀
func (t *HyperliquidTrader) resolveCoin(symbol string) (string, error) {
	// 先处理显式符号
	coin := convertSymbolToHyperliquid(symbol)
	if strings.Contains(coin, ":") {
		return coin, nil
	}

	// 始终优先使用 Info API 的 allPerpMetas 做HIP-3匹配（股票等非加密资产来源）
	if coinFromInfo, err := t.resolveFromInfoAPI(coin, false); err == nil && coinFromInfo != "" {
		log.Printf("🔄 InfoAPI 优先匹配到资产: %s (请求符号: %s)", coinFromInfo, symbol)
		return coinFromInfo, nil
	} else if err != nil {
		log.Printf("⚠️ InfoAPI 优先匹配失败: %v", err)
	}

	// 查询所有mid价格以获取有效交易对列表
	allMids, err := t.exchange.Info().AllMids(t.ctx)
	if err != nil {
		return "", fmt.Errorf("获取交易对列表失败: %w", err)
	}

	// 直接匹配（如纯币种）
	if _, ok := allMids[coin]; ok {
		return coin, nil
	}
	// 大小写不敏感匹配完整键
	for k := range allMids {
		if strings.EqualFold(k, coin) {
			return k, nil
		}
	}

	// 尝试匹配带前缀的HIP-3资产（形如 xyz:TSLA）
	for k := range allMids {
		if !strings.Contains(k, ":") {
			continue
		}
		parts := strings.SplitN(k, ":", 2)
		if len(parts) != 2 {
			continue
		}
		if strings.EqualFold(parts[1], coin) {
			return k, nil
		}
	}

	// 兜底：使用缓存的Meta.Universe做HIP-3股票匹配（AllMids可能未包含新股票）
	if t.meta != nil {
		for _, asset := range t.meta.Universe {
			name := asset.Name
			if !strings.Contains(name, ":") {
				continue
			}
			parts := strings.SplitN(name, ":", 2)
			if len(parts) != 2 {
				continue
			}
			if strings.EqualFold(parts[1], coin) {
				log.Printf("🔄 使用Meta.Universe匹配到HIP-3股票: %s (请求符号: %s)", name, symbol)
				return name, nil
			}
		}
	}

	// 再次尝试：刷新Meta后重试（防止启动后新增股票或Meta为空）
	if t.exchange != nil {
		if refreshedMeta, err := t.exchange.Info().Meta(t.ctx); err == nil && refreshedMeta != nil {
			t.meta = refreshedMeta
			for _, asset := range refreshedMeta.Universe {
				name := asset.Name
				if !strings.Contains(name, ":") {
					continue
				}
				parts := strings.SplitN(name, ":", 2)
				if len(parts) != 2 {
					continue
				}
				if strings.EqualFold(parts[1], coin) {
					log.Printf("🔄 Meta刷新后匹配到HIP-3股票: %s (请求符号: %s)", name, symbol)
					return name, nil
				}
			}
		} else if err != nil {
			log.Printf("⚠️ 刷新Meta失败: %v", err)
		}
	}

	// 使用 Info API 直接拉取 allPerpMetas 兜底（与 /stocks 列表一致，支持 HIP-3 股票）
	if coinFromInfo, err := t.resolveFromInfoAPI(coin, false); err == nil && coinFromInfo != "" {
		log.Printf("🔄 InfoAPI 匹配到HIP-3股票: %s (请求符号: %s)", coinFromInfo, symbol)
		return coinFromInfo, nil
	} else if err != nil {
		log.Printf("⚠️ InfoAPI 匹配失败: %v", err)
	}

	return "", fmt.Errorf("未找到交易对: %s", symbol)
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

// classifyOrderByPriceHeuristic 基于价格启发式判断订单类型
// 当其他方法无法确定时，使用触发价格与当前价格的相对关系来判断
func (t *HyperliquidTrader) classifyOrderByPriceHeuristic(ord hyperliquid.FrontendOpenOrder, positionSide string, triggerPx float64) string {
	// 获取当前市场价格
	allMids, err := t.exchange.Info().AllMids(t.ctx)
	if err != nil {
		log.Printf("⚠️ 获取市场价格失败，无法进行启发式判断: %v", err)
		return ""
	}

	priceStr, ok := allMids[ord.Coin]
	if !ok {
		log.Printf("⚠️ 找不到币种 %s 的市场价格", ord.Coin)
		return ""
	}

	currentPx, err := strconv.ParseFloat(priceStr, 64)
	if err != nil {
		log.Printf("⚠️ 解析市场价格失败: %v", err)
		return ""
	}

	// 基于持仓方向和价格相对关系判断
	switch strings.ToUpper(positionSide) {
	case "LONG":
		if triggerPx < currentPx {
			log.Printf("📊 启发式判断: 多头订单 触发价(%.4f) < 当前价(%.4f) = 止损", triggerPx, currentPx)
			return "sl"
		} else if triggerPx > currentPx {
			log.Printf("📊 启发式判断: 多头订单 触发价(%.4f) > 当前价(%.4f) = 止盈", triggerPx, currentPx)
			return "tp"
		}
	case "SHORT":
		if triggerPx > currentPx {
			log.Printf("📊 启发式判断: 空头订单 触发价(%.4f) > 当前价(%.4f) = 止损", triggerPx, currentPx)
			return "sl"
		} else if triggerPx < currentPx {
			log.Printf("📊 启发式判断: 空头订单 触发价(%.4f) < 当前价(%.4f) = 止盈", triggerPx, currentPx)
			return "tp"
		}
	}

	// 价格等于当前价格，无法判断
	log.Printf("⚠️ 启发式判断失败: 触发价(%.4f) == 当前价(%.4f)，无法区分止盈止损", triggerPx, currentPx)
	return ""
}
