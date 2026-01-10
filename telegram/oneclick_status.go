package telegram

import (
	"context"
	"fmt"
	"math/big"
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
	depositAddress := ""
	depositMemo := ""
	if len(args) >= 1 {
		depositAddress = args[0]
		if len(args) >= 2 {
			depositMemo = args[1]
		}
	} else {
		// 允许不传参：使用最近一次跨链充值生成的地址
		session := tbm.tgTraderMgr.GetSessionManager().GetOrCreateSession(telegramID)
		params := tbm.ensureSessionParams(session)
		if v, ok := params[oneClickParamLastDepositAddress].(string); ok {
			depositAddress = strings.TrimSpace(v)
		}
		if v, ok := params[oneClickParamLastDepositMemo].(string); ok {
			depositMemo = strings.TrimSpace(v)
		}
		if depositAddress == "" {
			tbm.sendMessage(chatID, "用法: /deposit_status <depositAddress> [depositMemo]\n\n也可以先使用 /deposit → 跨链充值 USDC 生成充值地址，然后点击“📦 查看状态”。")
			return
		}
	}

	tbm.sendOneClickDepositStatus(chatID, depositAddress, depositMemo)
}

func (tbm *TelegramBotManager) handleDepositStatusLastCallback(callback *tgbotapi.CallbackQuery, chatID int64, telegramID int64) {
	tbm.answerCallbackQuery(callback.ID, "⏳ 查询中...")

	if tbm.oneClick == nil {
		tbm.sendMessage(chatID, "❌ 跨链充值服务未初始化")
		return
	}

	session := tbm.tgTraderMgr.GetSessionManager().GetOrCreateSession(telegramID)
	params := tbm.ensureSessionParams(session)
	depositAddress, _ := params[oneClickParamLastDepositAddress].(string)
	depositMemo, _ := params[oneClickParamLastDepositMemo].(string)
	depositAddress = strings.TrimSpace(depositAddress)
	depositMemo = strings.TrimSpace(depositMemo)

	if depositAddress == "" {
		tbm.sendMessage(chatID, "❌ 未找到最近一次跨链充值信息，请重新使用 /deposit 生成充值地址")
		return
	}

	tbm.sendOneClickDepositStatus(chatID, depositAddress, depositMemo)
}

func (tbm *TelegramBotManager) sendOneClickDepositStatus(chatID int64, depositAddress string, depositMemo string) {
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

	depositedAmt := strings.TrimSpace(statusResp.SwapDetails.DepositedAmountFormatted)
	if depositedAmt == "" {
		depositedAmt = "0"
	}
	amountOut := strings.TrimSpace(statusResp.SwapDetails.AmountOutFormatted)
	if amountOut == "" {
		amountOut = strings.TrimSpace(statusResp.QuoteResponse.Quote.AmountOutFmt)
	}

	statusUpper := strings.ToUpper(strings.TrimSpace(statusResp.Status))
	isPendingOrIncomplete := statusUpper == "PENDING_DEPOSIT" || statusUpper == "INCOMPLETE_DEPOSIT" || statusUpper == "KNOWN_DEPOSIT_TX"
	if isPendingOrIncomplete {
		if ok, r := parseDecimal(depositedAmt); ok && r.Cmp(big.NewRat(20, 1)) < 0 {
			msg := fmt.Sprintf(`📦 跨链充值状态

状态：%s
更新时间：%s

已充值：%s USDC（未达到最小充值 %s USDC）

💡 你可以继续向同一地址补充充值，达到最小充值后会自动开始处理。`,
				esc(tbm.oneClickStatusText(statusResp.Status)),
				esc(statusResp.UpdatedAt),
				esc(depositedAmt),
				esc(oneClickMinDepositUSDC),
			)
			tbm.sendMessage(chatID, msg)
			return
		}
	}

	msg := fmt.Sprintf(`📦 跨链充值状态

状态：%s
更新时间：%s

输入资产：%s
输出资产：%s
收款地址：%s
已充值：%s
到账金额：%s

💡 若状态为“成功”，可执行 /balance 触发自动充值到 Hyperliquid（若已启用）。`,
		esc(tbm.oneClickStatusText(statusResp.Status)),
		esc(statusResp.UpdatedAt),
		esc(originAsset),
		esc(destAsset),
		esc(recipient),
		esc(depositedAmt),
		esc(amountOut),
	)
	tbm.sendMessage(chatID, msg)
}
