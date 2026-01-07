package telegram

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"nofx/charts"
)

// handleChart 处理 /chart 命令，格式：/chart SYMBOL [interval]
func (tbm *TelegramBotManager) handleChart(update tgbotapi.Update) {
	chatID := update.Message.Chat.ID
	telegramID := update.Message.From.ID

	// 权限校验
	if _, err := tbm.db.GetTGUserByTelegramID(telegramID); err != nil {
		tbm.sendMessage(chatID, "❌ 请先使用 /start 初始化账号")
		return
	}
	if !tbm.hasHyperliquidAccount(telegramID) {
		tbm.sendMessage(chatID, "❌ 请先使用 /start 完成账号初始化")
		return
	}

	// 解析参数
	args := strings.Fields(strings.TrimSpace(update.Message.CommandArguments()))
	if len(args) == 0 {
		tbm.sendMessage(chatID, "用法: /chart BTCUSDT [interval]，例如 /chart BTCUSDT 15m")
		return
	}
	symbol := strings.ToUpper(args[0])
	interval := "15m"
	if len(args) >= 2 {
		interval = strings.ToLower(args[1])
	}

	// 可选：从持仓获取买入价
	var entryPrice float64
	var entrySide string
	var stopLoss float64
	var takeProfit float64
	// 优先运行中的交易员
	if at, err := tbm.tgTraderMgr.GetRunningTrader(telegramID); err == nil && at != nil {
		if ep, side, sl, tp := GetPositionLevelsFromTrader(at, symbol); ep > 0 || sl > 0 || tp > 0 {
			entryPrice = ep
			entrySide = side
			stopLoss = sl
			takeProfit = tp
		}
	}
	// 回退：直接查账户持仓（即便未运行）
	if entryPrice == 0 || (stopLoss == 0 && takeProfit == 0) {
		if agentKey, walletAddr, err := tbm.extractAgentKeyAndWallet(telegramID); err == nil {
			if _, positions, err := tbm.hlService.GetPositionsWithData(agentKey, walletAddr, tbm.testnet); err == nil {
				if ep, side, sl, tp := findPositionInPositions(positions, symbol); ep > 0 || sl > 0 || tp > 0 {
					if entryPrice == 0 {
						entryPrice = ep
					}
					if entrySide == "" {
						entrySide = side
					}
					if stopLoss == 0 {
						stopLoss = sl
					}
					if takeProfit == 0 {
						takeProfit = tp
					}
				}
			}
		}
	}

	tbm.sendMessage(chatID, fmt.Sprintf("🔄 正在生成 %s %s 价格走势图...", symbol, interval))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	gen := charts.NewGenerator()
	png, err := gen.BuildKlinePNG(ctx, symbol, interval, 150, entryPrice, stopLoss, takeProfit)
	if err != nil {
		log.Printf("生成图表失败: %v", err)
		tbm.sendMessage(chatID, fmt.Sprintf("❌ 生成图表失败: %v", err))
		return
	}

	photo := tgbotapi.FileBytes{Bytes: png, Name: fmt.Sprintf("%s_%s.png", symbol, interval)}
	msg := tgbotapi.NewPhoto(chatID, photo)
	msg.Caption = fmt.Sprintf("%s %s 价格走势", symbol, interval)
	if entryPrice > 0 {
		msg.Caption += fmt.Sprintf("\n入场价: %.4f%s", entryPrice, sideText(entrySide))
	}
	if stopLoss > 0 {
		msg.Caption += fmt.Sprintf("\n止损价: %.4f", stopLoss)
	}
	if takeProfit > 0 {
		msg.Caption += fmt.Sprintf("\n止盈价: %.4f", takeProfit)
	}
	if _, err := tbm.bot.Send(msg); err != nil {
		log.Printf("发送图表失败: %v", err)
		tbm.sendMessage(chatID, "❌ 发送图表失败")
	}
}

// findPositionInPositions 在 positions 列表中匹配 symbol，并返回 entryPrice/side/stopLoss/takeProfit
func findPositionInPositions(positions []map[string]interface{}, symbol string) (entryPrice float64, side string, stopLoss float64, takeProfit float64) {
	target := normalizeSymbol(symbol)
	for _, pos := range positions {
		sym, _ := pos["symbol"].(string)
		if !symbolMatch(sym, target) {
			continue
		}
		if v, ok := pos["side"].(string); ok {
			side = strings.ToLower(strings.TrimSpace(v))
		}
		// 常见字段兼容
		if ep, ok := pos["entryPrice"].(float64); ok {
			entryPrice = ep
		}
		if ep, ok := pos["avgPrice"].(float64); ok {
			entryPrice = ep
		}
		if ep, ok := pos["entry_px"].(float64); ok {
			entryPrice = ep
		}

		if v, ok := pos["stopLoss"].(float64); ok {
			stopLoss = v
		} else if v, ok := pos["stop_loss"].(float64); ok {
			stopLoss = v
		}
		if v, ok := pos["takeProfit"].(float64); ok {
			takeProfit = v
		} else if v, ok := pos["take_profit"].(float64); ok {
			takeProfit = v
		}

		return entryPrice, side, stopLoss, takeProfit
	}
	return 0, "", 0, 0
}

func sideText(side string) string {
	switch strings.ToLower(strings.TrimSpace(side)) {
	case "long":
		return "（多仓）"
	case "short":
		return "（空仓）"
	default:
		return ""
	}
}
