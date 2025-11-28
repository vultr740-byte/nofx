package telegram

import (
	"fmt"
	"log"
	"reflect"
	"strings"

	"nofx/trader"
)

// HyperliquidService Hyperliquid 服务
type HyperliquidService struct{}

// NewHyperliquidService 创建 Hyperliquid 服务
func NewHyperliquidService() *HyperliquidService {
	return &HyperliquidService{}
}

// FetchBalance 获取原始余额信息，供外层业务复用
func (s *HyperliquidService) FetchBalance(agentKey, walletAddr string, testnet bool) (map[string]interface{}, error) {
	// 创建 Hyperliquid 交易器
	trader, err := trader.NewHyperliquidTrader(agentKey, walletAddr, testnet)
	if err != nil {
		return nil, fmt.Errorf("创建 Hyperliquid 交易器失败: %w", err)
	}

	// 获取余额
	balance, err := trader.GetBalance()
	if err != nil {
		return nil, fmt.Errorf("获取余额失败: %w", err)
	}

	return balance, nil
}

// GetBalance 获取余额并格式化为 Telegram 消息
func (s *HyperliquidService) GetBalance(agentKey, walletAddr string, testnet bool) (string, error) {
	balance, err := s.FetchBalance(agentKey, walletAddr, testnet)
	if err != nil {
		return "", err
	}

	// 格式化余额信息
	return s.formatBalanceMessage(balance), nil
}

// GetPositions 获取持仓并格式化为 Telegram 消息
func (s *HyperliquidService) GetPositions(agentKey, walletAddr string, testnet bool) (string, error) {
	// 创建 Hyperliquid 交易器
	trader, err := trader.NewHyperliquidTrader(agentKey, walletAddr, testnet)
	if err != nil {
		return "", fmt.Errorf("创建 Hyperliquid 交易器失败: %w", err)
	}

	// 获取持仓
	positions, err := trader.GetPositions()
	if err != nil {
		return "", fmt.Errorf("获取持仓失败: %w", err)
	}

	// 格式化持仓信息
	return s.formatPositionsMessage(positions), nil
}

// GetPositionsWithData 获取持仓并返回消息和原始数据
func (s *HyperliquidService) GetPositionsWithData(agentKey, walletAddr string, testnet bool) (string, []map[string]interface{}, error) {
	// 添加panic恢复机制，防止整个程序崩溃
	defer func() {
		if r := recover(); r != nil {
			log.Printf("GetPositionsWithData panic recovered: %v", r)
		}
	}()

	// 创建 Hyperliquid 交易器
	trader, err := trader.NewHyperliquidTrader(agentKey, walletAddr, testnet)
	if err != nil {
		return "", nil, fmt.Errorf("创建 Hyperliquid 交易器失败: %w", err)
	}

	// 获取持仓
	positions, err := trader.GetPositions()
	if err != nil {
		return "", nil, fmt.Errorf("获取持仓失败: %w", err)
	}

	// 格式化持仓信息
	message := s.formatPositionsMessage(positions)
	return message, positions, nil
}

// formatBalanceMessage 格式化余额消息
func (s *HyperliquidService) formatBalanceMessage(balance map[string]interface{}) string {
	totalWalletBalance, _ := balance["totalWalletBalance"].(float64)
	availableBalance, _ := balance["availableBalance"].(float64)
	totalUnrealizedProfit, _ := balance["totalUnrealizedProfit"].(float64)
	spotBalance, _ := balance["spotBalance"].(float64)
	totalPosition, _ := balance["totalPosition"].(float64)

	// 计算盈亏百分比
	var profitPercent float64
	if totalWalletBalance-totalUnrealizedProfit > 0 {
		profitPercent = (totalUnrealizedProfit / (totalWalletBalance - totalUnrealizedProfit)) * 100
	}

	// 盈亏表情符号
	pnlEmoji := "📊"
	if totalUnrealizedProfit > 0 {
		pnlEmoji = "🟢"
	} else if totalUnrealizedProfit < 0 {
		pnlEmoji = "🔴"
	}

	message := fmt.Sprintf(`总资产: %.2f USDC

📊 资产详情:
• 现货余额: %.2f USDC
• 可用余额: %.2f USDC
• 总持仓: %.2f USDC

%s 盈亏状况:
• 未实现盈亏: %.2f USDC (%.2f%%)`,
		totalWalletBalance,
		spotBalance,
		availableBalance,
		totalPosition,
		pnlEmoji,
		totalUnrealizedProfit,
		profitPercent,
	)

	return message
}

// formatPositionsMessage 格式化持仓消息
func (s *HyperliquidService) formatPositionsMessage(positions []map[string]interface{}) string {
	if len(positions) == 0 {
		return `🕒 暂无持仓

💡 去充值 /deposit`
	}

	var message strings.Builder

	for i, pos := range positions {
		symbol, _ := pos["symbol"].(string)
		side, _ := pos["side"].(string)
		positionAmt, _ := pos["positionAmt"].(float64)
		entryPrice, _ := pos["entryPrice"].(float64)
		markPrice, _ := pos["markPrice"].(float64)
		unRealizedProfit, _ := pos["unRealizedProfit"].(float64)
		leverage, _ := pos["leverage"].(float64)
		stopLoss, _ := pos["stopLoss"].(float64)
		takeProfit, _ := pos["takeProfit"].(float64)

		// 计算盈亏百分比
		var profitPercent float64
		if entryPrice > 0 && positionAmt > 0 {
			profitPercent = ((markPrice - entryPrice) / entryPrice) * 100
			if side == "short" {
				profitPercent = -profitPercent
			}
		}

		// 方向和盈亏表情符号
		sideEmoji := "📈"
		sideText := "多头"
		if side == "short" {
			sideEmoji = "📉"
			sideText = "空头"
		}

		pnlEmoji := "🟢"
		if unRealizedProfit < 0 {
			pnlEmoji = "🔴"
		}

		// 格式化杠杆显示
		leverageText := fmt.Sprintf("%.0fx", leverage)

		message.WriteString(fmt.Sprintf(`%s <b>%s → %s (%s)</b>
持仓数量: <code>%.4f</code>
入场价格: <code>$%.4f</code>
标记价格: <code>$%.4f</code>
未实现盈亏: <code>%.2f USDC (%.2f%%)</code> %s
止损: <code>%s</code>
止盈: <code>%s</code>
`,
			sideEmoji, symbol, sideText, leverageText,
			positionAmt, entryPrice, markPrice,
			unRealizedProfit, profitPercent, pnlEmoji,
			func() string {
				if stopLoss > 0 {
					return fmt.Sprintf("$%.4f", stopLoss)
				}
				return "未设置"
			}(),
			func() string {
				if takeProfit > 0 {
					return fmt.Sprintf("$%.4f", takeProfit)
				}
				return "未设置"
			}(),
		))

		// 如果不是最后一个持仓，添加分隔线
		if i < len(positions)-1 {
			message.WriteString("─────────────────\n")
		}
	}

	// 添加汇总信息
	totalUnrealized := 0.0
	positionCount := len(positions)
	for _, pos := range positions {
		if pnl, ok := pos["unRealizedProfit"].(float64); ok {
			totalUnrealized += pnl
		}
	}

	// 计算总盈亏百分比
	var totalProfitPercent float64
	if positionCount > 0 {
		totalValue := 0.0
		for _, pos := range positions {
			if value, ok := pos["entryPrice"].(float64); ok {
				if amt, ok := pos["positionAmt"].(float64); ok {
					totalValue += value * amt
				}
			}
		}
		if totalValue > 0 {
			totalProfitPercent = (totalUnrealized / totalValue) * 100
		}
	}

	message.WriteString(fmt.Sprintf(`
📋 <b>持仓汇总</b>
活跃持仓: <code>%d 个</code>
总未实现盈亏: <code>%.2f USDC (%.2f%%)</code>`,
		positionCount, totalUnrealized, totalProfitPercent,
	))

	return message.String()
}

// ValidateAddress 验证地址格式
func (s *HyperliquidService) ValidateAddress(address string) bool {
	return strings.HasPrefix(address, "0x") && len(address) == 42
}

// GetStockAssets 获取所有HIP-3股票资产
func (s *HyperliquidService) GetStockAssets(agentKey, walletAddr string, testnet bool) (string, error) {
	// 创建 Hyperliquid 交易器
	trader, err := trader.NewHyperliquidTrader(agentKey, walletAddr, testnet)
	if err != nil {
		return "", fmt.Errorf("创建 Hyperliquid 交易器失败: %w", err)
	}

	// 获取所有资产信息
	assets, err := s.GetAllPerpMetas(trader)
	if err != nil {
		return "", fmt.Errorf("获取资产信息失败: %w", err)
	}

	// 筛选股票资产
	stocks := s.extractStockAssets(assets)

	// 格式化股票信息
	return s.formatStocksMessage(stocks), nil
}

// GetAllPerpMetas 获取所有永续合约元数据
func (s *HyperliquidService) GetAllPerpMetas(trader *trader.HyperliquidTrader) ([]map[string]interface{}, error) {
	// 添加panic恢复机制
	defer func() {
		if r := recover(); r != nil {
			log.Printf("GetAllPerpMetas panic recovered: %v", r)
		}
	}()

	// 直接使用反射获取meta信息
	meta := trader.GetMeta()
	if meta == nil {
		return nil, fmt.Errorf("meta信息为空")
	}

	if meta.Universe == nil {
		return nil, fmt.Errorf("资产信息为空")
	}

	var result []map[string]interface{}

	// 转换为通用格式
	for _, asset := range meta.Universe {
		// 使用反射获取资产信息
		assetValue := reflect.ValueOf(asset)
		if assetValue.Kind() == reflect.Struct {
			assetMap := map[string]interface{}{}

			// 获取结构体字段
			assetType := assetValue.Type()
			for i := 0; i < assetValue.NumField(); i++ {
				field := assetType.Field(i)
				fieldValue := assetValue.Field(i)

				// 只处理可导出的字段
				if field.IsExported() {
					// 转换字段名
					switch field.Name {
					case "Name":
						assetMap["name"] = fieldValue.Interface()
					case "SzDecimals":
						assetMap["sz_decimals"] = fieldValue.Interface()
					case "PxDecimals":
						assetMap["px_decimals"] = fieldValue.Interface()
					case "IsPerp":
						assetMap["is_perp"] = fieldValue.Interface()
					default:
						assetMap[strings.ToLower(field.Name)] = fieldValue.Interface()
					}
				}
			}

			result = append(result, assetMap)
		}
	}

	return result, nil
}

// extractStockAssets 从所有资产中筛选HIP-3股票资产
func (s *HyperliquidService) extractStockAssets(assets []map[string]interface{}) []map[string]interface{} {
	var stocks []map[string]interface{}

	for _, asset := range assets {
		name, ok := asset["name"].(string)
		if !ok {
			continue
		}

		// HIP-3 股票资产使用带冒号的前缀（如 xyz:NVDA、flx:TSLA 等）
		if !strings.Contains(name, ":") {
			continue
		}

		// 分割前缀和股票代码
		parts := strings.Split(name, ":")
		if len(parts) != 2 {
			continue
		}

		prefix := parts[0]
		symbol := parts[1]

		// 添加股票特有信息
		stockAsset := asset
		stockAsset["prefix"] = prefix
		stockAsset["symbol"] = symbol
		stockAsset["type"] = "stock"

		stocks = append(stocks, stockAsset)
	}

	return stocks
}

// formatStocksMessage 格式化股票资产信息消息
func (s *HyperliquidService) formatStocksMessage(stocks []map[string]interface{}) string {
	if len(stocks) == 0 {
		return `📊 <b>HIP-3 股票资产</b>

暂无可用的HIP-3股票资产

💡 HIP-3股票资产使用冒号前缀格式（如 xyz:NVDA）`
	}

	var message strings.Builder
	message.WriteString("📊 <b>HIP-3 股票资产列表</b>\n\n")

	// 按前缀分组统计
	prefixCount := make(map[string]int)
	for _, stock := range stocks {
		if prefix, ok := stock["prefix"].(string); ok {
			prefixCount[prefix]++
		}
	}

	// 显示统计信息
	message.WriteString("📈 <b>资产统计</b>\n")
	for prefix, count := range prefixCount {
		message.WriteString(fmt.Sprintf("• %s: %d 个资产\n", prefix, count))
	}

	message.WriteString(fmt.Sprintf("\n📋 <b>详细信息</b> (共 %d 个资产)\n\n", len(stocks)))

	// 显示前20个资产的详细信息
	maxDisplay := 20
	if len(stocks) < maxDisplay {
		maxDisplay = len(stocks)
	}

	for i := 0; i < maxDisplay; i++ {
		stock := stocks[i]
		name := stock["name"].(string)
		prefix := stock["prefix"].(string)
		symbol := stock["symbol"].(string)
		szDecimals := stock["sz_decimals"].(int)
		pxDecimals := stock["px_decimals"].(int)

		message.WriteString(fmt.Sprintf("<b>%d. %s</b>\n", i+1, name))
		message.WriteString(fmt.Sprintf("   • 前缀: <code>%s</code>\n", prefix))
		message.WriteString(fmt.Sprintf("   • 股票代码: <code>%s</code>\n", symbol))
		message.WriteString(fmt.Sprintf("   • 数量精度: %d 位小数\n", szDecimals))
		message.WriteString(fmt.Sprintf("   • 价格精度: %d 位小数\n", pxDecimals))
		message.WriteString("   • 类型: 股票合约\n\n")
	}

	// 如果资产数量超过显示限制，添加提示
	if len(stocks) > maxDisplay {
		message.WriteString(fmt.Sprintf("📝 还有 %d 个资产未显示...\n\n", len(stocks)-maxDisplay))
	}

	message.WriteString("💡 <b>提示</b>\n")
	message.WriteString("• HIP-3股票资产支持多空交易\n")
	message.WriteString("• 使用 /start 初始化后可进行股票交易\n")
	message.WriteString("• 股票交易时间遵循美股交易时间")

	return message.String()
}

// absFloat 返回浮点数的绝对值
func absFloat(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
