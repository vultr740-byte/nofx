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

// 链优先级：按“资金体量/主流程度”优先展示（可按运营反馈继续微调）。
var oneClickChainLiquidityRank = map[string]int{
	"eth":    0,
	"base":   1,
	"sol":    2,
	"sui":    3,
	"op":     4,
	"pol":    5,
	"bsc":    6,
	"avax":   7,
	"gnosis": 8,
	// 其余链默认靠后（按字母排序兜底）
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

func chainLiquidityPriority(chain string) int {
	c := strings.ToLower(strings.TrimSpace(chain))
	if v, ok := oneClickChainLiquidityRank[c]; ok {
		return v
	}
	return 1000
}

func isEVMChain(chain string) bool {
	switch strings.ToLower(strings.TrimSpace(chain)) {
	case "eth", "arb", "base", "op", "pol", "bsc", "avax", "gnosis", "bera", "xlayer", "monad", "adi":
		return true
	default:
		return false
	}
}

func isSupportedOneClickOriginChain(chain string) bool {
	if isEVMChain(chain) {
		// Arbitrum 已有“直充”入口，不在跨链来源列表中展示/支持。
		if strings.EqualFold(strings.TrimSpace(chain), "arb") {
			return false
		}
		return true
	}
	switch strings.ToLower(strings.TrimSpace(chain)) {
	case "sol", "sui":
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
		if !isSupportedOneClickOriginChain(chain) {
			continue
		}
		chains = append(chains, chain)
	}
	sort.SliceStable(chains, func(i, j int) bool {
		ci := strings.ToLower(strings.TrimSpace(chains[i]))
		cj := strings.ToLower(strings.TrimSpace(chains[j]))
		// Ethereum 手续费偏高，放到列表最后。
		if ci == "eth" && cj != "eth" {
			return false
		}
		if ci != "eth" && cj == "eth" {
			return true
		}
		pi := chainLiquidityPriority(ci)
		pj := chainLiquidityPriority(cj)
		if pi != pj {
			return pi < pj
		}
		return ci < cj
	})

	const pageSize = 20
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

	pageLine := ""
	if totalPages > 1 {
		pageLine = fmt.Sprintf("\n页码：%d/%d", page+1, totalPages)
	}

	msg := `🌐 跨链充值 USDC，选择资金来源网络。` + pageLine

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

	tbm.startDepositCrossChainSelection(chatID, telegramID)
}

func (tbm *TelegramBotManager) handleDepositCrossChainReselectCallback(callback *tgbotapi.CallbackQuery, chatID int64, telegramID int64) {
	// 与“取消”一致：删除当前消息（上一条消息就是网络选择列表）。
	tbm.answerCallbackQuery(callback.ID, "")
	if callback.Message != nil {
		tbm.deleteMessage(chatID, callback.Message.MessageID)
	}
}

func (tbm *TelegramBotManager) startDepositCrossChainSelection(chatID int64, telegramID int64) {
	if tbm.oneClick == nil {
		tbm.sendMessage(chatID, "❌ 跨链充值服务未初始化")
		return
	}

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

	if strings.EqualFold(originChain, "arb") {
		tbm.sendMessage(chatID, "💡 Arbitrum 已有“直充”入口，请返回上一层选择「🟦 Arbitrum 直充」。")
		return
	}
	if !isSupportedOneClickOriginChain(originChain) {
		tbm.sendMessage(chatID, "❌ 当前仅支持 EVM / Solana / Sui 网络的 USDC 跨链充值，请重新选择")
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
	}
	walletAddr = normalizeEVMAddressLower(walletAddr)
	if walletAddr == "" {
		tbm.sendMessage(chatID, "❌ 获取钱包地址失败，请稍后重试")
		return
	}
	params[oneClickParamWalletAddr] = walletAddr

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
		RefundType:        "INTENTS",
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

	minDeposit := oneClickMinDepositUSDC
	if v := strings.TrimSpace(resp.Quote.MinAmountIn); v != "" {
		if s, err := baseUnitsToDecimal(v, originTok.Decimals); err == nil && strings.TrimSpace(s) != "" {
			minDeposit = strings.TrimSpace(s)
		}
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
		memoLine = fmt.Sprintf("\nMemo:\n<code>%s</code>", esc(depositMemo))
	}

	remainLine := ""
	validityBlock := ""
	if _, t, ok := pickSoonerTime(deadlineStr, inactiveStr); ok {
		remain := formatDurationCN(time.Until(t))
		if remain == "已过期" {
			validityBlock = "\n\n🚫 <b>地址已失效</b>\n请重新使用 /deposit 生成新地址。"
		} else {
			remainLine = fmt.Sprintf("剩余时间：%s", esc(remain))
		}
	}

	msg := fmt.Sprintf(`✅ <b>跨链充值地址已生成</b>

网络：%s
最小充值：%s USDC
%s

充值地址（点击复制）：
<code>%s</code>%s%s

到账后执行 /balance。`,
		esc(chainDisplayName(originChain)),
		esc(minDeposit),
		remainLine,
		esc(depositAddr),
		memoLine,
		validityBlock,
	)

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ 已完成充值", fmt.Sprintf("deposit_status_last|%d", telegramID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔄 重新选择网络", fmt.Sprintf("deposit_xchain_reselect|%d", telegramID)),
			tgbotapi.NewInlineKeyboardButtonData("❌ 取消", fmt.Sprintf("deposit_cancel|%d", telegramID)),
		),
	)
	tbm.sendMessageWithInlineKeyboard(chatID, msg, keyboard)
}

func (tbm *TelegramBotManager) handleDepositCancelCallback(callback *tgbotapi.CallbackQuery, chatID int64, telegramID int64) {
	tbm.answerCallbackQuery(callback.ID, "")

	sessionMgr := tbm.tgTraderMgr.GetSessionManager()
	session := sessionMgr.GetOrCreateSession(telegramID)
	params := tbm.ensureSessionParams(session)
	delete(params, oneClickParamOriginChain)

	session.State = StateIdle
	sessionMgr.UpdateSessionState(telegramID, StateIdle)
	sessionMgr.UpdateTraderConfig(telegramID, session.TraderConfig)

	if callback.Message != nil {
		tbm.deleteMessage(chatID, callback.Message.MessageID)
	}
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

func trimTrailingZerosDecimal(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if !strings.Contains(s, ".") {
		return s
	}
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	return s
}

func baseUnitsToDecimal(amountBase string, decimals int) (string, error) {
	amountBase = strings.TrimSpace(amountBase)
	amountBase = strings.ReplaceAll(amountBase, ",", "")
	if amountBase == "" {
		return "", fmt.Errorf("金额为空")
	}

	// 兼容 API 直接返回小数的情况
	if strings.Contains(amountBase, ".") {
		if ok, _ := parseDecimal(amountBase); ok {
			return trimTrailingZerosDecimal(amountBase), nil
		}
		return "", fmt.Errorf("无法解析金额: %s", amountBase)
	}

	bi, ok := new(big.Int).SetString(amountBase, 10)
	if !ok {
		return "", fmt.Errorf("无法解析 base units: %s", amountBase)
	}
	if bi.Sign() < 0 {
		return "", fmt.Errorf("金额不能为负数")
	}
	if decimals <= 0 {
		return bi.String(), nil
	}

	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	quotient, remainder := new(big.Int).QuoRem(bi, scale, new(big.Int))
	if remainder.Sign() == 0 {
		return quotient.String(), nil
	}

	frac := remainder.Text(10)
	if len(frac) < decimals {
		frac = strings.Repeat("0", decimals-len(frac)) + frac
	}
	frac = strings.TrimRight(frac, "0")
	if frac == "" {
		return quotient.String(), nil
	}
	return quotient.String() + "." + frac, nil
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
