package telegram

import (
	"context"
	"fmt"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func (tbm *TelegramBotManager) handleDepositStatus(update tgbotapi.Update) {
	chatID := update.Message.Chat.ID
	telegramID := update.Message.From.ID

	if tbm.oneClick == nil {
		tbm.sendMessage(chatID, "❌ 跨链充值服务未初始化")
		return
	}

	// 仅校验用户存在（不强制要求已创建交易员，方便用户查询历史）
	if _, err := tbm.db.GetTGUserByTelegramID(telegramID); err != nil {
		tbm.sendMessage(chatID, "❌ 请先使用 /start 初始化账号")
		return
	}

	args := strings.Fields(strings.TrimSpace(update.Message.CommandArguments()))
	if len(args) < 1 {
		tbm.sendMessage(chatID, "用法: /deposit_status <depositAddress> [depositMemo]")
		return
	}
	depositAddress := args[0]
	depositMemo := ""
	if len(args) >= 2 {
		depositMemo = args[1]
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	statusResp, err := tbm.oneClick.GetStatus(ctx, depositAddress, depositMemo)
	if err != nil {
		tbm.sendMessage(chatID, fmt.Sprintf("❌ 查询失败: %s", esc(err)))
		return
	}

	originAsset := statusResp.QuoteResponse.QuoteRequest.OriginAsset
	destAsset := statusResp.QuoteResponse.QuoteRequest.DestinationAsset
	recipient := statusResp.QuoteResponse.QuoteRequest.Recipient
	amountIn := statusResp.QuoteResponse.Quote.AmountInFmt
	amountOut := statusResp.QuoteResponse.Quote.AmountOutFmt

	msg := fmt.Sprintf(`📦 跨链充值状态

状态：%s
更新时间：%s

输入资产：%s
输出资产：%s
收款地址：%s
输入金额：%s
预计输出：%s

💡 若状态为“成功”，可执行 /balance 触发自动充值到 Hyperliquid（若已启用）。`,
		esc(tbm.oneClickStatusText(statusResp.Status)),
		esc(statusResp.UpdatedAt),
		esc(originAsset),
		esc(destAsset),
		esc(recipient),
		esc(amountIn),
		esc(amountOut),
	)
	tbm.sendMessage(chatID, msg)
}
