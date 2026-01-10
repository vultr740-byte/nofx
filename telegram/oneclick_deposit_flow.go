package telegram

import (
	"context"
	"fmt"
	"log"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const (
	oneClickParamWalletAddr       = "oneclick_wallet_addr"
	oneClickParamOriginChain      = "oneclick_origin_chain"
	oneClickParamDestinationAsset = "oneclick_destination_asset"

	oneClickParamLastDepositAddress = "oneclick_last_deposit_addr"
	oneClickParamLastDepositMemo    = "oneclick_last_deposit_memo"
	oneClickParamLastDepositChain   = "oneclick_last_deposit_chain"
)

const oneClickMinDepositUSDC = "1"

func chainDisplayName(chain string) string {
	switch strings.ToLower(strings.TrimSpace(chain)) {
	case "eth":
		return "Ethereum"
	case "arb":
		return "Arbitrum"
	case "base":
		return "Base"
	case "op":
		return "Optimism"
	case "pol":
		return "Polygon"
	case "bsc":
		return "BSC"
	case "avax":
		return "Avalanche"
	case "sol":
		return "Solana"
	case "near":
		return "NEAR"
	case "sui":
		return "Sui"
	case "stellar":
		return "Stellar"
	case "gnosis":
		return "Gnosis"
	case "monad":
		return "Monad"
	case "xlayer":
		return "X Layer"
	default:
		if chain == "" {
			return "Unknown"
		}
		return strings.ToUpper(chain[:1]) + chain[1:]
	}
}

func isEVMChain(chain string) bool {
	switch strings.ToLower(strings.TrimSpace(chain)) {
	case "eth", "arb", "base", "op", "pol", "bsc", "avax", "gnosis", "bera", "xlayer", "monad", "adi":
		return true
	default:
		return false
	}
}

func (tbm *TelegramBotManager) ensureSessionParams(session *UserSession) map[string]interface{} {
	if session.TraderConfig.CustomParams == nil {
		session.TraderConfig.CustomParams = make(map[string]interface{})
	}
	return session.TraderConfig.CustomParams
}

func (tbm *TelegramBotManager) sendOneClickChainSelection(chatID int64, telegramID int64, page int) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if tbm.oneClick == nil {
		tbm.sendMessage(chatID, "❌ 跨链充值服务未初始化")
		return
	}

	chainToToken, err := tbm.oneClick.GetUSDCTokensByChain(ctx)
	if err != nil {
		tbm.sendMessage(chatID, fmt.Sprintf("❌ 获取可用网络失败: %s", esc(err)))
		return
	}

	// 目标资产固定为 Arbitrum USDC
	dest, ok := chainToToken["arb"]
	if !ok || dest.AssetID == "" {
		tbm.sendMessage(chatID, "❌ 1Click 未返回 Arbitrum USDC 资产信息，暂无法使用跨链充值")
		return
	}

	chains := make([]string, 0, len(chainToToken))
	for chain := range chainToToken {
		if !isEVMChain(chain) {
			continue
		}
		// Arbitrum 作为目标链，仍允许选择，但 UI 上建议直充
		chains = append(chains, chain)
	}
	sort.Strings(chains)

	const pageSize = 10
	if page < 0 {
		page = 0
	}
	totalPages := (len(chains) + pageSize - 1) / pageSize
	if totalPages == 0 {
		totalPages = 1
	}
	if page >= totalPages {
		page = totalPages - 1
	}

	start := page * pageSize
	end := start + pageSize
	if end > len(chains) {
		end = len(chains)
	}
	cur := chains[start:end]

	var rows [][]tgbotapi.InlineKeyboardButton
	for i := 0; i < len(cur); i += 2 {
		row := []tgbotapi.InlineKeyboardButton{}
		for j := 0; j < 2 && i+j < len(cur); j++ {
			chain := cur[i+j]
			label := chainDisplayName(chain)
			if strings.EqualFold(chain, "arb") {
				label = label + "（同链）"
			}
			row = append(row, tgbotapi.NewInlineKeyboardButtonData(
				label,
				fmt.Sprintf("deposit_chain|%d|%s", telegramID, chain),
			))
		}
		rows = append(rows, row)
	}

	navRow := []tgbotapi.InlineKeyboardButton{}
	if page > 0 {
		navRow = append(navRow, tgbotapi.NewInlineKeyboardButtonData("⬅️ 上一页", fmt.Sprintf("deposit_chain_page|%d|%d", telegramID, page-1)))
	}
	if page < totalPages-1 {
		navRow = append(navRow, tgbotapi.NewInlineKeyboardButtonData("➡️ 下一页", fmt.Sprintf("deposit_chain_page|%d|%d", telegramID, page+1)))
	}
	if len(navRow) > 0 {
		rows = append(rows, navRow)
	}
	rows = append(rows, []tgbotapi.InlineKeyboardButton{
		tgbotapi.NewInlineKeyboardButtonData("❌ 取消", fmt.Sprintf("deposit_cancel|%d", telegramID)),
	})

	msg := fmt.Sprintf(`🌐 跨链充值 USDC（NEAR Intents 1Click）

请选择你要转出 USDC 的来源网络。

说明：
• 目前仅支持 EVM 网络（如 Ethereum/Base/OP/Polygon/BSC/Avalanche 等）
• 暂不支持 Solana/Stellar/NEAR/Sui 等非 EVM 网络

最终到账网络：Arbitrum（自动充值到 Hyperliquid 仍需 /balance 触发）
页码：%d/%d`, page+1, totalPages)

	tbm.sendMessageWithInlineKeyboard(chatID, msg, tgbotapi.NewInlineKeyboardMarkup(rows...))
}

func (tbm *TelegramBotManager) handleDepositArbitrumCallback(callback *tgbotapi.CallbackQuery, chatID int64, telegramID int64) {
	tbm.answerCallbackQuery(callback.ID, "📋 复制地址充值")

	if _, err := tbm.db.GetTGUserByTelegramID(telegramID); err != nil {
		tbm.sendMessage(chatID, "❌ 请先使用 /start 初始化账号")
		return
	}
	if !tbm.hasHyperliquidAccount(telegramID) {
		tbm.sendMessage(chatID, "❌ 请先使用 /start 完成账号初始化")
		return
	}

	_, walletAddr, err := tbm.extractAgentKeyAndWallet(telegramID)
	if err != nil {
		tbm.sendMessage(chatID, "❌ 获取充值地址失败，请稍后重试")
		return
	}

	depositMsg := fmt.Sprintf(`🏦 Hyperliquid 充值地址（Arbitrum 网络）

<code>%s</code>（点击复制）

📋 充值说明:
• 网络: Arbitrum One
• 最小充值: 20 USDC
• 到账时间: 通常 2-5 分钟

💡 提示:
• 充值后可在 /balance 查看余额
• 如配置了自动充值，/balance 会尝试自动充值到 Hyperliquid`, esc(walletAddr))

	tbm.sendMessage(chatID, depositMsg)
}

func (tbm *TelegramBotManager) handleDepositCrossChainCallback(callback *tgbotapi.CallbackQuery, chatID int64, telegramID int64) {
	tbm.answerCallbackQuery(callback.ID, "🌐 选择来源网络")

	if _, err := tbm.db.GetTGUserByTelegramID(telegramID); err != nil {
		tbm.sendMessage(chatID, "❌ 请先使用 /start 初始化账号")
		return
	}
	if !tbm.hasHyperliquidAccount(telegramID) {
		tbm.sendMessage(chatID, "❌ 请先使用 /start 完成账号初始化")
		return
	}

	sessionMgr := tbm.tgTraderMgr.GetSessionManager()
	session := sessionMgr.GetOrCreateSession(telegramID)
	params := tbm.ensureSessionParams(session)

	_, walletAddr, err := tbm.extractAgentKeyAndWallet(telegramID)
	if err != nil {
		tbm.sendMessage(chatID, "❌ 获取钱包地址失败，请稍后重试")
		return
	}
	params[oneClickParamWalletAddr] = walletAddr

	// 目标资产固定为 Arbitrum USDC（从 tokens 动态获取）
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	chainToToken, err := tbm.oneClick.GetUSDCTokensByChain(ctx)
	if err != nil {
		tbm.sendMessage(chatID, fmt.Sprintf("❌ 获取 1Click 资产列表失败: %s", esc(err)))
		return
	}
	dest, ok := chainToToken["arb"]
	if !ok || dest.AssetID == "" {
		tbm.sendMessage(chatID, "❌ 1Click 未返回 Arbitrum USDC 资产信息，暂无法使用跨链充值")
		return
	}
	params[oneClickParamDestinationAsset] = dest.AssetID

	// 清理上一次残留参数
	delete(params, oneClickParamOriginChain)
	delete(params, oneClickParamLastDepositChain)
	delete(params, oneClickParamLastDepositAddress)
	delete(params, oneClickParamLastDepositMemo)

	session.State = StateIdle
	sessionMgr.UpdateSessionState(telegramID, StateIdle)
	sessionMgr.UpdateTraderConfig(telegramID, session.TraderConfig)

	tbm.sendOneClickChainSelection(chatID, telegramID, 0)
}

func (tbm *TelegramBotManager) handleDepositChainPageCallback(callback *tgbotapi.CallbackQuery, chatID int64, telegramID int64, parts []string) {
	tbm.answerCallbackQuery(callback.ID, "切换页码")
	page, ok := parsePage(parts)
	if !ok {
		tbm.sendMessage(chatID, "❌ 页码参数错误")
		return
	}
	tbm.sendOneClickChainSelection(chatID, telegramID, page)
}

func (tbm *TelegramBotManager) handleDepositChainSelectCallback(callback *tgbotapi.CallbackQuery, chatID int64, telegramID int64, parts []string) {
	tbm.answerCallbackQuery(callback.ID, "⏳ 生成充值地址中...")
	if len(parts) < 3 {
		tbm.sendMessage(chatID, "❌ 网络参数错误")
		return
	}
	originChain := strings.TrimSpace(parts[2])

	sessionMgr := tbm.tgTraderMgr.GetSessionManager()
	session := sessionMgr.GetOrCreateSession(telegramID)
	params := tbm.ensureSessionParams(session)

	if !isEVMChain(originChain) {
		tbm.sendMessage(chatID, "❌ 当前仅支持 EVM 网络的 USDC 跨链充值，请重新选择")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	chainToToken, err := tbm.oneClick.GetUSDCTokensByChain(ctx)
	if err != nil {
		tbm.sendMessage(chatID, fmt.Sprintf("❌ 获取 1Click 资产列表失败: %s", esc(err)))
		return
	}
	originTok, ok := chainToToken[originChain]
	if !ok || originTok.AssetID == "" {
		tbm.sendMessage(chatID, "❌ 该网络暂不支持 USDC 跨链充值")
		return
	}

	destAsset, _ := params[oneClickParamDestinationAsset].(string)
	if destAsset == "" {
		// 兜底：重新取一次 arb
		if destTok, ok := chainToToken["arb"]; ok {
			destAsset = destTok.AssetID
		}
	}
	if destAsset == "" {
		tbm.sendMessage(chatID, "❌ 未找到目标 Arbitrum USDC 资产，暂无法继续")
		return
	}

	walletAddr, _ := params[oneClickParamWalletAddr].(string)
	if strings.TrimSpace(walletAddr) == "" {
		_, extractedWallet, err := tbm.extractAgentKeyAndWallet(telegramID)
		if err != nil {
			tbm.sendMessage(chatID, "❌ 获取钱包地址失败，请稍后重试")
			return
		}
		walletAddr = extractedWallet
		params[oneClickParamWalletAddr] = walletAddr
	}

	amountBase, err := decimalToBaseUnits(oneClickMinDepositUSDC, originTok.Decimals)
	if err != nil {
		tbm.sendMessage(chatID, fmt.Sprintf("❌ 生成最小充值金额失败: %s", esc(err)))
		return
	}

	req := oneClickQuoteRequest{
		Dry:               false,
		SwapType:          "FLEX_INPUT",
		SlippageTolerance: tbm.oneClickSlippageBps,
		OriginAsset:       originTok.AssetID,
		DepositType:       "ORIGIN_CHAIN",
		DestinationAsset:  destAsset,
		Amount:            amountBase,
		RefundTo:          walletAddr,
		RefundType:        "ORIGIN_CHAIN",
		Recipient:         walletAddr,
		RecipientType:     "DESTINATION_CHAIN",
		Deadline:          time.Now().UTC().Add(tbm.oneClickDeadline).Format(time.RFC3339),
		QuoteWaitingTime:  tbm.oneClickQuoteWaitMs,
	}

	resp, err := tbm.oneClick.RequestQuote(ctx, req)
	if err != nil {
		tbm.sendMessage(chatID, fmt.Sprintf("❌ 生成充值地址失败: %s", esc(err)))
		return
	}

	depositAddr := strings.TrimSpace(resp.Quote.DepositAddress)
	depositMemo := strings.TrimSpace(resp.Quote.DepositMemo)
	if depositAddr == "" {
		tbm.sendMessage(chatID, "❌ 1Click 未返回充值地址，请稍后重试")
		return
	}

	deadlineStr := strings.TrimSpace(resp.Quote.Deadline)
	inactiveStr := strings.TrimSpace(resp.Quote.TimeWhenInactive)

	params[oneClickParamOriginChain] = originChain
	params[oneClickParamLastDepositChain] = originChain
	params[oneClickParamLastDepositAddress] = depositAddr
	params[oneClickParamLastDepositMemo] = depositMemo

	session.State = StateIdle
	sessionMgr.UpdateSessionState(telegramID, StateIdle)
	sessionMgr.UpdateTraderConfig(telegramID, session.TraderConfig)

	memoLine := ""
	if depositMemo != "" {
		memoLine = fmt.Sprintf("\n充值 Memo:\n<code>%s</code>", esc(depositMemo))
	}

	validityBlock := ""
	if raw, t, ok := pickSoonerTime(deadlineStr, inactiveStr); ok {
		remain := formatDurationCN(time.Until(t))
		if remain == "已过期" {
			validityBlock = fmt.Sprintf("\n\n🚫 <b>地址已失效</b>\n最迟转账时间：<code>%s</code>\n请重新使用 /deposit 生成新地址（不要再向旧地址转账）。", esc(raw))
		} else {
			validityBlock = fmt.Sprintf("\n\n🚨 <b>请尽快完成转账</b>\n最迟转账时间：<code>%s</code>（剩余 %s）\n超时请重新 /deposit 生成新地址。", esc(raw), esc(remain))
		}
	}

	msg := fmt.Sprintf(`✅ 跨链充值地址已生成

来源网络：%s
最小充值：%s USDC

请从【%s】网络将 USDC 转账到以下地址：
<code>%s</code>%s
%s

最终收款地址（Arbitrum）：
<code>%s</code>

💡 到账后执行 /balance，可触发自动充值到 Hyperliquid（若已启用）。`,
		esc(chainDisplayName(originChain)),
		esc(oneClickMinDepositUSDC),
		esc(chainDisplayName(originChain)),
		esc(depositAddr),
		memoLine,
		validityBlock,
		esc(walletAddr),
	)

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📦 查看状态", fmt.Sprintf("deposit_status_last|%d", telegramID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔄 重新选择网络", fmt.Sprintf("deposit_xchain|%d", telegramID)),
			tgbotapi.NewInlineKeyboardButtonData("❌ 关闭", fmt.Sprintf("deposit_cancel|%d", telegramID)),
		),
	)
	tbm.sendMessageWithInlineKeyboard(chatID, msg, keyboard)
}

func (tbm *TelegramBotManager) handleDepositCancelCallback(callback *tgbotapi.CallbackQuery, chatID int64, telegramID int64) {
	tbm.answerCallbackQuery(callback.ID, "已关闭")

	sessionMgr := tbm.tgTraderMgr.GetSessionManager()
	session := sessionMgr.GetOrCreateSession(telegramID)
	params := tbm.ensureSessionParams(session)
	delete(params, oneClickParamOriginChain)

	session.State = StateIdle
	sessionMgr.UpdateSessionState(telegramID, StateIdle)
	sessionMgr.UpdateTraderConfig(telegramID, session.TraderConfig)

	tbm.sendMessage(chatID, "✅ 已关闭")
}

func decimalToBaseUnits(amount string, decimals int) (string, error) {
	amount = strings.TrimSpace(amount)
	amount = strings.ReplaceAll(amount, ",", "")
	if amount == "" {
		return "", fmt.Errorf("金额为空")
	}
	r, ok := new(big.Rat).SetString(amount)
	if !ok {
		return "", fmt.Errorf("无法解析金额: %s", amount)
	}
	if r.Sign() <= 0 {
		return "", fmt.Errorf("金额必须大于0")
	}

	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	r.Mul(r, new(big.Rat).SetInt(scale))

	out := new(big.Int).Quo(r.Num(), r.Denom()) // floor
	return out.String(), nil
}

func parseDecimal(s string) (bool, *big.Rat) {
	s = strings.TrimSpace(s)
	if s == "" {
		return false, nil
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return false, nil
	}
	return true, r
}

func (tbm *TelegramBotManager) oneClickStatusText(status string) string {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "KNOWN_DEPOSIT_TX":
		return "已提交转账（已识别交易哈希）"
	case "PENDING_DEPOSIT":
		return "等待转账"
	case "INCOMPLETE_DEPOSIT":
		return "转账金额不足"
	case "PROCESSING":
		return "处理中"
	case "SUCCESS":
		return "成功"
	case "REFUNDED":
		return "已退款"
	case "FAILED":
		return "失败"
	default:
		if status == "" {
			return "未知"
		}
		return status
	}
}

func parsePage(parts []string) (int, bool) {
	if len(parts) < 3 {
		return 0, false
	}
	p, err := strconv.Atoi(parts[2])
	if err != nil {
		return 0, false
	}
	return p, true
}

func (tbm *TelegramBotManager) logOneClickParams(prefix string, params map[string]interface{}) {
	if params == nil {
		return
	}
	chain, _ := params[oneClickParamOriginChain].(string)
	destAsset, _ := params[oneClickParamDestinationAsset].(string)
	lastDeposit, _ := params[oneClickParamLastDepositAddress].(string)
	log.Printf("🔎 [%s] oneclick params: chain=%s dest=%s lastDeposit=%s", prefix, chain, destAsset, lastDeposit)
}
