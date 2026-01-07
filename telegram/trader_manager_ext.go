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

// GetEntryPriceFromTrader 获取指定 symbol 的持仓均价（若存在持仓）
func GetEntryPriceFromTrader(at *trader.AutoTrader, symbol string) float64 {
	if at == nil {
		return 0
	}
	target := normalizeSymbol(symbol)
	positions, err := at.GetPositions()
	if err != nil {
		return 0
	}
	for _, pos := range positions {
		sym, _ := pos["symbol"].(string)
		if !symbolMatch(sym, target) {
			continue
		}
		if ep, ok := pos["entryPrice"].(float64); ok {
			return ep
		}
	}
	return 0
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
