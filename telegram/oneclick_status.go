package telegram

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const oneClickParamStatusMsgCtx = "oneclick_status_msg_ctx"

type oneClickStatusMsgContext struct {
	DepositAddress string
	DepositMemo    string
}

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
			tbm.sendMessage(chatID, "用法: /deposit_status <depositAddress> [depositMemo]\n\n也可以先使用 /deposit → 跨链充值 USDC 生成充值地址，然后点击“✅ 已完成充值”。")
			return
		}
	}

	tbm.sendOneClickDepositStatus(chatID, telegramID, depositAddress, depositMemo)
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

	tbm.sendOneClickDepositStatus(chatID, telegramID, depositAddress, depositMemo)
}

func (tbm *TelegramBotManager) handleDepositStatusRefreshCallback(callback *tgbotapi.CallbackQuery, chatID int64, telegramID int64) {
	// 先结束客户端的 loading 动画（避免用户觉得“卡住了”）
	tbm.answerCallbackQuery(callback.ID, "")

	if tbm.oneClick == nil {
		tbm.sendMessage(chatID, "❌ 跨链充值服务未初始化")
		return
	}
	if callback.Message == nil {
		return
	}

	msgCtx, ok := tbm.getOneClickStatusMsgContext(telegramID, callback.Message.MessageID)
	if !ok {
		tbm.sendMessage(chatID, "❌ 刷新失败：请重新执行 /deposit_status")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	msg, statusUpper, err := tbm.buildOneClickDepositStatusMessage(ctx, telegramID, msgCtx.DepositAddress, msgCtx.DepositMemo)
	if err != nil {
		tbm.sendMessage(chatID, fmt.Sprintf("❌ 刷新失败: %s", esc(err)))
		return
	}

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔄 刷新", fmt.Sprintf("deposit_status_refresh|%d", telegramID)),
		),
	)
	tbm.editCallbackMessageWithInlineKeyboard(callback.Message.MessageID, chatID, msg, keyboard)

	// 自动触发一次 Arbitrum -> Hyperliquid 充值（避免用户还要再点 /balance）
	if statusUpper == "SUCCESS" {
		if tbm.arbService == nil {
			return
		}
		agentKey, walletAddr, err := tbm.extractAgentKeyAndWallet(telegramID)
		if err != nil {
			return
		}
		go tbm.tryAutoBridge(telegramID, chatID, agentKey, walletAddr)
	}
}

func (tbm *TelegramBotManager) sendOneClickDepositStatus(chatID int64, telegramID int64, depositAddress string, depositMemo string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	msg, statusUpper, err := tbm.buildOneClickDepositStatusMessage(ctx, telegramID, depositAddress, depositMemo)
	if err != nil {
		tbm.sendMessage(chatID, fmt.Sprintf("❌ 查询失败: %s", esc(err)))
		return
	}

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔄 刷新", fmt.Sprintf("deposit_status_refresh|%d", telegramID)),
		),
	)
	sentMsg, err := tbm.sendMessageWithMarkupAndReturn(chatID, msg, keyboard)
	if err == nil && sentMsg != nil {
		tbm.setOneClickStatusMsgContext(telegramID, sentMsg.MessageID, depositAddress, depositMemo)
	} else {
		tbm.sendMessage(chatID, msg)
	}

	// 自动触发一次 Arbitrum -> Hyperliquid 充值（避免用户还要再点 /balance）
	if statusUpper == "SUCCESS" {
		if tbm.arbService == nil {
			return
		}
		agentKey, walletAddr, err := tbm.extractAgentKeyAndWallet(telegramID)
		if err != nil {
			return
		}
		go tbm.tryAutoBridge(telegramID, chatID, agentKey, walletAddr)
	}
}

func (tbm *TelegramBotManager) buildOneClickDepositStatusMessage(ctx context.Context, telegramID int64, depositAddress string, depositMemo string) (string, string, error) {
	statusResp, err := tbm.oneClick.GetStatus(ctx, depositAddress, depositMemo)
	if err != nil {
		return "", "", err
	}

	originAsset := statusResp.QuoteResponse.QuoteRequest.OriginAsset
	destAsset := statusResp.QuoteResponse.QuoteRequest.DestinationAsset
	recipient := statusResp.QuoteResponse.QuoteRequest.Recipient

	deadlineStr := strings.TrimSpace(statusResp.QuoteResponse.Quote.Deadline)
	inactiveStr := strings.TrimSpace(statusResp.QuoteResponse.Quote.TimeWhenInactive)

	depositedAmt := strings.TrimSpace(statusResp.SwapDetails.DepositedAmountFormatted)
	if depositedAmt == "" {
		depositedAmt = "0"
	}
	amountOut := strings.TrimSpace(statusResp.SwapDetails.AmountOutFormatted)
	if amountOut == "" {
		amountOut = strings.TrimSpace(statusResp.QuoteResponse.Quote.AmountOutFmt)
	}

	minDeposit := oneClickMinDepositUSDC
	if v := strings.TrimSpace(statusResp.QuoteResponse.Quote.MinAmountIn); v != "" {
		decimals := 6
		originAssetID := strings.TrimSpace(originAsset)
		if originAssetID != "" {
			if toks, err := tbm.oneClick.GetTokens(ctx); err == nil {
				for _, tok := range toks {
					if strings.TrimSpace(tok.AssetID) == originAssetID {
						decimals = tok.Decimals
						break
					}
				}
			}
		}
		if s, err := baseUnitsToDecimal(v, decimals); err == nil && strings.TrimSpace(s) != "" {
			minDeposit = strings.TrimSpace(s)
		}
	}

	updatedAtText := strings.TrimSpace(statusResp.UpdatedAt)
	if t, ok := parseRFC3339Time(updatedAtText); ok {
		updatedAtText = t.Format("2006-01-02 15:04")
	}

	remainText := ""
	isExpired := false
	if _, t, ok := pickSoonerTime(deadlineStr, inactiveStr); ok {
		remain := formatDurationCN(time.Until(t))
		if remain == "已过期" {
			isExpired = true
		} else {
			remainText = remain
		}
	}

	statusUpper := strings.ToUpper(strings.TrimSpace(statusResp.Status))
	isPendingOrIncomplete := statusUpper == "PENDING_DEPOSIT" || statusUpper == "INCOMPLETE_DEPOSIT" || statusUpper == "KNOWN_DEPOSIT_TX"
	if isPendingOrIncomplete {
		if isExpired {
			return `📦 <b>跨链充值状态</b>

🚫 <b>地址已失效</b>
请重新使用 /deposit 生成新地址。`, statusUpper, nil
		}

		needsTopUp := false
		minRat := new(big.Rat).SetInt64(1)
		if ok, v := parseDecimal(minDeposit); ok {
			minRat = v
		}
		if ok, r := parseDecimal(depositedAmt); ok && r.Cmp(minRat) < 0 {
			needsTopUp = true
		}

		lines := []string{
			"📦 <b>跨链充值状态</b>",
			"",
			fmt.Sprintf("状态：%s", esc(tbm.oneClickStatusText(statusResp.Status))),
			fmt.Sprintf("已充值：%s USDC", esc(depositedAmt)),
			fmt.Sprintf("最小充值：%s USDC", esc(minDeposit)),
		}
		if remainText != "" {
			lines = append(lines, fmt.Sprintf("剩余时间：%s", esc(remainText)))
		}
		lines = append(lines, fmt.Sprintf("更新时间：%s", esc(updatedAtText)))
		if needsTopUp {
			lines = append(lines, "", "💡 可继续向同一地址补充充值，达到最小充值后会自动开始处理。")
		}

		return strings.Join(lines, "\n"), statusUpper, nil
	}

	lines := []string{
		"📦 <b>跨链充值状态</b>",
		"",
		fmt.Sprintf("状态：%s", esc(tbm.oneClickStatusText(statusResp.Status))),
		fmt.Sprintf("更新时间：%s", esc(updatedAtText)),
	}
	if remainText != "" {
		lines = append(lines, fmt.Sprintf("剩余时间：%s", esc(remainText)))
	}
	lines = append(lines,
		"",
		fmt.Sprintf("输入资产：%s", esc(originAsset)),
		fmt.Sprintf("输出资产：%s", esc(destAsset)),
		fmt.Sprintf("收款地址：%s", esc(recipient)),
		fmt.Sprintf("已充值：%s", esc(depositedAmt)),
		fmt.Sprintf("到账金额：%s", esc(amountOut)),
		"",
		"💡 若状态为“成功”，可执行 /balance 触发自动充值到 Hyperliquid（若已启用）。",
	)

	return strings.Join(lines, "\n"), statusUpper, nil
}

func (tbm *TelegramBotManager) setOneClickStatusMsgContext(telegramID int64, messageID int, depositAddress string, depositMemo string) {
	sessionMgr := tbm.tgTraderMgr.GetSessionManager()
	session := sessionMgr.GetOrCreateSession(telegramID)
	params := tbm.ensureSessionParams(session)

	m, _ := params[oneClickParamStatusMsgCtx].(map[int]oneClickStatusMsgContext)
	if m == nil {
		m = make(map[int]oneClickStatusMsgContext)
	}
	m[messageID] = oneClickStatusMsgContext{
		DepositAddress: strings.TrimSpace(depositAddress),
		DepositMemo:    strings.TrimSpace(depositMemo),
	}
	// 简单限流：避免长期使用导致 map 膨胀
	if len(m) > 50 {
		for k := range m {
			if k == messageID {
				continue
			}
			delete(m, k)
			break
		}
	}

	params[oneClickParamStatusMsgCtx] = m
	sessionMgr.UpdateTraderConfig(telegramID, session.TraderConfig)
}

func (tbm *TelegramBotManager) getOneClickStatusMsgContext(telegramID int64, messageID int) (oneClickStatusMsgContext, bool) {
	session := tbm.tgTraderMgr.GetSessionManager().GetOrCreateSession(telegramID)
	params := tbm.ensureSessionParams(session)

	m, ok := params[oneClickParamStatusMsgCtx].(map[int]oneClickStatusMsgContext)
	if !ok || m == nil {
		return oneClickStatusMsgContext{}, false
	}
	msgCtx, ok := m[messageID]
	if !ok || strings.TrimSpace(msgCtx.DepositAddress) == "" {
		return oneClickStatusMsgContext{}, false
	}
	return msgCtx, true
}
