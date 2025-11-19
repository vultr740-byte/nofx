package telegram

import (
	"fmt"
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

// formatBalanceMessage 格式化余额消息
func (s *HyperliquidService) formatBalanceMessage(balance map[string]interface{}) string {
	totalWalletBalance, _ := balance["totalWalletBalance"].(float64)
	availableBalance, _ := balance["availableBalance"].(float64)
	totalUnrealizedProfit, _ := balance["totalUnrealizedProfit"].(float64)
	spotBalance, _ := balance["spotBalance"].(float64)

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

	message := fmt.Sprintf(`💰 账户余额总览

💎 总资产: %.2f USDC

📊 资产详情:
• 现货余额: %.2f USDC
• 可用余额: %.2f USDC
• 合约净值: %.2f USDC

%s 盈亏状况:
• 未实现盈亏: %.2f USDC (%.2f%%)`,
		totalWalletBalance,
		spotBalance,
		availableBalance,
		totalWalletBalance-spotBalance,
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
	message.WriteString("💎 当前持仓\n\n")

	for i, pos := range positions {
		symbol, _ := pos["symbol"].(string)
		side, _ := pos["side"].(string)
		positionAmt, _ := pos["positionAmt"].(float64)
		entryPrice, _ := pos["entryPrice"].(float64)
		markPrice, _ := pos["markPrice"].(float64)
		unRealizedProfit, _ := pos["unRealizedProfit"].(float64)
		leverage, _ := pos["leverage"].(float64)

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

		pnlEmoji := "✅"
		if unRealizedProfit < 0 {
			pnlEmoji = "❌"
		}

		// 格式化杠杆显示
		leverageText := fmt.Sprintf("%.0fx", leverage)

		message.WriteString(fmt.Sprintf(`%s <b>%s → %s (%s)</b>
持仓数量: <code>%.4f</code>
入场价格: <code>$%.4f</code>
标记价格: <code>$%.4f</code>
未实现盈亏: <code>%.2f USDC (%.2f%%)</code> %s

`,
			sideEmoji, symbol, sideText, leverageText,
			positionAmt, entryPrice, markPrice,
			unRealizedProfit, profitPercent, pnlEmoji,
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

// absFloat 返回浮点数的绝对值
func absFloat(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
