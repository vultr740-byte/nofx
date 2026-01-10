package telegram

import (
	"context"
	"fmt"
	"log"
	"math/big"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const (
	oneClickParamWalletAddr       = "oneclick_wallet_addr"
	oneClickParamOriginChain      = "oneclick_origin_chain"
	oneClickParamOriginAsset      = "oneclick_origin_asset"
	oneClickParamOriginDecimals   = "oneclick_origin_decimals"
	oneClickParamDestinationAsset = "oneclick_destination_asset"
	oneClickParamAmountHuman      = "oneclick_amount_human"
	oneClickParamAmountBase       = "oneclick_amount_base"
	oneClickParamRefundTo         = "oneclick_refund_to"
	oneClickParamDepositMode      = "oneclick_deposit_mode"
	oneClickParamDryOutFmt        = "oneclick_dry_amount_out_fmt"
	oneClickParamDryTimeEstimate  = "oneclick_dry_time_estimate"
)

var oneClickAmountRe = regexp.MustCompile(`(?i)([0-9]+(?:\\.[0-9]+)?)`)

type oneClickOutBelowMinError struct {
	AmountOut string
}

func (e oneClickOutBelowMinError) Error() string {
	if strings.TrimSpace(e.AmountOut) == "" {
		return "预计到账不足 20 USDC"
	}
	return fmt.Sprintf("预计到账不足 20 USDC（%s）", strings.TrimSpace(e.AmountOut))
}

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
	delete(params, oneClickParamOriginAsset)
	delete(params, oneClickParamOriginDecimals)
	delete(params, oneClickParamAmountHuman)
	delete(params, oneClickParamAmountBase)
	delete(params, oneClickParamRefundTo)
	delete(params, oneClickParamDepositMode)
	delete(params, oneClickParamDryOutFmt)
	delete(params, oneClickParamDryTimeEstimate)

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
	tbm.answerCallbackQuery(callback.ID, "请输入金额")
	if len(parts) < 3 {
		tbm.sendMessage(chatID, "❌ 网络参数错误")
		return
	}
	originChain := strings.TrimSpace(parts[2])

	sessionMgr := tbm.tgTraderMgr.GetSessionManager()
	session := sessionMgr.GetOrCreateSession(telegramID)
	params := tbm.ensureSessionParams(session)

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

	params[oneClickParamOriginChain] = originChain
	params[oneClickParamOriginAsset] = originTok.AssetID
	params[oneClickParamOriginDecimals] = originTok.Decimals

	// 切换链后重置后续参数
	delete(params, oneClickParamAmountHuman)
	delete(params, oneClickParamAmountBase)
	delete(params, oneClickParamRefundTo)
	delete(params, oneClickParamDepositMode)
	delete(params, oneClickParamDryOutFmt)
	delete(params, oneClickParamDryTimeEstimate)

	session.State = StateDepositInputAmount
	sessionMgr.UpdateSessionState(telegramID, StateDepositInputAmount)
	sessionMgr.UpdateTraderConfig(telegramID, session.TraderConfig)

	tbm.sendMessage(chatID, fmt.Sprintf(`✅ 已选择来源网络：%s

请输入充值金额（USDC），例如 50 或 50.5。

提示：
• 跨链存在费用，建议最终到账 ≥ 20 USDC
• 输入 cancel 可取消`, esc(chainDisplayName(originChain))))
}

func (tbm *TelegramBotManager) handleDepositRefundSameCallback(callback *tgbotapi.CallbackQuery, chatID int64, telegramID int64) {
	tbm.answerCallbackQuery(callback.ID, "使用钱包地址")

	sessionMgr := tbm.tgTraderMgr.GetSessionManager()
	session := sessionMgr.GetOrCreateSession(telegramID)
	params := tbm.ensureSessionParams(session)
	walletAddr, _ := params[oneClickParamWalletAddr].(string)
	if walletAddr == "" {
		tbm.sendMessage(chatID, "❌ 未找到钱包地址，请重新使用 /deposit")
		return
	}

	params[oneClickParamRefundTo] = walletAddr

	if err := tbm.previewOneClickQuote(chatID, telegramID, session); err != nil {
		if below, ok := err.(oneClickOutBelowMinError); ok {
			session.State = StateDepositInputAmount
			sessionMgr.UpdateSessionState(telegramID, StateDepositInputAmount)
			sessionMgr.UpdateTraderConfig(telegramID, session.TraderConfig)
			tbm.sendMessage(chatID, fmt.Sprintf("⚠️ %s\n\n请提高充值金额后重试（例如 21+）。", esc(below.Error())))
			return
		}
		tbm.sendMessage(chatID, fmt.Sprintf("❌ 获取报价失败: %s", esc(err)))
		return
	}

	session.State = StateDepositConfirmX
	sessionMgr.UpdateSessionState(telegramID, StateDepositConfirmX)
	sessionMgr.UpdateTraderConfig(telegramID, session.TraderConfig)
}

func (tbm *TelegramBotManager) handleDepositQuoteConfirmCallback(callback *tgbotapi.CallbackQuery, chatID int64, telegramID int64) {
	tbm.answerCallbackQuery(callback.ID, "⏳ 生成中...")

	sessionMgr := tbm.tgTraderMgr.GetSessionManager()
	session := sessionMgr.GetOrCreateSession(telegramID)
	params := tbm.ensureSessionParams(session)

	originChain, _ := params[oneClickParamOriginChain].(string)
	originAsset, _ := params[oneClickParamOriginAsset].(string)
	destAsset, _ := params[oneClickParamDestinationAsset].(string)
	amountHuman, _ := params[oneClickParamAmountHuman].(string)
	amountBase, _ := params[oneClickParamAmountBase].(string)
	refundTo, _ := params[oneClickParamRefundTo].(string)
	walletAddr, _ := params[oneClickParamWalletAddr].(string)

	if originAsset == "" || destAsset == "" || amountBase == "" || refundTo == "" || walletAddr == "" {
		tbm.sendMessage(chatID, "❌ 缺少参数，请重新使用 /deposit")
		sessionMgr.ClearSession(telegramID)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	swapType := "EXACT_INPUT"
	if tbm.oneClickUseFlexInputSwap {
		swapType = "FLEX_INPUT"
	}

	req := oneClickQuoteRequest{
		Dry:               false,
		SwapType:          swapType,
		SlippageTolerance: tbm.oneClickSlippageBps,
		OriginAsset:       originAsset,
		DepositType:       "ORIGIN_CHAIN",
		DestinationAsset:  destAsset,
		Amount:            amountBase,
		RefundTo:          refundTo,
		RefundType:        "ORIGIN_CHAIN",
		Recipient:         walletAddr,
		RecipientType:     "DESTINATION_CHAIN",
		Deadline:          time.Now().UTC().Add(tbm.oneClickDeadline).Format(time.RFC3339),
		QuoteWaitingTime:  tbm.oneClickQuoteWaitMs,
	}
	if mode, ok := params[oneClickParamDepositMode].(string); ok && strings.TrimSpace(mode) != "" {
		req.DepositMode = strings.TrimSpace(mode)
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

	memoLine := ""
	if depositMemo != "" {
		memoLine = fmt.Sprintf("\n充值 Memo:\n<code>%s</code>", esc(depositMemo))
	}

	msg := fmt.Sprintf(`✅ 跨链充值地址已生成

来源网络：%s
充值金额：%s USDC

请将 USDC 转账到以下地址：
<code>%s</code>%s

最终收款地址（Arbitrum）：
<code>%s</code>

📦 查询状态：
/deposit_status %s%s

💡 到账后执行 /balance，可触发自动充值到 Hyperliquid（若已启用）。`,
		esc(chainDisplayName(originChain)),
		esc(amountHuman),
		esc(depositAddr),
		memoLine,
		esc(walletAddr),
		esc(depositAddr),
		func() string {
			if depositMemo == "" {
				return ""
			}
			return " " + esc(depositMemo)
		}(),
	)
	tbm.sendMessage(chatID, msg)

	sessionMgr.ClearSession(telegramID)
}

func (tbm *TelegramBotManager) handleDepositCancelCallback(callback *tgbotapi.CallbackQuery, chatID int64, telegramID int64) {
	tbm.answerCallbackQuery(callback.ID, "已取消")
	tbm.tgTraderMgr.GetSessionManager().ClearSession(telegramID)
	tbm.sendMessage(chatID, "❌ 已取消")
}

func (tbm *TelegramBotManager) handleOneClickDepositInput(update tgbotapi.Update, session *UserSession) {
	chatID := update.Message.Chat.ID
	telegramID := update.Message.From.ID
	input := strings.TrimSpace(update.Message.Text)

	sessionMgr := tbm.tgTraderMgr.GetSessionManager()

	if strings.EqualFold(input, "cancel") || input == "取消" {
		sessionMgr.ClearSession(telegramID)
		tbm.sendMessage(chatID, "❌ 已取消跨链充值")
		return
	}

	params := tbm.ensureSessionParams(session)

	switch session.State {
	case StateDepositInputAmount:
		originDecimalsAny, ok := params[oneClickParamOriginDecimals]
		if !ok {
			sessionMgr.ClearSession(telegramID)
			tbm.sendMessage(chatID, "❌ 会话已过期，请重新使用 /deposit")
			return
		}
		originDecimals, _ := originDecimalsAny.(int)
		if originDecimals == 0 {
			// 兼容 json.Number 或 float64
			if f, ok := originDecimalsAny.(float64); ok {
				originDecimals = int(f)
			}
		}

		num := oneClickAmountRe.FindStringSubmatch(input)
		if len(num) < 2 {
			tbm.sendMessage(chatID, "❌ 未识别到金额，请输入例如 50 或 50.5（USDC）。输入 cancel 取消。")
			return
		}
		amountHuman := num[1]
		amountBase, err := decimalToBaseUnits(amountHuman, originDecimals)
		if err != nil {
			tbm.sendMessage(chatID, fmt.Sprintf("❌ 金额格式错误: %s", esc(err)))
			return
		}

		params[oneClickParamAmountHuman] = amountHuman
		params[oneClickParamAmountBase] = amountBase

		session.State = StateDepositInputRefund
		sessionMgr.UpdateSessionState(telegramID, StateDepositInputRefund)
		sessionMgr.UpdateTraderConfig(telegramID, session.TraderConfig)

		originChain, _ := params[oneClickParamOriginChain].(string)
		walletAddr, _ := params[oneClickParamWalletAddr].(string)
		if isEVMChain(originChain) && walletAddr != "" {
			text := fmt.Sprintf(`请输入退款地址（仅在 swap 失败时退款到此地址）

• 来源网络：%s
• 你也可以点击按钮使用你的钱包地址作为退款地址
• 输入 cancel 可取消`, esc(chainDisplayName(originChain)))

			keyboard := tgbotapi.NewInlineKeyboardMarkup(
				tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButtonData("使用我的钱包地址", fmt.Sprintf("deposit_refund_same|%d", telegramID)),
				),
				tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButtonData("❌ 取消", fmt.Sprintf("deposit_cancel|%d", telegramID)),
				),
			)
			tbm.sendMessageWithInlineKeyboard(chatID, text, keyboard)
			return
		}

		tbm.sendMessage(chatID, fmt.Sprintf(`请输入退款地址（仅在 swap 失败时退款到此地址）

• 来源网络：%s
• 输入 cancel 可取消`, esc(chainDisplayName(originChain))))

	case StateDepositInputRefund:
		walletAddr, _ := params[oneClickParamWalletAddr].(string)
		if strings.EqualFold(input, "same") || strings.EqualFold(input, "同钱包") {
			if walletAddr == "" {
				tbm.sendMessage(chatID, "❌ 未找到钱包地址，请重新使用 /deposit")
				return
			}
			input = walletAddr
		}
		params[oneClickParamRefundTo] = input

		// 获取 dry quote 预览
		if err := tbm.previewOneClickQuote(chatID, telegramID, session); err != nil {
			if below, ok := err.(oneClickOutBelowMinError); ok {
				session.State = StateDepositInputAmount
				sessionMgr.UpdateSessionState(telegramID, StateDepositInputAmount)
				sessionMgr.UpdateTraderConfig(telegramID, session.TraderConfig)
				tbm.sendMessage(chatID, fmt.Sprintf("⚠️ %s\n\n请提高充值金额后重试（例如 21+）。", esc(below.Error())))
				return
			}
			tbm.sendMessage(chatID, fmt.Sprintf("❌ 获取报价失败: %s", esc(err)))
			// 回退到输入退款地址，方便用户修正
			session.State = StateDepositInputRefund
			sessionMgr.UpdateSessionState(telegramID, StateDepositInputRefund)
			sessionMgr.UpdateTraderConfig(telegramID, session.TraderConfig)
			return
		}

		session.State = StateDepositConfirmX
		sessionMgr.UpdateSessionState(telegramID, StateDepositConfirmX)
		sessionMgr.UpdateTraderConfig(telegramID, session.TraderConfig)

	default:
		sessionMgr.ClearSession(telegramID)
		tbm.sendMessage(chatID, "❌ 会话已过期，请重新使用 /deposit")
	}
}

func (tbm *TelegramBotManager) previewOneClickQuote(chatID int64, telegramID int64, session *UserSession) error {
	params := tbm.ensureSessionParams(session)
	originChain, _ := params[oneClickParamOriginChain].(string)
	originAsset, _ := params[oneClickParamOriginAsset].(string)
	destAsset, _ := params[oneClickParamDestinationAsset].(string)
	amountHuman, _ := params[oneClickParamAmountHuman].(string)
	amountBase, _ := params[oneClickParamAmountBase].(string)
	refundTo, _ := params[oneClickParamRefundTo].(string)
	walletAddr, _ := params[oneClickParamWalletAddr].(string)

	if originAsset == "" || destAsset == "" || amountBase == "" || refundTo == "" || walletAddr == "" {
		return fmt.Errorf("缺少参数，请重新使用 /deposit")
	}

	tbm.sendMessage(chatID, "🔄 正在获取跨链充值报价，请稍候...")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	swapType := "EXACT_INPUT"
	if tbm.oneClickUseFlexInputSwap {
		swapType = "FLEX_INPUT"
	}

	req := oneClickQuoteRequest{
		Dry:               true,
		SwapType:          swapType,
		SlippageTolerance: tbm.oneClickSlippageBps,
		OriginAsset:       originAsset,
		DepositType:       "ORIGIN_CHAIN",
		DestinationAsset:  destAsset,
		Amount:            amountBase,
		RefundTo:          refundTo,
		RefundType:        "ORIGIN_CHAIN",
		Recipient:         walletAddr,
		RecipientType:     "DESTINATION_CHAIN",
		Deadline:          time.Now().UTC().Add(tbm.oneClickDeadline).Format(time.RFC3339),
		QuoteWaitingTime:  tbm.oneClickQuoteWaitMs,
	}

	if mode, ok := params[oneClickParamDepositMode].(string); ok && strings.TrimSpace(mode) != "" {
		req.DepositMode = strings.TrimSpace(mode)
	}

	resp, err := tbm.oneClick.RequestQuote(ctx, req)
	if err != nil {
		return err
	}

	// 保存服务端最终使用的 depositMode，避免后续 confirm 再次踩到 stellar 的 MEMO 约束
	if strings.TrimSpace(resp.QuoteRequest.DepositMode) != "" {
		params[oneClickParamDepositMode] = resp.QuoteRequest.DepositMode
	}
	params[oneClickParamDryOutFmt] = resp.Quote.AmountOutFmt
	params[oneClickParamDryTimeEstimate] = resp.Quote.TimeEstimateSec

	// 若预计到账不足 20 USDC，提示用户提高金额（避免 Hyperliquid 最小充值限制）
	if outOk, outRat := parseDecimal(resp.Quote.AmountOutFmt); outOk {
		if outRat.Cmp(big.NewRat(20, 1)) < 0 {
			return oneClickOutBelowMinError{AmountOut: resp.Quote.AmountOutFmt}
		}
	}

	msg := fmt.Sprintf(`📌 跨链充值报价（USDC → Arbitrum USDC）

来源网络：%s
充值金额：%s USDC
预计到账：%s USDC
预计耗时：~%ds

是否生成专属充值地址？`,
		esc(chainDisplayName(originChain)),
		esc(amountHuman),
		esc(resp.Quote.AmountOutFmt),
		resp.Quote.TimeEstimateSec,
	)

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ 生成充值地址", fmt.Sprintf("deposit_quote_confirm|%d", telegramID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("❌ 取消", fmt.Sprintf("deposit_cancel|%d", telegramID)),
		),
	)
	tbm.sendMessageWithInlineKeyboard(chatID, msg, keyboard)
	return nil
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
	originAsset, _ := params[oneClickParamOriginAsset].(string)
	destAsset, _ := params[oneClickParamDestinationAsset].(string)
	amount, _ := params[oneClickParamAmountHuman].(string)
	log.Printf("🔎 [%s] oneclick params: chain=%s origin=%s dest=%s amount=%s", prefix, chain, originAsset, destAsset, amount)
}
