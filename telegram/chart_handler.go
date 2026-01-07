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
    if at, err := tbm.tgTraderMgr.GetRunningTrader(telegramID); err == nil && at != nil {
        if posEntry := GetEntryPriceFromTrader(at, symbol); posEntry > 0 {
            entryPrice = posEntry
        }
    }

	tbm.sendMessage(chatID, fmt.Sprintf("🔄 正在生成 %s %s K线图...", symbol, interval))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	gen := charts.NewGenerator()
	png, err := gen.BuildKlinePNG(ctx, symbol, interval, 150, entryPrice)
	if err != nil {
		log.Printf("生成图表失败: %v", err)
		tbm.sendMessage(chatID, fmt.Sprintf("❌ 生成图表失败: %v", err))
		return
	}

	photo := tgbotapi.FileBytes{Bytes: png, Name: fmt.Sprintf("%s_%s.png", symbol, interval)}
	msg := tgbotapi.NewPhoto(chatID, photo)
	msg.Caption = fmt.Sprintf("%s %s K线图", symbol, interval)
	if entryPrice > 0 {
		msg.Caption += fmt.Sprintf("\n标注买入价: %.4f", entryPrice)
	}
	if _, err := tbm.bot.Send(msg); err != nil {
		log.Printf("发送图表失败: %v", err)
		tbm.sendMessage(chatID, "❌ 发送图表失败")
	}
}
