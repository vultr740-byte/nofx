package telegram

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"nofx/charts"
	"nofx/market"
)

var chartIntervals = []string{"15m", "1h", "4h", "1d", "3d", "7d"}

// handleChart 处理 /chart 命令，格式：/chart SYMBOL [interval]
func (tbm *TelegramBotManager) handleChart(update tgbotapi.Update) {
	chatID := update.Message.Chat.ID
	telegramID := update.Message.From.ID

	// 权限校验
	userAny, userErr := tbm.db.GetTGUserByTelegramID(telegramID)
	if userErr != nil {
		tbm.sendMessage(chatID, "❌ 请先使用 /start 初始化账号")
		return
	}
	if !tbm.hasHyperliquidAccount(telegramID) {
		tbm.sendMessage(chatID, "❌ 请先使用 /start 完成账号初始化")
		return
	}

	// 推断用户时区（优先使用 DB 存储的 language_code，再尝试 Telegram 的语言，最终回退 UTC）
	lang := ""
	if userMap, ok := userAny.(map[string]interface{}); ok {
		if lc, ok := userMap["language_code"].(string); ok {
			lang = lc
		}
	}
	if lang == "" && update.Message != nil && update.Message.From != nil {
		lang = update.Message.From.LanguageCode
	}
	loc := inferLocationFromLanguage(lang)

	// 解析参数
	args := strings.Fields(strings.TrimSpace(update.Message.CommandArguments()))
	if len(args) == 0 {
		tbm.sendMessage(chatID, "用法: /chart BTC [interval]，例如 /chart BTC 15m")
		return
	}

	rawSymbol := strings.ToUpper(strings.TrimSpace(args[0]))
	bnSymbol, ok := market.ToBinanceSymbol(rawSymbol)
	if !ok {
		tbm.sendMessage(chatID, "❌ 不支持的币种/交易对（目前仅支持 Binance 可交易的加密资产），示例：/chart BTC 15m")
		return
	}
	asset := chartAssetName(bnSymbol)

	interval := "15m"
	if len(args) >= 2 {
		interval = strings.ToLower(args[1])
	}

	entryPrice, entrySide, stopLoss, takeProfit := tbm.getChartPositionLevels(telegramID, bnSymbol)

	tbm.sendMessage(chatID, fmt.Sprintf("🔄 正在生成 %s %s 价格走势图...", asset, interval))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	gen := charts.NewGenerator()
	png, err := gen.BuildKlinePNG(ctx, bnSymbol, interval, 150, entryPrice, stopLoss, takeProfit, loc)
	if err != nil {
		log.Printf("生成图表失败: %v", err)
		tbm.sendMessage(chatID, fmt.Sprintf("❌ 生成图表失败: %v", err))
		return
	}

	photo := tgbotapi.FileBytes{Bytes: png, Name: fmt.Sprintf("%s_%s.png", asset, interval)}
	msg := tgbotapi.NewPhoto(chatID, photo)
	msg.Caption = buildChartCaption(asset, interval, entryPrice, stopLoss, takeProfit, entrySide)
	msg.ReplyMarkup = buildChartIntervalKeyboard(telegramID, bnSymbol, interval)
	if _, err := tbm.bot.Send(msg); err != nil {
		log.Printf("发送图表失败: %v", err)
		tbm.sendMessage(chatID, "❌ 发送图表失败")
	}
}

// handleChartIntervalCallback 处理 /chart 时间维度切换
func (tbm *TelegramBotManager) handleChartIntervalCallback(callback *tgbotapi.CallbackQuery, chatID int64, telegramID int64, parts []string) {
	if callback == nil || callback.Message == nil {
		return
	}
	if len(parts) < 4 {
		tbm.answerCallbackQuery(callback.ID, "请求格式错误")
		return
	}
	rawSymbol := strings.ToUpper(strings.TrimSpace(parts[2]))
	interval := strings.ToLower(strings.TrimSpace(parts[3]))
	if !isChartIntervalSupported(interval) {
		tbm.answerCallbackQuery(callback.ID, "不支持的时间维度")
		return
	}
	bnSymbol, ok := market.ToBinanceSymbol(rawSymbol)
	if !ok {
		tbm.answerCallbackQuery(callback.ID, "不支持的币种/交易对")
		return
	}
	asset := chartAssetName(bnSymbol)

	tbm.answerCallbackQuery(callback.ID, fmt.Sprintf("✅ 已切换到 %s", interval))

	// 推断用户时区（优先使用 DB 存储的 language_code，再尝试 Telegram 的语言，最终回退 UTC）
	lang := ""
	if userAny, userErr := tbm.db.GetTGUserByTelegramID(telegramID); userErr == nil {
		if userMap, ok := userAny.(map[string]interface{}); ok {
			if lc, ok := userMap["language_code"].(string); ok {
				lang = lc
			}
		}
	}
	if lang == "" && callback.From != nil {
		lang = callback.From.LanguageCode
	}
	loc := inferLocationFromLanguage(lang)

	entryPrice, entrySide, stopLoss, takeProfit := tbm.getChartPositionLevels(telegramID, bnSymbol)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	gen := charts.NewGenerator()
	png, err := gen.BuildKlinePNG(ctx, bnSymbol, interval, 150, entryPrice, stopLoss, takeProfit, loc)
	if err != nil {
		log.Printf("生成图表失败: %v", err)
		tbm.sendMessage(chatID, fmt.Sprintf("❌ 生成图表失败: %v", err))
		return
	}

	caption := buildChartCaption(asset, interval, entryPrice, stopLoss, takeProfit, entrySide)
	keyboard := buildChartIntervalKeyboard(telegramID, bnSymbol, interval)
	photo := tgbotapi.FileBytes{Bytes: png, Name: fmt.Sprintf("%s_%s.png", asset, interval)}
	media := tgbotapi.NewInputMediaPhoto(photo)
	media.Caption = caption

	editConfig := tgbotapi.EditMessageMediaConfig{
		BaseEdit: tgbotapi.BaseEdit{
			ChatID:      chatID,
			MessageID:   callback.Message.MessageID,
			ReplyMarkup: &keyboard,
		},
		Media: media,
	}
	if _, err := tbm.bot.Request(editConfig); err != nil {
		log.Printf("更新图表失败: %v", err)
		msg := tgbotapi.NewPhoto(chatID, photo)
		msg.Caption = caption
		msg.ReplyMarkup = keyboard
		if _, sendErr := tbm.bot.Send(msg); sendErr != nil {
			log.Printf("发送图表失败: %v", sendErr)
		}
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

// buildChartCaption 构建图表文案（含可选的入场/止损/止盈）
func buildChartCaption(asset, interval string, entryPrice, stopLoss, takeProfit float64, entrySide string) string {
	caption := fmt.Sprintf("%s %s 价格走势", asset, interval)
	if entryPrice > 0 {
		caption += fmt.Sprintf("\n入场价: %.4f%s", entryPrice, sideText(entrySide))
	}
	if stopLoss > 0 {
		caption += fmt.Sprintf("\n止损价: %.4f", stopLoss)
	}
	if takeProfit > 0 {
		caption += fmt.Sprintf("\n止盈价: %.4f", takeProfit)
	}
	return caption
}

// buildChartIntervalKeyboard 构建图表时间维度按钮
func buildChartIntervalKeyboard(telegramID int64, symbol, interval string) tgbotapi.InlineKeyboardMarkup {
	rows := [][]tgbotapi.InlineKeyboardButton{}
	row := []tgbotapi.InlineKeyboardButton{}
	for i, iv := range chartIntervals {
		label := iv
		if iv == interval {
			label = "✅ " + iv
		}
		row = append(row, tgbotapi.NewInlineKeyboardButtonData(
			label,
			fmt.Sprintf("chart_interval|%d|%s|%s", telegramID, symbol, iv),
		))
		if (i+1)%3 == 0 {
			rows = append(rows, row)
			row = []tgbotapi.InlineKeyboardButton{}
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

// isChartIntervalSupported 校验时间维度白名单
func isChartIntervalSupported(interval string) bool {
	for _, iv := range chartIntervals {
		if iv == interval {
			return true
		}
	}
	return false
}

// chartAssetName 提取资产简称（如 BTCUSDT -> BTC）
func chartAssetName(bnSymbol string) string {
	asset := bnSymbol
	for _, suffix := range []string{"USDT", "USDC", "BUSD", "USD"} {
		if strings.HasSuffix(asset, suffix) {
			return strings.TrimSuffix(asset, suffix)
		}
	}
	return asset
}

// getChartPositionLevels 获取持仓价格信息
func (tbm *TelegramBotManager) getChartPositionLevels(telegramID int64, bnSymbol string) (entryPrice float64, entrySide string, stopLoss float64, takeProfit float64) {
	// 优先运行中的交易员
	if at, err := tbm.tgTraderMgr.GetRunningTrader(telegramID); err == nil && at != nil {
		if ep, side, sl, tp := GetPositionLevelsFromTrader(at, bnSymbol); ep > 0 || sl > 0 || tp > 0 {
			return ep, side, sl, tp
		}
	}
	// 回退：直接查账户持仓（即便未运行）
	if agentKey, walletAddr, err := tbm.extractAgentKeyAndWallet(telegramID); err == nil {
		if _, positions, err := tbm.hlService.GetPositionsWithData(agentKey, walletAddr, tbm.testnet); err == nil {
			if ep, side, sl, tp := findPositionInPositions(positions, bnSymbol); ep > 0 || sl > 0 || tp > 0 {
				return ep, side, sl, tp
			}
		}
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
