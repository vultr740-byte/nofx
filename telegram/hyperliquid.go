package telegram

import (
	"encoding/json"
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

// GetBalance 获取余额并格式化为 Telegram 消息
func (s *HyperliquidService) GetBalance(agentKey, walletAddr string, testnet bool) (string, error) {
	// 创建 Hyperliquid 交易器
	trader, err := trader.NewHyperliquidTrader(agentKey, walletAddr, testnet)
	if err != nil {
		return "", fmt.Errorf("创建 Hyperliquid 交易器失败: %w", err)
	}

	// 获取余额
	balance, err := trader.GetBalance()
	if err != nil {
		return "", fmt.Errorf("获取余额失败: %w", err)
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
		return `📊 当前持仓

🎯 暂无持仓

---
💡 使用 /deposit 充值资金后即可开始交易`
	}

	var message strings.Builder
	message.WriteString("📊 当前持仓\n\n")

	for i, pos := range positions {
		symbol, _ := pos["symbol"].(string)
		side, _ := pos["side"].(string)
		positionAmt, _ := pos["positionAmt"].(float64)
		entryPrice, _ := pos["entryPrice"].(float64)
		markPrice, _ := pos["markPrice"].(float64)
		unRealizedProfit, _ := pos["unRealizedProfit"].(float64)

		// 计算盈亏百分比
		var profitPercent float64
		if entryPrice > 0 && positionAmt > 0 {
			profitPercent = ((markPrice - entryPrice) / entryPrice) * 100
			if side == "short" {
				profitPercent = -profitPercent
			}
		}

		// 方向表情符号
		sideEmoji := "📈"
		if side == "short" {
			sideEmoji = "📉"
		}

		message.WriteString(fmt.Sprintf(`%s %s → %s
数量: %.4f | 入场: $%.2f
标记: $%.2f | 盈亏: %.2f USDC (%.2f%%)

`,
			sideEmoji, symbol, strings.ToUpper(side),
			positionAmt, entryPrice, markPrice,
			unRealizedProfit, profitPercent,
		))

		// 如果不是最后一个持仓，添加分隔线
		if i < len(positions)-1 {
			message.WriteString("---\n")
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

	message.WriteString(fmt.Sprintf(`
📋 持仓汇总
• 持仓数量: %d 个
• 总未实现盈亏: %.2f USDC`,
		positionCount, totalUnrealized,
	))

	return message.String()
}

// ExtractAgentKeyAndWallet 从 session_data 中提取 Agent Key 和 Wallet Address
func (s *HyperliquidService) ExtractAgentKeyAndWallet(sessionDataStr string) (agentKey, walletAddr string, err error) {
	// 解析 JSON
	var sessionData map[string]interface{}
	if err := json.Unmarshal([]byte(sessionDataStr), &sessionData); err != nil {
		return "", "", fmt.Errorf("解析 session_data 失败: %w", err)
	}

	// 提取 agent_key
	if key, ok := sessionData["agent_key"].(string); ok {
		agentKey = key
	} else {
		return "", "", fmt.Errorf("未找到 agent_key")
	}

	// 提取 wallet_address
	if addr, ok := sessionData["wallet_address"].(string); ok {
		walletAddr = addr
	} else {
		return "", "", fmt.Errorf("未找到 wallet_address")
	}

	return agentKey, walletAddr, nil
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