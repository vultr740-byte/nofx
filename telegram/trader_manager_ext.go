package telegram

import (
	"fmt"
	"strings"

	"nofx/trader"
)

// GetRunningTrader 返回该用户当前运行中的 AutoTrader（若有）
func (ttm *TelegramTraderManager) GetRunningTrader(telegramID int64) (*trader.AutoTrader, error) {
	tgTraders, err := ttm.db.GetTgTraders(telegramID)
	if err != nil {
		return nil, fmt.Errorf("获取交易员失败: %w", err)
	}
	for _, t := range tgTraders {
		if !t.IsRunning {
			continue
		}
		tr, err := ttm.GetTgTrader(t.ID)
		if err != nil || tr == nil {
			continue
		}
		if tr.IsRunning() {
			return tr, nil
		}
	}
	return nil, fmt.Errorf("未找到运行中的交易员")
}

// GetPositionLevelsFromTrader 获取指定 symbol 的持仓关键信息（若存在持仓）
func GetPositionLevelsFromTrader(at *trader.AutoTrader, symbol string) (entryPrice float64, side string, stopLoss float64, takeProfit float64) {
	if at == nil {
		return 0, "", 0, 0
	}
	target := normalizeSymbol(symbol)
	positions, err := at.GetPositions()
	if err != nil {
		return 0, "", 0, 0
	}
	for _, pos := range positions {
		sym, _ := pos["symbol"].(string)
		if !symbolMatch(sym, target) {
			continue
		}

		if v, ok := pos["side"].(string); ok {
			side = strings.ToLower(strings.TrimSpace(v))
		}

		// AutoTrader.GetPositions() 返回 snake_case；部分交易器/历史实现可能返回驼峰字段
		if v, ok := pos["entry_price"].(float64); ok {
			entryPrice = v
		} else if v, ok := pos["entryPrice"].(float64); ok {
			entryPrice = v
		} else if v, ok := pos["avgPrice"].(float64); ok {
			entryPrice = v
		} else if v, ok := pos["entry_px"].(float64); ok {
			entryPrice = v
		}

		if v, ok := pos["stop_loss"].(float64); ok {
			stopLoss = v
		} else if v, ok := pos["stopLoss"].(float64); ok {
			stopLoss = v
		}
		if v, ok := pos["take_profit"].(float64); ok {
			takeProfit = v
		} else if v, ok := pos["takeProfit"].(float64); ok {
			takeProfit = v
		}

		return entryPrice, side, stopLoss, takeProfit
	}
	return 0, "", 0, 0
}

// GetPositionFromTrader 获取指定 symbol 的持仓信息（若存在持仓）
func GetPositionFromTrader(at *trader.AutoTrader, symbol string) (entryPrice float64, side string) {
	entryPrice, side, _, _ = GetPositionLevelsFromTrader(at, symbol)
	return entryPrice, side
}

// GetEntryPriceFromTrader 获取指定 symbol 的持仓均价（若存在持仓）
func GetEntryPriceFromTrader(at *trader.AutoTrader, symbol string) float64 {
	ep, _, _, _ := GetPositionLevelsFromTrader(at, symbol)
	return ep
}

func normalizeSymbol(sym string) string {
	s := strings.ToUpper(strings.TrimSpace(sym))
	s = strings.TrimSuffix(s, "USDT")
	s = strings.TrimSuffix(s, "USDC")
	// 对于 HIP-3，去掉前缀，只保留资产名用于匹配 Binance 符号
	if strings.Contains(s, ":") {
		parts := strings.SplitN(s, ":", 2)
		s = parts[len(parts)-1]
	}
	return s
}

func symbolMatch(posSym, target string) bool {
	if target == "" {
		return false
	}
	p := normalizeSymbol(posSym)
	return p == target || p+"USDT" == target || p+"USDC" == target
}
