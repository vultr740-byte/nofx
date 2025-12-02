package telegram

import (
	"fmt"
	"html"
	"log"
	"math/big"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"nofx/config"
	"nofx/manager"
	"nofx/mcp"
)

// TelegramBotManager Telegram Bot 管理器
type TelegramBotManager struct {
	bot           *tgbotapi.BotAPI
	db            config.DatabaseInterface
	hlService     *HyperliquidService
	arbService    *ArbitrumService
	debug         bool
	testnet       bool
	traderMgr     *manager.TraderManager
	tgTraderMgr   *TelegramTraderManager
	configWizard  *ConfigWizard
	nlParser      *NLParser
	cmdValidator  *CommandValidator
	debugMu       sync.Mutex
	gasSponsorKey string
	gasSponsorWei *big.Int
	gasProcMu     sync.Mutex
	gasProcessing map[string]struct{}
}

func esc(v interface{}) string {
	return html.EscapeString(fmt.Sprint(v))
}

const (
	telegramMessageChunkSize = 3000
	decisionJSONMarker       = "📋 决策JSON"
	hyperliquidBridgeAddress = "0x2df1c51e09aecf9cacb7bc98cb1742757f163df7"
	gasSponsorshipCooldown   = 6 * time.Hour
)

var minUSDCBridgeAmount = new(big.Int).Mul(big.NewInt(20), big.NewInt(1_000_000))

const (
	gasStatusProcessing = "processing"
	gasStatusGasSent    = "gas_sent"
	gasStatusCompleted  = "completed"
	gasStatusFailed     = "failed"
)

type decisionChunk struct {
	Text   string
	IsJSON bool
}

// NewTelegramBotManager 创建 Telegram Bot 管理器
func NewTelegramBotManager(cfg *config.TelegramBotConfig, db config.DatabaseInterface, traderMgr *manager.TraderManager) (*TelegramBotManager, error) {
	bot, err := tgbotapi.NewBotAPI(cfg.BotToken)
	if err != nil {
		return nil, fmt.Errorf("创建 Telegram Bot 失败: %w", err)
	}

	bot.Debug = cfg.Debug

	network := "主网"
	if cfg.HyperliquidTestnet {
		network = "测试网"
	}
	log.Printf("✅ Telegram Bot 初始化成功: %s (Hyperliquid: %s)", bot.Self.UserName, network)

	// 创建交易员管理器
	tgTraderMgr := NewTelegramTraderManager(db, traderMgr, cfg.HyperliquidTestnet)
	configWizard := NewConfigWizard(tgTraderMgr)

	// 创建自然语言解析器（暂时使用 nil MCP 客户端，后续可以从交易员获取）
	nlParser := NewNLParser(nil)
	cmdValidator := NewCommandValidator(db)

	tgBotMgr := &TelegramBotManager{
		bot:           bot,
		db:            db,
		hlService:     NewHyperliquidService(),
		debug:         cfg.Debug,
		testnet:       cfg.HyperliquidTestnet,
		traderMgr:     traderMgr,
		tgTraderMgr:   tgTraderMgr,
		configWizard:  configWizard,
		nlParser:      nlParser,
		cmdValidator:  cmdValidator,
		gasSponsorKey: cfg.GasPayerPrivateKey,
		gasProcessing: make(map[string]struct{}),
	}

	if cfg.GasSponsorshipETH > 0 {
		tgBotMgr.gasSponsorWei = CalcWeiFromETH(cfg.GasSponsorshipETH)
	} else {
		tgBotMgr.gasSponsorWei = big.NewInt(0)
	}

	if cfg.ArbitrumRPCURL != "" && cfg.ArbitrumUSDC != "" {
		if arbService, err := NewArbitrumService(cfg.ArbitrumRPCURL, cfg.ArbitrumChainID, cfg.ArbitrumUSDC, hyperliquidBridgeAddress); err != nil {
			log.Printf("⚠️ 初始化 Arbitrum 服务失败: %v", err)
		} else {
			tgBotMgr.arbService = arbService
			log.Printf("🌉 已启用 Arbitrum 充值自动化 (RPC: %s)", cfg.ArbitrumRPCURL)
		}
	}

	// 设置TelegramBotManager引用到TelegramTraderManager，用于推送决策
	tgTraderMgr.SetTelegramBotManager(tgBotMgr)

	// 设置TelegramBotManager引用到ConfigWizard，用于删除API KEY消息
	configWizard.SetTelegramBotManager(tgBotMgr)

	return tgBotMgr, nil
}

// Start 启动 Bot
func (tbm *TelegramBotManager) Start() {
	log.Printf("🚀 Telegram Bot 开始运行...")

	// 设置自定义菜单
	tbm.setupCommands()

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := tbm.bot.GetUpdatesChan(u)

	for update := range updates {
		if update.Message != nil {
			if tbm.isSensitiveInput(update.Message.From.ID) {
				log.Printf("收到消息 [%s] [敏感输入已隐藏]", update.Message.From.UserName)
			} else {
				log.Printf("收到消息 [%s] %s", update.Message.From.UserName, update.Message.Text)
			}
			tbm.handleMessage(update)
		} else if update.CallbackQuery != nil {
			log.Printf("收到回调查询 [%s]", update.CallbackQuery.From.UserName)
			tbm.handleCallbackQuery(update)
		}
	}
}

// handleMessage 处理消息
func (tbm *TelegramBotManager) handleMessage(update tgbotapi.Update) {
	telegramID := update.Message.From.ID

	// 更新用户最后交互时间
	if err := tbm.db.UpdateTGUserLastInteraction(telegramID); err != nil {
		log.Printf("更新用户最后交互时间失败: %v", err)
	}

	// 检查是否为命令
	if update.Message.IsCommand() {
		tbm.handleCommand(update)
		return
	}

	// 处理普通消息
	tbm.handleRegularMessage(update)
}

// handleCommand 处理命令
func (tbm *TelegramBotManager) handleCommand(update tgbotapi.Update) {
	chatID := update.Message.Chat.ID
	command := update.Message.Command()

	switch command {
	case "start":
		tbm.handleStart(update)
	case "help":
		tbm.handleHelp(update)
	case "balance":
		tbm.handleBalance(update)
	case "positions":
		tbm.handlePositions(update)
	case "stocks":
		tbm.handleStocks(update)
	case "deposit":
		tbm.handleDeposit(update)
	case "create_trader":
		tbm.handleCreateTrader(update)
	case "start_trader":
		tbm.handleStartTrader(update)
	case "stop_trader":
		tbm.handleStopTrader(update)
	case "trader_status":
		tbm.handleTraderStatus(update)
	case "leaderboard":
		tbm.handleLeaderboard(update)
	case "settings":
		tbm.handleMenu(update)
	default:
		tbm.sendMessage(chatID, "❌ 未知命令。使用 /help 查看可用命令。")
	}
}

// handleStart 处理 /start 命令
func (tbm *TelegramBotManager) handleStart(update tgbotapi.Update) {
	chatID := update.Message.Chat.ID
	telegramID := update.Message.From.ID
	username := update.Message.From.UserName
	firstName := update.Message.From.FirstName
	languageCode := update.Message.From.LanguageCode

	// 确保用户记录存在
	if _, err := tbm.db.GetTGUserByTelegramID(telegramID); err != nil {
		if err := tbm.db.CreateTGUser(telegramID, username, firstName, chatID, languageCode); err != nil {
			log.Printf("创建 TGUser 失败: %v", err)
			tbm.sendMessage(chatID, "❌ 创建用户失败，请稍后重试。")
			return
		}
	}

	traderRecord, err := tbm.tgTraderMgr.EnsureTraderAccount(telegramID)
	if err != nil {
		log.Printf("创建基础账户失败: %v", err)
		tbm.sendMessage(chatID, "❌ 初始化 Hyperliquid 账户失败，请稍后重试。")
		return
	}

	if traderRecord.IsConfigured {
		msg := fmt.Sprintf("👋 欢迎回来，%s！\n\n🤖 你的 AI 交易员已准备就绪。\n\n💡 快速入口:\n/deposit - 充值\n/start_trader - 启动交易\n/help - 帮助", esc(firstName))
		tbm.sendMessage(chatID, msg)
		return
	}

	welcomeMsg := fmt.Sprintf(`🎉 已为你生成 Hyperliquid 钱包 <b>%s</b>

💡 下一步操作:
1. 使用 /deposit 获取充值地址并充值 USDC
2. 使用 /create_trader 完成策略与模型配置
3. 配置完成后可用 /start_trader 启动交易`,
		esc(traderRecord.Name),
	)

	tbm.sendMessage(chatID, welcomeMsg)

	log.Printf("✅ 创建默认交易员成功 - 用户: %d, 钱包: %s",
		telegramID, traderRecord.WalletAddress)
}

// handleHelp 处理 /help 命令
func (tbm *TelegramBotManager) handleHelp(update tgbotapi.Update) {
	chatID := update.Message.Chat.ID
	firstName := update.Message.From.FirstName

	helpMsg := fmt.Sprintf(`你好，%s！以下是可用命令：

📋 基础命令:
/start - 创建或查看您的 Hyperliquid 账号
/help - 显示此帮助信息

💰 账户查询:
/balance - 查看您的账户余额（现货 + 合约）
/positions - 查看当前持仓信息
/deposit - 获取 USDC 充值地址
/leaderboard - 查看交易员盈利排行榜

🤖 AI Agent 管理:
/start_trader - 启动 Agent 开始交易
/stop_trader - 停止 Agent
/trader_status - 查看 Agent 运行状态
/settings - 打开 ⚙️ 设置 面板（导出私钥等）

🔒 安全提示:
请妥善保管您的 Agent Key
不要将 Agent Key 分享给他人
Agent Key 仅用于交易操作

💡 提示: 每个用户自动创建一个 Agent`, esc(firstName))
	tbm.sendMessage(chatID, helpMsg)
}

// handleBalance 处理 /balance 命令
func (tbm *TelegramBotManager) handleBalance(update tgbotapi.Update) {
	chatID := update.Message.Chat.ID
	telegramID := update.Message.From.ID

	// 获取用户信息
	if _, err := tbm.db.GetTGUserByTelegramID(telegramID); err != nil {
		tbm.sendMessage(chatID, "❌ 请先使用 /start 初始化账号")
		return
	}

	// 检查是否有 Hyperliquid 账号
	if !tbm.hasHyperliquidAccount(telegramID) {
		tbm.sendMessage(chatID, "❌ 请先使用 /start 完成账号初始化")
		return
	}

	// 发送查询中消息
	tbm.sendMessage(chatID, "🔄 正在查询余额，请稍候...")

	// 提取 Agent Key 和 Wallet Address
	agentKey, walletAddr, err := tbm.extractAgentKeyAndWallet(telegramID)
	if err != nil {
		log.Printf("提取账号信息失败: %v", err)
		tbm.sendMessage(chatID, "❌ 获取账号信息失败，请稍后重试")
		return
	}

	// 查询余额
	balanceMsg, err := tbm.hlService.GetBalance(agentKey, walletAddr, tbm.testnet)
	if err != nil {
		log.Printf("查询余额失败: %v", err)
		tbm.sendMessage(chatID, "❌ 查询余额失败，请稍后重试")
		return
	}

	// 发送余额信息
	tbm.sendMessage(chatID, balanceMsg)

	// 自动充值逻辑
	if tbm.arbService != nil {
		go tbm.tryAutoBridge(telegramID, chatID, agentKey, walletAddr)
	}
}

// handlePositions 处理 /positions 命令
func (tbm *TelegramBotManager) handlePositions(update tgbotapi.Update) {
	// 添加panic恢复机制，防止整个程序崩溃
	defer func() {
		if r := recover(); r != nil {
			log.Printf("handlePositions panic recovered: %v", r)
			chatID := update.Message.Chat.ID
			tbm.sendMessage(chatID, "❌ 查询持仓时发生错误，请稍后重试")
		}
	}()

	chatID := update.Message.Chat.ID
	telegramID := update.Message.From.ID

	// 获取用户信息
	if _, err := tbm.db.GetTGUserByTelegramID(telegramID); err != nil {
		tbm.sendMessage(chatID, "❌ 请先使用 /start 初始化账号")
		return
	}

	// 检查是否有 Hyperliquid 账号
	if !tbm.hasHyperliquidAccount(telegramID) {
		tbm.sendMessage(chatID, "❌ 请先使用 /start 完成账号初始化")
		return
	}

	// 发送查询中消息
	tbm.sendMessage(chatID, "🔄 正在查询持仓，请稍候...")

	// 提取 Agent Key 和 Wallet Address
	agentKey, walletAddr, err := tbm.extractAgentKeyAndWallet(telegramID)
	if err != nil {
		log.Printf("提取账号信息失败: %v", err)
		tbm.sendMessage(chatID, "❌ 获取账号信息失败，请稍后重试")
		return
	}

	// 查询持仓
	positionsMsg, positions, err := tbm.hlService.GetPositionsWithData(agentKey, walletAddr, tbm.testnet)
	if err != nil {
		log.Printf("查询持仓失败: %v", err)
		tbm.sendMessage(chatID, "❌ 查询持仓失败，请稍后重试")
		return
	}

	// 提取第一个持仓的资产名称
	assetName := ""
	if len(positions) > 0 {
		symbol, ok := positions[0]["symbol"].(string)
		if ok {
			// 去掉USDT或USDC后缀
			assetName = strings.TrimSuffix(symbol, "USDT")
			if assetName == symbol { // 如果没有USDT后缀，尝试去掉USDC后缀
				assetName = strings.TrimSuffix(symbol, "USDC")
			}
		}
	}

	// 动态构造Trade链接
	var tradeURL string
	if assetName != "" {
		// 有持仓时：包含market参数（资产名称-USDC）
		tradeURL = fmt.Sprintf("https://app.trade.xyz/trade?market=%s-USDC&ghost=%s", assetName, walletAddr)
	} else {
		// 无持仓时：不包含market参数
		tradeURL = fmt.Sprintf("https://app.trade.xyz/trade?ghost=%s", walletAddr)
	}
	hyperbotURL := fmt.Sprintf("https://hyperbot.network/trader/%s", walletAddr)

	// 创建内联键盘
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonURL("查看 Trade", tradeURL),
			tgbotapi.NewInlineKeyboardButtonURL("查看 Hyperbot", hyperbotURL),
		),
	)

	// 发送带按钮的持仓信息
	tbm.sendMessageWithInlineKeyboard(chatID, positionsMsg, keyboard)
}

// handleStocks 处理 /stocks 命令
func (tbm *TelegramBotManager) handleStocks(update tgbotapi.Update) {
	// 添加panic恢复机制，防止整个程序崩溃
	defer func() {
		if r := recover(); r != nil {
			log.Printf("handleStocks panic recovered: %v", r)
			chatID := update.Message.Chat.ID
			tbm.sendMessage(chatID, "❌ 查询股票资产时发生错误，请稍后重试")
		}
	}()

	chatID := update.Message.Chat.ID
	telegramID := update.Message.From.ID

	// 获取用户信息
	if _, err := tbm.db.GetTGUserByTelegramID(telegramID); err != nil {
		tbm.sendMessage(chatID, "❌ 请先使用 /start 初始化账号")
		return
	}

	// 检查是否有 Hyperliquid 账号
	if !tbm.hasHyperliquidAccount(telegramID) {
		tbm.sendMessage(chatID, "❌ 请先使用 /start 完成账号初始化")
		return
	}

	// 发送查询中消息
	tbm.sendMessage(chatID, "🔄 正在获取HIP-3股票资产列表，请稍候...")

	// 提取 Agent Key 和 Wallet Address
	agentKey, walletAddr, err := tbm.extractAgentKeyAndWallet(telegramID)
	if err != nil {
		log.Printf("提取账号信息失败: %v", err)
		tbm.sendMessage(chatID, "❌ 获取账号信息失败，请稍后重试")
		return
	}

	// 获取股票资产信息
	stocksMsg, err := tbm.hlService.GetStockAssets(agentKey, walletAddr, tbm.testnet)
	if err != nil {
		log.Printf("获取股票资产失败: %v", err)
		tbm.sendMessage(chatID, "❌ 获取股票资产失败，请稍后重试")
		return
	}

	// 发送股票资产信息
	tbm.sendMessage(chatID, stocksMsg)
}

// handleLeaderboard 处理 /leaderboard 命令
func (tbm *TelegramBotManager) handleLeaderboard(update tgbotapi.Update) {
	chatID := update.Message.Chat.ID

	if tbm.traderMgr == nil {
		tbm.sendMessage(chatID, "⚠️ 排行榜暂不可用，请稍后重试。")
		return
	}

	tbm.sendMessage(chatID, "🔄 正在加载排行榜，请稍候...")

	// 读取 tg_traders 表的 trader ID 列表
	db, ok := tbm.db.(interface {
		GetAllTGUsers() ([]int64, error)
		GetTgTraders(tgUserID int64) ([]config.TgTraderRecord, error)
	})
	if !ok {
		tbm.sendMessage(chatID, "⚠️ 排行榜暂不可用，请稍后重试。")
		return
	}

	var traderIDs []string
	userIDs, err := db.GetAllTGUsers()
	if err != nil {
		log.Printf("获取TG用户列表失败: %v", err)
		tbm.sendMessage(chatID, "❌ 获取排行榜失败，请稍后重试。")
		return
	}
	for _, uid := range userIDs {
		tgTraders, err := db.GetTgTraders(uid)
		if err != nil {
			log.Printf("获取TG交易员失败 (uid=%d): %v", uid, err)
			continue
		}
		for _, t := range tgTraders {
			traderIDs = append(traderIDs, t.ID)
		}
	}

	data, err := tbm.traderMgr.GetCompetitionDataByIDs(traderIDs)
	if err != nil {
		log.Printf("获取排行榜失败: %v", err)
		tbm.sendMessage(chatID, "❌ 获取排行榜失败，请稍后重试。")
		return
	}

	tradersAny, ok := data["traders"]
	if !ok {
		tbm.sendMessage(chatID, "⚠️ 暂无交易员数据。")
		return
	}

	traders, ok := tradersAny.([]map[string]interface{})
	if !ok || len(traders) == 0 {
		tbm.sendMessage(chatID, "⚠️ 暂无交易员数据。")
		return
	}

	limit := 10
	if len(traders) < limit {
		limit = len(traders)
	}

	var sb strings.Builder
	sb.WriteString("🏆 盈利排行榜（按收益率排名）\n\n")

	for i := 0; i < limit; i++ {
		tr := traders[i]
		name, _ := tr["trader_name"].(string)
		model, _ := tr["ai_model"].(string)
		exchange, _ := tr["exchange"].(string)
		pnlPct, _ := tr["total_pnl_pct"].(float64)
		pnl, _ := tr["total_pnl"].(float64)

		if name == "" {
			name = "未命名交易员"
		}
		if model == "" {
			model = "未配置模型"
		}
		if exchange == "" {
			exchange = "交易所未配置"
		}

		sb.WriteString(fmt.Sprintf("#%d %s\n", i+1, name))
		sb.WriteString(fmt.Sprintf("• 模型: %s\n", model))
		sb.WriteString(fmt.Sprintf("• 交易所: %s\n", exchange))
		sb.WriteString(fmt.Sprintf("• 收益率: %.2f%%\n", pnlPct))
		sb.WriteString(fmt.Sprintf("• 盈亏: %.2f USDT\n\n", pnl))
	}

	tbm.sendMessage(chatID, sb.String())
}

// handleDeposit 处理 /deposit 命令
func (tbm *TelegramBotManager) handleDeposit(update tgbotapi.Update) {
	chatID := update.Message.Chat.ID
	telegramID := update.Message.From.ID

	// 获取用户信息
	if _, err := tbm.db.GetTGUserByTelegramID(telegramID); err != nil {
		tbm.sendMessage(chatID, "❌ 请先使用 /start 初始化账号")
		return
	}

	// 检查是否有 Hyperliquid 账号
	if !tbm.hasHyperliquidAccount(telegramID) {
		tbm.sendMessage(chatID, "❌ 请先使用 /start 完成账号初始化")
		return
	}

	// 提取 Agent Key 和 Wallet Address
	_, walletAddr, err := tbm.extractAgentKeyAndWallet(telegramID)
	if err != nil {
		log.Printf("提取账号信息失败: %v", err)
		tbm.sendMessage(chatID, "❌ 获取充值地址失败，请稍后重试")
		return
	}

	// 生成充值消息（使用与首次创建相同的地址展示格式，方便复制）
	depositMsg := fmt.Sprintf(`💰 USDC 充值地址（Arbitrum 网络）

<code>%s</code>（点击复制）

📋 充值说明:
• 支持资产: USDC
• 支持网络: Arbitrum One
• 最小充值: 20 USDC
• 到账时间: 通常 2-5 分钟

⚠️ 注意事项:
• 请勿充值其他资产到该地址
• 充值后可在 /balance 查看余额`, esc(walletAddr))

	tbm.sendMessage(chatID, depositMsg)
}

// handleMenu 处理 /menu 命令
func (tbm *TelegramBotManager) handleMenu(update tgbotapi.Update) {
	chatID := update.Message.Chat.ID
	telegramID := update.Message.From.ID

	if _, err := tbm.db.GetTGUserByTelegramID(telegramID); err != nil {
		tbm.sendMessage(chatID, "❌ 请先使用 /start 初始化账号")
		return
	}

	if !tbm.hasHyperliquidAccount(telegramID) {
		tbm.sendMessage(chatID, "❌ 请先使用 /start 完成账号初始化")
		return
	}

	menuMsg := `⚙️ <b>设置</b>

选择需要执行的操作：`
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🧠 更新 AI API KEY", fmt.Sprintf("menu_set_api|%d", telegramID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔑 导出 Agent 私钥", fmt.Sprintf("menu_export|%d", telegramID)),
		),
	)

	tbm.sendMessageWithInlineKeyboard(chatID, menuMsg, keyboard)
}

func (tbm *TelegramBotManager) handleExportPrivateKeyRequest(callback *tgbotapi.CallbackQuery, chatID int64, telegramID int64) {
	tbm.answerCallbackQuery(callback.ID, "⚠️ 请确认是否导出")

	if _, err := tbm.db.GetTGUserByTelegramID(telegramID); err != nil {
		tbm.sendMessage(chatID, "❌ 请先使用 /start 初始化账号")
		return
	}

	if !tbm.hasHyperliquidAccount(telegramID) {
		tbm.sendMessage(chatID, "❌ 尚未创建 Hyperliquid 账户，请先使用 /start 完成初始化")
		return
	}

	warning := `⚠️ <b>导出 Agent 私钥</b>

• 私钥一旦泄露，资金将不受保护
• 建议复制后立即删除聊天记录
• 系统将在发送后自动删除该消息`

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ 确认导出", fmt.Sprintf("confirm_export|%d", telegramID)),
		),
	)

	tbm.sendMessageWithInlineKeyboard(chatID, warning, keyboard)
}

// handleSetAPIKey 处理 /set_api_key 命令
func (tbm *TelegramBotManager) handleSetAPIKey(update tgbotapi.Update) {
	chatID := update.Message.Chat.ID
	telegramID := update.Message.From.ID

	tbm.startAPIKeyUpdate(chatID, telegramID)
}

// handleAPIKeyUpdateFlow 处理 API KEY 更新输入
func (tbm *TelegramBotManager) handleAPIKeyUpdateFlow(update tgbotapi.Update, session *UserSession) {
	chatID := update.Message.Chat.ID
	telegramID := update.Message.From.ID
	input := strings.TrimSpace(update.Message.Text)

	sessionMgr := tbm.tgTraderMgr.GetSessionManager()

	if strings.EqualFold(input, "cancel") || input == "取消" {
		sessionMgr.ClearSession(telegramID)
		tbm.sendMessageRemovingKeyboard(chatID, "❌ 已取消 API KEY 更新")
		return
	}

	switch session.State {
	case StateUpdatingAIProvider:
		if provider, ok := detectAIProviderFromInput(input); ok {
			session.TraderConfig.AIProvider = provider
			session.TraderConfig.Step = StateUpdatingAPIKey
			sessionMgr.UpdateTraderConfig(telegramID, session.TraderConfig)
			sessionMgr.UpdateSessionState(telegramID, StateUpdatingAPIKey)
			tbm.sendMessageRemovingKeyboard(chatID, tbm.configWizard.getAPIKeyMessage(provider))
			return
		}

		tbm.sendAIProviderSelectionMessage(chatID, "❌ 无法识别的选项，请点击按钮选择 DeepSeek 或 Qwen，或输入 1 / 2。输入 \"cancel\" 可取消。")
	case StateUpdatingAPIKey:
		provider := normalizeAIProvider(session.TraderConfig.AIProvider)
		if !tbm.configWizard.validateAPIKeyForProvider(provider, input) {
			tbm.sendMessage(chatID, tbm.configWizard.getAPIKeyMessage(provider)+"\n\n"+tbm.configWizard.getAPIKeyFormatHint(provider))
			return
		}

		// 删除包含API KEY的消息
		if session.LastMessageID != 0 {
			deleteConfig := tgbotapi.NewDeleteMessage(chatID, session.LastMessageID)
			if _, err := tbm.bot.Request(deleteConfig); err != nil {
				log.Printf("⚠️ 删除API KEY消息失败 (ChatID: %d, MessageID: %d): %v", chatID, session.LastMessageID, err)
			} else {
				log.Printf("✅ 成功删除API KEY消息 (ChatID: %d, MessageID: %d)", chatID, session.LastMessageID)
			}
		}

		traderRecord, err := tbm.tgTraderMgr.UpdateTraderAPIConfig(telegramID, provider, input, session.TraderConfig.AIModelName)
		if err != nil {
			tbm.sendMessage(chatID, fmt.Sprintf("❌ 更新 API KEY 失败: %s", esc(err)))
			sessionMgr.ClearSession(telegramID)
			return
		}

		sessionMgr.ClearSession(telegramID)

		providerName := aiProviderDisplayName(provider)
		maskedKey := tbm.configWizard.maskAPIKey(input)

		successMsg := fmt.Sprintf(`✅ 已为 %s 更新 %s API KEY
• 新密钥: <code>%s</code>

如交易员正在运行，请执行 /stop_trader 后再 /start_trader 让新密钥生效。`,
			esc(traderRecord.Name),
			esc(providerName),
			esc(maskedKey))

		tbm.sendMessage(chatID, successMsg)
	default:
		sessionMgr.ClearSession(telegramID)
		tbm.sendMessage(chatID, "❌ 会话已过期，请重新使用 /set_api_key")
	}
}

// handleRegularMessage 处理普通消息
func (tbm *TelegramBotManager) handleRegularMessage(update tgbotapi.Update) {
	chatID := update.Message.Chat.ID
	telegramID := update.Message.From.ID
	message := update.Message.Text

	// 检查是否在配置向导过程中
	session := tbm.tgTraderMgr.GetSessionManager().GetOrCreateSession(telegramID)

	// 如果正在输入API KEY，保存消息ID以便后续删除
	if session.State == StateSettingAPIKey || session.State == StateUpdatingAPIKey {
		tbm.tgTraderMgr.GetSessionManager().UpdateLastMessageID(telegramID, update.Message.MessageID)
		log.Printf("🔍 保存API KEY消息ID: %d (用户: %d)", update.Message.MessageID, telegramID)
	}

	// 处理 API KEY 更新流程
	if session.State == StateUpdatingAIProvider || session.State == StateUpdatingAPIKey {
		tbm.handleAPIKeyUpdateFlow(update, session)
		return
	}

	if session.State != StateIdle {
		prevState := session.State

		// 处理配置向导输入
		response, isComplete, err := tbm.configWizard.ProcessInput(telegramID, message)
		if err != nil {
			tbm.sendMessage(chatID, fmt.Sprintf("❌ 处理输入失败: %s", esc(err)))
			return
		}

		if !isComplete && session.State == StateChoosingAIModel {
			tbm.sendAIProviderSelectionMessage(chatID, response)
		} else if prevState == StateChoosingAIModel && session.State != StateChoosingAIModel {
			tbm.sendMessageRemovingKeyboard(chatID, response)
		} else {
			tbm.sendMessage(chatID, response)
		}

		// 如果配置完成，清理会话
		if isComplete {
			tbm.tgTraderMgr.GetSessionManager().ClearSession(telegramID)
		}
		return
	}

	// 尝试解析自然语言交易命令
	if tbm.handleNaturalLanguageCommand(update) {
		return // 如果成功处理了交易命令，直接返回
	}

	// 简单的回复逻辑
	if strings.Contains(strings.ToLower(message), "hello") || strings.Contains(strings.ToLower(message), "你好") {
		tbm.sendMessage(chatID, "👋 你好！使用 /help 查看可用命令。")
	} else {
		tbm.sendMessage(chatID, "💡 我只理解命令。使用 /help 查看可用命令。")
	}
}

// setupCommands 设置 Bot 自定义菜单
func (tbm *TelegramBotManager) setupCommands() {
	commands := []tgbotapi.BotCommand{
		{
			Command:     "start",
			Description: "🚀 开始",
		},
		{
			Command:     "deposit",
			Description: "💳 充值",
		},
		{
			Command:     "balance",
			Description: "💰 账户余额",
		},
		{
			Command:     "positions",
			Description: "💹 当前持仓",
		},
		{
			Command:     "stocks",
			Description: "🏛️ 股票资产",
		},
		{
			Command:     "create_trader",
			Description: "🤖 创建 Agent",
		},
		{
			Command:     "start_trader",
			Description: "▶️ 启动 Agent",
		},
		{
			Command:     "stop_trader",
			Description: "⏹️ 停止 Agent",
		},
		{
			Command:     "trader_status",
			Description: "👁️ 查看 Agent",
		},
		{
			Command:     "leaderboard",
			Description: "🏆 盈利排行榜",
		},
		{
			Command:     "settings",
			Description: "⚙️ 设置",
		},
		{
			Command:     "help",
			Description: "❓ 帮助信息",
		},
	}

	config := tgbotapi.NewSetMyCommands(commands...)
	if _, err := tbm.bot.Request(config); err != nil {
		log.Printf("❌ 设置自定义菜单失败: %v", err)
	} else {
		log.Printf("✅ 自定义菜单设置成功")
	}
}

// sendMessage 发送消息
func (tbm *TelegramBotManager) sendMessage(chatID int64, text string) {
	tbm.sendMessageWithMarkupAndReturnInternal(chatID, text, nil, true, false)
}

// sendMessageWithError 发送消息并返回错误
func (tbm *TelegramBotManager) sendMessageWithError(chatID int64, text string) error {
	_, err := tbm.sendMessageWithMarkupAndReturnInternal(chatID, text, nil, true, false)
	return err
}

// sendMessageWithInlineKeyboard 发送带内联键盘的消息
func (tbm *TelegramBotManager) sendMessageWithInlineKeyboard(chatID int64, text string, keyboard tgbotapi.InlineKeyboardMarkup) {
	tbm.sendMessageWithMarkupAndReturnInternal(chatID, text, keyboard, true, false)
}

// sendAIProviderSelectionMessage 发送AI提供商选择按钮
func (tbm *TelegramBotManager) sendAIProviderSelectionMessage(chatID int64, text string) {
	keyboard := tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("DeepSeek"),
			tgbotapi.NewKeyboardButton("Qwen / 通义千问"),
		),
	)
	keyboard.ResizeKeyboard = true
	keyboard.OneTimeKeyboard = true
	tbm.sendMessageWithMarkupAndReturnInternal(chatID, text, keyboard, true, false)
}

// sendMessageRemovingKeyboard 发送消息并移除键盘
func (tbm *TelegramBotManager) sendMessageRemovingKeyboard(chatID int64, text string) {
	removeKeyboard := tgbotapi.NewRemoveKeyboard(true)
	tbm.sendMessageWithMarkupAndReturnInternal(chatID, text, removeKeyboard, true, false)
}

func (tbm *TelegramBotManager) isSensitiveInput(telegramID int64) bool {
	sessionState := tbm.tgTraderMgr.GetSessionManager().GetSessionStateOnly(telegramID)
	return sessionState == StateUpdatingAPIKey
}

// sendMessageWithMarkup 通用的消息发送方法（可附带自定义键盘）
func (tbm *TelegramBotManager) sendMessageWithMarkup(chatID int64, text string, replyMarkup interface{}) {
	tbm.sendMessageWithMarkupAndReturnInternal(chatID, text, replyMarkup, true, false)
}

// sendMessageWithMarkupAndReturn 发送消息并返回消息对象，便于后续更新
func (tbm *TelegramBotManager) sendMessageWithMarkupAndReturn(chatID int64, text string, replyMarkup interface{}) (*tgbotapi.Message, error) {
	return tbm.sendMessageWithMarkupAndReturnInternal(chatID, text, replyMarkup, true, false)
}

func (tbm *TelegramBotManager) sendSensitiveMessageWithMarkupAndReturn(chatID int64, text string, replyMarkup interface{}) (*tgbotapi.Message, error) {
	return tbm.sendMessageWithMarkupAndReturnInternal(chatID, text, replyMarkup, false, true)
}

func (tbm *TelegramBotManager) sendMessageWithMarkupAndReturnInternal(chatID int64, text string, replyMarkup interface{}, logText bool, suppressDebug bool) (*tgbotapi.Message, error) {
	log.Printf("🔍 [DEBUG] 检查消息UTF-8编码 (ChatID: %d, 长度: %d)", chatID, len(text))
	if !utf8.ValidString(text) {
		log.Printf("❌ [DEBUG] 消息包含无效UTF-8字符！")
		text = strings.ToValidUTF8(text, "�")
		log.Printf("✅ [DEBUG] 已清理无效UTF-8字符，使用清理后的消息")
	} else {
		log.Printf("✅ [DEBUG] 消息UTF-8编码有效")
	}

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "HTML"
	if replyMarkup != nil {
		msg.ReplyMarkup = replyMarkup
	}

	if logText {
		log.Printf("📤 准备发送消息到 ChatID %d: %s", chatID, text)
	} else {
		log.Printf("📤 正在发送敏感消息到 ChatID %d", chatID)
	}

	if suppressDebug {
		tbm.debugMu.Lock()
		originalDebug := tbm.bot.Debug
		tbm.bot.Debug = false
		defer func() {
			tbm.bot.Debug = originalDebug
			tbm.debugMu.Unlock()
		}()
	}

	sentMsg, err := tbm.bot.Send(msg)
	if err != nil {
		// 如果HTML格式失败，重试为普通文本
		if strings.Contains(err.Error(), "can't parse entities") {
			log.Printf("⚠️ HTML格式解析失败，发送纯文本消息 (ChatID: %d)", chatID)
			msg.ParseMode = ""
			sentMsg, err = tbm.bot.Send(msg)
			if err != nil {
				log.Printf("❌ 发送消息失败 (ChatID: %d): %v", chatID, err)
			}
		} else if strings.Contains(err.Error(), "text must be encoded in UTF-8") {
			// 如果仍然是UTF-8编码错误，强制清理
			log.Printf("⚠️ UTF-8编码错误，强制清理消息 (ChatID: %d)", chatID)
			msg.Text = strings.ToValidUTF8(text, "�")
			msg.ParseMode = "" // 使用纯文本
			sentMsg, err = tbm.bot.Send(msg)
			if err != nil {
				log.Printf("❌ 发送消息失败 (ChatID: %d): %v", chatID, err)
			}
		} else {
			log.Printf("❌ 发送消息失败 (ChatID: %d): %v", chatID, err)
		}
	}

	if err == nil {
		if logText {
			log.Printf("✅ 消息发送成功 (ChatID: %d)", chatID)
		} else {
			log.Printf("✅ 敏感消息发送成功 (ChatID: %d)", chatID)
		}
		return &sentMsg, nil
	}

	return nil, err
}

// sendProgressMessage 发送进度提示，返回消息ID以便后续更新
func (tbm *TelegramBotManager) sendProgressMessage(chatID int64, text string) int {
	msg, err := tbm.sendMessageWithMarkupAndReturn(chatID, text, nil)
	if err != nil || msg == nil {
		return 0
	}
	return msg.MessageID
}

// updateProgressMessage 更新进度提示消息内容
func (tbm *TelegramBotManager) updateProgressMessage(chatID int64, messageID int, text string) {
	if messageID == 0 {
		tbm.sendMessage(chatID, text)
		return
	}

	if !utf8.ValidString(text) {
		text = strings.ToValidUTF8(text, "�")
	}

	editConfig := tgbotapi.NewEditMessageText(chatID, messageID, text)
	editConfig.ParseMode = "HTML"

	if _, err := tbm.bot.Send(editConfig); err != nil {
		log.Printf("⚠️ 更新进度消息失败 (ChatID: %d, MsgID: %d): %v", chatID, messageID, err)
		tbm.sendMessage(chatID, text)
	}
}

func (tbm *TelegramBotManager) scheduleDeleteMessage(chatID int64, messageID int, delay time.Duration) {
	if messageID == 0 {
		return
	}

	go func() {
		time.Sleep(delay)
		tbm.deleteMessage(chatID, messageID)
	}()
}

func (tbm *TelegramBotManager) deleteMessage(chatID int64, messageID int) {
	deleteReq := tgbotapi.NewDeleteMessage(chatID, messageID)
	if _, err := tbm.bot.Request(deleteReq); err != nil {
		log.Printf("⚠️ 删除消息失败 (ChatID: %d, MsgID: %d): %v", chatID, messageID, err)
	} else {
		log.Printf("🗑️ 已删除敏感消息 (ChatID: %d, MsgID: %d)", chatID, messageID)
	}
}

// hasHyperliquidAccount 检查用户是否有 Hyperliquid 账号
func (tbm *TelegramBotManager) getPrimaryTrader(telegramID int64) (*config.TgTraderRecord, error) {
	traders, err := tbm.db.GetTgTraders(telegramID)
	if err != nil {
		return nil, err
	}
	if len(traders) == 0 {
		return nil, fmt.Errorf("未找到交易员")
	}
	trader := traders[0]
	return &trader, nil
}

func (tbm *TelegramBotManager) hasHyperliquidAccount(telegramID int64) bool {
	traders, err := tbm.db.GetTgTraders(telegramID)
	if err != nil {
		return false
	}

	for _, trader := range traders {
		if trader.PrivateKey != "" && trader.WalletAddress != "" {
			return true
		}
	}

	return false
}

// extractAgentKeyAndWallet 从用户数据中提取 Agent Key 和 Wallet Address
func (tbm *TelegramBotManager) extractAgentKeyAndWallet(telegramID int64) (agentKey, walletAddr string, err error) {
	trader, err := tbm.getPrimaryTrader(telegramID)
	if err != nil {
		return "", "", err
	}

	if trader.PrivateKey == "" || trader.WalletAddress == "" {
		return "", "", fmt.Errorf("交易员缺少 Hyperliquid 账户信息")
	}

	return trader.PrivateKey, trader.WalletAddress, nil
}

// ensureTraderHasFunds 在启动交易员前检查是否已经充值资金
func (tbm *TelegramBotManager) ensureTraderHasFunds(telegramID int64) error {
	if !tbm.hasHyperliquidAccount(telegramID) {
		return fmt.Errorf("❌ 尚未创建 Hyperliquid 账户，请先使用 /start 完成首次初始化")
	}

	agentKey, walletAddr, err := tbm.extractAgentKeyAndWallet(telegramID)
	if err != nil {
		log.Printf("提取账号信息失败: %v", err)
		return fmt.Errorf("❌ 获取账号信息失败，请稍后重试")
	}

	balanceData, err := tbm.hlService.FetchBalance(agentKey, walletAddr, tbm.testnet)
	if err != nil {
		log.Printf("获取余额失败: %v", err)
		return fmt.Errorf("❌ 查询余额失败，请稍后重试或使用 /balance 查看详情")
	}

	totalBalance, _ := balanceData["totalWalletBalance"].(float64)
	availableBalance, _ := balanceData["availableBalance"].(float64)
	spotBalance, _ := balanceData["spotBalance"].(float64)

	if totalBalance <= 0 {
		msg := "❌ 当前 Hyperliquid 钱包余额为 0 USDC，无法启动交易员。\n\n" +
			"📌 请先完成充值:\n" +
			"1. 使用 /deposit 获取专属充值地址\n" +
			"2. 充值并等待链上确认\n" +
			"3. 使用 /balance 确认到账后，再执行 /start_trader"
		return fmt.Errorf(msg)
	}

	if availableBalance <= 0 {
		if spotBalance > 0 {
			msg := fmt.Sprintf("⚠️ 账户 Spot 余额 %.2f USDC，但合约账户可用余额为 0。\n\n"+
				"请在 Hyperliquid 中将现货资金转入 Perpetuals 账户后再启动交易员。", spotBalance)
			return fmt.Errorf(msg)
		} else {
			return fmt.Errorf("❌ 当前合约账户可用余额为 0，无法启动交易员。请充值或释放保证金后重试。")
		}
	}

	return nil
}

// ensureTraderConfigured 确保交易员已经完成配置
func (tbm *TelegramBotManager) ensureTraderConfigured(chatID int64, telegramID int64) bool {
	trader, err := tbm.getPrimaryTrader(telegramID)
	if err != nil {
		tbm.sendMessage(chatID, "❌ 未找到交易员，请先使用 /start 初始化账号")
		return false
	}

	if !trader.IsConfigured {
		tbm.sendMessage(chatID, "❌ 交易员尚未完成配置，请先使用 /create_trader 完成设置")
		return false
	}

	return true
}

func (tbm *TelegramBotManager) tryAutoBridge(telegramID int64, chatID int64, privateKey, walletAddr string) {
	if tbm.arbService == nil {
		return
	}

	key := fmt.Sprintf("%d:%s", telegramID, strings.ToLower(walletAddr))
	tbm.gasProcMu.Lock()
	if _, exists := tbm.gasProcessing[key]; exists {
		tbm.gasProcMu.Unlock()
		log.Printf("ℹ️ 跳过重复 Gas 赞助请求 (user=%d, wallet=%s)", telegramID, walletAddr)
		return
	}
	tbm.gasProcessing[key] = struct{}{}
	tbm.gasProcMu.Unlock()
	defer func() {
		tbm.gasProcMu.Lock()
		delete(tbm.gasProcessing, key)
		tbm.gasProcMu.Unlock()
	}()

	// 用户级别冷却：提前拦截，避免重复创建记录
	inCooldown, err := tbm.db.HasRecentGasSponsorshipForUser(walletAddr, telegramID, int(gasSponsorshipCooldown.Hours()))
	if err != nil {
		log.Printf("⚠️ 查询 Gas 冷却状态失败: %v", err)
		return
	}
	if inCooldown {
		log.Printf("ℹ️ 用户 %d 地址 %s 仍在 Gas 冷却期内，跳过自动赞助", telegramID, walletAddr)
		return
	}

	usdcBal, err := tbm.arbService.GetUSDCBalance(walletAddr)
	if err != nil {
		log.Printf("⚠️ 查询 USDC 余额失败: %v", err)
		return
	}
	if usdcBal.Cmp(minUSDCBridgeAmount) < 0 {
		return
	}

	record, err := tbm.db.GetActiveGasSponsorshipForUser(walletAddr, telegramID)
	if err != nil {
		log.Printf("⚠️ 查询 Gas 赞助记录失败: %v", err)
	}

	requiredEth := big.NewInt(0)
	if tbm.gasSponsorWei != nil && tbm.gasSponsorWei.Sign() > 0 {
		requiredEth.Set(tbm.gasSponsorWei)
	}

	if record == nil {
		record = &config.TgGasSponsorshipRecord{
			TgUserID:      telegramID,
			WalletAddress: walletAddr,
			AmountWei:     requiredEth.String(),
			USDCAmount:    usdcBal.String(),
			Status:        gasStatusProcessing,
		}
		recordID, err := tbm.db.CreateTgGasSponsorship(record)
		if err != nil {
			log.Printf("⚠️ 创建 Gas 赞助记录失败: %v", err)
			return
		}
		record.ID = recordID
		log.Printf("🧾 创建 Gas 赞助记录: user=%d wallet=%s id=%d", telegramID, walletAddr, recordID)
	} else if record.USDCAmount == "" || usdcBal.Cmp(stringToBig(record.USDCAmount)) > 0 {
		record.USDCAmount = usdcBal.String()
		_ = tbm.db.UpdateTgGasSponsorshipProgress(record.ID, "", "", "", record.USDCAmount)
	}

	targetUSDC := stringToBig(record.USDCAmount)
	if targetUSDC.Sign() == 0 {
		targetUSDC = new(big.Int).Set(usdcBal)
		record.USDCAmount = targetUSDC.String()
		_ = tbm.db.UpdateTgGasSponsorshipProgress(record.ID, "", "", "", record.USDCAmount)
	}

	gasCost, err := tbm.arbService.EstimateUSDCTransferCost(walletAddr, targetUSDC)
	if err != nil {
		log.Printf("⚠️ 估算 USDC 充值 Gas 失败: %v", err)
		return
	}
	if gasCost.Sign() > 0 && gasCost.Cmp(requiredEth) > 0 {
		requiredEth = gasCost
		log.Printf("ℹ️ 使用估算 Gas 费用覆盖配置值: %s wei", gasCost.String())
	}
	if requiredEth.Sign() == 0 {
		log.Printf("⚠️ 无法确定所需 Gas，跳过赞助流程 (user=%d wallet=%s)", telegramID, walletAddr)
		return
	}
	record.AmountWei = requiredEth.String()
	_ = tbm.db.UpdateTgGasSponsorshipProgress(record.ID, "", "", "", record.AmountWei)

	if record.Status == gasStatusProcessing && requiredEth.Sign() > 0 && strings.TrimSpace(tbm.gasSponsorKey) == "" {
		log.Printf("⚠️ 检测到 USDC>=20 但未配置 Gas 赞助账户")
		tbm.sendMessage(chatID, "⚠️ 检测到 USDC 余额满足自动充值，但当前地址缺少 ETH Gas，且系统尚未配置赞助账户。请手动充值少量 ETH 后重试 /balance。")
		return
	}

	ethBal, err := tbm.arbService.GetETHBalance(walletAddr)
	if err != nil {
		log.Printf("⚠️ 查询 ETH 余额失败: %v", err)
		return
	}

	if record.Status == gasStatusProcessing {
		if requiredEth.Sign() == 0 || ethBal.Cmp(requiredEth) >= 0 {
			record.Status = gasStatusGasSent
			_ = tbm.db.UpdateTgGasSponsorshipProgress(record.ID, gasStatusGasSent, "", "", "")
		} else {
			if record.GasTxHash != "" {
				tbm.sendMessage(chatID, "⏳ Gas 赞助已发送，等待到账后再执行 /balance。")
				return
			}
			recent, err := tbm.db.HasRecentGasSponsorshipForUser(walletAddr, telegramID, int(gasSponsorshipCooldown.Hours()))
			if err != nil {
				log.Printf("⚠️ 查询 Gas 赞助记录失败: %v", err)
			}
			if recent {
				tbm.sendMessage(chatID, "⚠️ 该地址近期刚获得 Gas 赞助，请稍后再试。")
				return
			}
			txHash, err := tbm.arbService.SendGas(tbm.gasSponsorKey, walletAddr, requiredEth)
			if err != nil {
				log.Printf("❌ Gas 赞助失败: %v", err)
				_ = tbm.db.UpdateTgGasSponsorshipProgress(record.ID, gasStatusFailed, "", "", "")
				tbm.sendMessage(chatID, fmt.Sprintf("❌ 自动赞助 Gas 失败: %s", esc(err)))
				return
			}
			record.GasTxHash = txHash
			record.Status = gasStatusGasSent
			_ = tbm.db.UpdateTgGasSponsorshipProgress(record.ID, gasStatusGasSent, txHash, "", "")
			tbm.sendMessage(chatID, fmt.Sprintf("⛽ 已赞助 %s ETH 用于 Gas，交易哈希: <code>%s</code>", esc(formatETH(requiredEth)), esc(txHash)))

			time.Sleep(15 * time.Second)
			ethBal, err = tbm.arbService.GetETHBalance(walletAddr)
			if err != nil {
				log.Printf("⚠️ 再次查询 ETH 余额失败: %v", err)
				return
			}
			if ethBal.Cmp(requiredEth) < 0 {
				tbm.sendMessage(chatID, "⚠️ Gas 已发送但余额仍不足，请稍后使用 /balance 重试自动充值。")
				return
			}
		}
	}

	if record.Status != gasStatusGasSent {
		return
	}

	if usdcBal.Cmp(targetUSDC) < 0 {
		tbm.sendMessage(chatID, "⚠️ USDC 余额不足以完成自动充值，请确保金额 ≥ 20 USDC 后重新执行 /balance。")
		_ = tbm.db.UpdateTgGasSponsorshipProgress(record.ID, gasStatusFailed, "", "", "")
		return
	}

	txHash, err := tbm.arbService.TransferUSDC(privateKey, targetUSDC)
	if err != nil {
		log.Printf("❌ 自动充值失败: %v", err)
		_ = tbm.db.UpdateTgGasSponsorshipProgress(record.ID, gasStatusFailed, "", "", "")
		tbm.sendMessage(chatID, fmt.Sprintf("❌ 自动充值失败: %s", esc(err)))
		return
	}

	_ = tbm.db.UpdateTgGasSponsorshipProgress(record.ID, gasStatusCompleted, "", txHash, "")
	tbm.sendMessage(chatID, fmt.Sprintf("💸 已检测到 %s USDC，自动充值至 Hyperliquid。\\nTx: <code>%s</code>", esc(formatUSDC(targetUSDC)), esc(txHash)))

	// 异步刷新余额，避免阻塞主流程
	go func() {
		tbm.refreshHyperliquidBalance(chatID, privateKey, walletAddr)
	}()
}

func (tbm *TelegramBotManager) refreshHyperliquidBalance(chatID int64, agentKey, walletAddr string) {
	if tbm.hlService == nil {
		return
	}

	// 等待一段时间让区块链处理交易
	tbm.sendMessage(chatID, "⏳ 等待区块链确认充值交易...")
	time.Sleep(5 * time.Second)

	// 重试查询余额，最多重试9次
	maxRetries := 9
	intervals := []int{1, 2, 3} // 1s, 2s, 3s 循环

	for attempt := 0; attempt < maxRetries; attempt++ {
		balanceMsg, err := tbm.hlService.GetBalance(agentKey, walletAddr, tbm.testnet)
		if err != nil {
			// 只在后台记录日志，不发送给用户
			log.Printf("⚠️ 余额查询失败 (尝试 %d/%d): %v", attempt+1, maxRetries, err)
			if attempt < maxRetries-1 {
				// 使用1s,2s,3s循环间隔
				retryInterval := time.Duration(intervals[attempt%len(intervals)]) * time.Second
				time.Sleep(retryInterval)
			}
			continue
		}

		// 成功获取余额
		log.Printf("✅ 余额查询成功 (尝试 %d/%d)", attempt+1, maxRetries)
		tbm.sendMessage(chatID, fmt.Sprintf("✅ 充值完成，最新余额如下：\n\n%s", balanceMsg))
		return
	}

	// 所有重试都失败 - 只在后台记录
	log.Printf("❌ 余额查询重试 %d 次后仍然失败", maxRetries)
	tbm.sendMessage(chatID, "⚠️ 无法自动刷新 Hyperliquid 余额，请稍后使用 /balance 重试。")
}

func (tbm *TelegramBotManager) startAPIKeyUpdate(chatID int64, telegramID int64) bool {
	if _, err := tbm.db.GetTGUserByTelegramID(telegramID); err != nil {
		tbm.sendMessage(chatID, "❌ 请先使用 /start 与机器人建立连接")
		return false
	}

	trader, err := tbm.getPrimaryTrader(telegramID)
	if err != nil {
		tbm.sendMessage(chatID, "❌ 您还没有创建交易员，请先使用 /start 初始化账号")
		return false
	}

	if !trader.IsConfigured {
		tbm.sendMessage(chatID, "❌ 交易员尚未配置，请先使用 /create_trader 完成设置")
		return false
	}

	sessionMgr := tbm.tgTraderMgr.GetSessionManager()
	sessionMgr.ClearSession(telegramID)

	session := sessionMgr.GetOrCreateSession(telegramID)
	provider := normalizeAIProvider(trader.AIModelID)
	session.TraderConfig.AIProvider = provider
	session.TraderConfig.AIModelAPIKey = trader.AIModelAPIKey
	session.TraderConfig.AIModelAPIURL = trader.AIModelAPIURL
	session.TraderConfig.AIModelName = trader.AIModelName
	session.TraderConfig.Step = StateUpdatingAIProvider
	sessionMgr.UpdateTraderConfig(telegramID, session.TraderConfig)
	sessionMgr.UpdateSessionState(telegramID, StateUpdatingAIProvider)

	providerName := aiProviderDisplayName(provider)
	maskedKey := "未设置"
	if trader.AIModelAPIKey != "" {
		maskedKey = tbm.configWizard.maskAPIKey(trader.AIModelAPIKey)
	}

	message := fmt.Sprintf(`🔐 <b>更新 AI API KEY</b>

当前交易员: %s
当前模型: %s
当前密钥: %s

请选择要使用的大模型：
1. DeepSeek（默认）
2. Qwen / 通义千问

点击下方按钮或回复 1 / 2，输入 "cancel" 可取消。`,
		esc(trader.Name),
		esc(providerName),
		esc(maskedKey))

	tbm.sendAIProviderSelectionMessage(chatID, message)
	return true
}

func formatUSDC(amount *big.Int) string {
	f := new(big.Rat).SetFrac(amount, big.NewInt(1_000_000))
	floatVal, _ := f.Float64()
	return fmt.Sprintf("%.2f", floatVal)
}

func formatETH(amount *big.Int) string {
	if amount == nil {
		return "0"
	}
	f := new(big.Rat).SetFrac(amount, big.NewInt(1_000_000_000_000_000_000))
	floatVal, _ := f.Float64()
	return fmt.Sprintf("%.8f", floatVal)
}

func stringToBig(value string) *big.Int {
	if strings.TrimSpace(value) == "" {
		return new(big.Int)
	}
	if bi, ok := new(big.Int).SetString(value, 10); ok {
		return bi
	}
	return new(big.Int)
}

// handleCreateTrader 处理 /create_trader 命令
func (tbm *TelegramBotManager) handleCreateTrader(update tgbotapi.Update) {
	chatID := update.Message.Chat.ID
	telegramID := update.Message.From.ID

	// 检查用户是否已存在
	_, err := tbm.db.GetTGUserByTelegramID(telegramID)
	if err != nil {
		tbm.sendMessage(chatID, "❌ 请先使用 /start 初始化账号")
		return
	}

	// 启动配置向导
	message, err := tbm.configWizard.StartWizard(telegramID)
	if err != nil {
		log.Printf("启动配置向导失败: %v", err)
		tbm.sendMessage(chatID, "❌ 启动配置向导失败，请稍后重试")
		return
	}

	tbm.sendMessage(chatID, message)
}

// handleStartTrader 处理 /start_trader 命令
func (tbm *TelegramBotManager) handleStartTrader(update tgbotapi.Update) {
	chatID := update.Message.Chat.ID
	telegramID := update.Message.From.ID

	// 检查用户是否已存在
	_, err := tbm.db.GetTGUserByTelegramID(telegramID)
	if err != nil {
		tbm.sendMessage(chatID, "❌ 请先使用 /start 初始化账号")
		return
	}

	if !tbm.ensureTraderConfigured(chatID, telegramID) {
		return
	}

	if !tbm.ensureTraderConfigured(chatID, telegramID) {
		return
	}

	progressMsgID := tbm.sendProgressMessage(chatID, "⏳ <b>启动中...</b>")

	if err := tbm.ensureTraderHasFunds(telegramID); err != nil {
		tbm.updateProgressMessage(chatID, progressMsgID, err.Error())
		return
	}

	// 启动交易员
	err = tbm.tgTraderMgr.StartTrader(telegramID)
	if err != nil {
		if err.Error() == "交易员已经在运行中" {
			tbm.updateProgressMessage(chatID, progressMsgID, "✅ 交易员已经在运行中！使用 /trader_status 查看运行状态")
		} else {
			tbm.updateProgressMessage(chatID, progressMsgID, fmt.Sprintf("❌ 启动交易员失败: %s", esc(err)))
		}
		return
	}

	tbm.updateProgressMessage(chatID, progressMsgID, "🚀 交易员启动成功！使用 /trader_status 查看运行状态")
}

// handleStopTrader 处理 /stop_trader 命令
func (tbm *TelegramBotManager) handleStopTrader(update tgbotapi.Update) {
	chatID := update.Message.Chat.ID
	telegramID := update.Message.From.ID

	// 检查用户是否已存在
	_, err := tbm.db.GetTGUserByTelegramID(telegramID)
	if err != nil {
		tbm.sendMessage(chatID, "❌ 请先使用 /start 初始化账号")
		return
	}

	// 检查交易员状态
	status, err := tbm.tgTraderMgr.GetTraderStatus(telegramID)
	if err != nil {
		tbm.sendMessage(chatID, fmt.Sprintf("❌ 获取交易员状态失败: %s", esc(err)))
		return
	}

	// 如果交易员不存在，提示用户先创建
	if !status["has_trader"].(bool) {
		tbm.sendMessage(chatID, "❌ 您还没有创建交易员，请先使用 /start 初始化账号")
		return
	}

	// 如果交易员已经停止，显示友好消息
	if !status["is_running"].(bool) {
		tbm.sendMessage(chatID, "✅ 交易员已经停止")
		return
	}

	// 检查是否有未平仓位
	hasPositions, positionCount, err := tbm.tgTraderMgr.CheckPositionsBeforeStop(telegramID)
	if err != nil {
		tbm.sendMessage(chatID, fmt.Sprintf("❌ 检查仓位失败: %s", esc(err)))
		return
	}

	// 如果没有仓位，直接停止
	if !hasPositions {
		if err := tbm.tgTraderMgr.StopTrader(telegramID); err != nil {
			tbm.sendMessage(chatID, fmt.Sprintf("❌ 停止交易员失败: %s", esc(err)))
			return
		}
		tbm.sendMessage(chatID, "⏹️ 交易员已停止")
		return
	}

	// 有仓位，发送确认消息
	tbm.sendStopConfirmationMessage(chatID, telegramID, positionCount)
}

// handleTraderStatus 处理 /trader_status 命令
func (tbm *TelegramBotManager) handleTraderStatus(update tgbotapi.Update) {
	chatID := update.Message.Chat.ID
	telegramID := update.Message.From.ID

	// 检查用户是否已存在
	_, err := tbm.db.GetTGUserByTelegramID(telegramID)
	if err != nil {
		tbm.sendMessage(chatID, "❌ 请先使用 /start 初始化账号")
		return
	}

	// 获取交易员状态
	status, err := tbm.tgTraderMgr.GetTraderStatus(telegramID)
	if err != nil {
		tbm.sendMessage(chatID, fmt.Sprintf("❌ 获取交易员状态失败: %s", esc(err)))
		return
	}

	if !status["has_trader"].(bool) {
		noTraderMsg := `🤖 <b>交易员状态</b>

📊 您还没有创建交易员

💡 <i>下一步操作:</i>
/create_trader - 创建新的 AI 交易员`
		tbm.sendMessage(chatID, noTraderMsg)
		return
	}

	if configured, ok := status["is_configured"].(bool); ok && !configured {
		walletAddr := ""
		if addr, ok := status["wallet_address"].(string); ok {
			walletAddr = addr
		}
		msg := fmt.Sprintf(`🤖 <b>交易员待配置</b>

• 钱包地址: <code>%s</code>
• 状态: 💤 未配置

💡 请使用 /create_trader 完成模型与策略配置
• /deposit - 充值 USDC
• /balance - 查看账户余额`, esc(walletAddr))
		tbm.sendMessage(chatID, msg)
		return
	}

	// 格式化状态消息
	statusEmoji := "⏹️"
	if status["is_running"].(bool) {
		statusEmoji = "🟢"
	}

	traderStatusMsg := fmt.Sprintf(`🤖 <b>交易员状态</b>

📊 基本信息
• 名称: %s
• 状态: %s %s
• 初始资金: %s USDC
• 策略: %s

⚙️ 配置参数
• BTC/ETH杠杆: %s倍
• 山寨币杠杆: %s倍
• 扫描间隔: %s 分钟`,
		esc(status["name"]),
		esc(statusEmoji),
		esc(status["status"]),
		esc(fmt.Sprintf("%.0f", status["initial_balance"])),
		esc(tbm.getPromptDisplayName(status["prompt_template"].(string))),
		esc(fmt.Sprintf("%v", status["btc_eth_leverage"])),
		esc(fmt.Sprintf("%v", status["altcoin_leverage"])),
		esc(fmt.Sprintf("%v", status["scan_interval_minutes"])),
	)

	// 如果有实时状态，添加详细信息
	if currentStatus, ok := status["current_status"]; ok {
		if currentStatusMap, ok := currentStatus.(map[string]interface{}); ok {
			traderStatusMsg += fmt.Sprintf(`

📈 <b>实时状态</b>
• 调用次数: %s
• 最后决策时间: %s`,
				esc(currentStatusMap["call_count"]),
				esc(currentStatusMap["last_decision_time"]),
			)
		}
	}

	buttonText := "▶️ 启动交易员"
	buttonAction := "start_trader"
	if status["is_running"].(bool) {
		buttonText = "⏹️ 停止交易员"
		buttonAction = "stop_trader"
	}

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(buttonText, fmt.Sprintf("%s|%d", buttonAction, telegramID)),
		),
	)

	tbm.sendMessageWithInlineKeyboard(chatID, traderStatusMsg, keyboard)
}

// getPromptDisplayName 获取提示词显示名称
func (tbm *TelegramBotManager) getPromptDisplayName(templateName string) string {
	templates := GetAvailablePromptTemplates()
	for _, template := range templates {
		if template.Name == templateName {
			return template.DisplayName
		}
	}
	return templateName
}

// PushDecisionToUser 推送AI决策到指定用户
func (tbm *TelegramBotManager) PushDecisionToUser(telegramID int64, decisionMsg string) error {
	if tbm.bot == nil {
		return fmt.Errorf("Telegram Bot未初始化")
	}

	// ⚠️ 调试UTF-8编码问题：记录消息的UTF-8有效性
	log.Printf("🔍 [DEBUG] 检查消息UTF-8编码 (ChatID: %d, 长度: %d)", telegramID, len(decisionMsg))
	if !utf8.ValidString(decisionMsg) {
		log.Printf("❌ [DEBUG] 消息包含无效UTF-8字符！")
		// 清理无效字符
		cleanMsg := strings.ToValidUTF8(decisionMsg, "�")
		log.Printf("✅ [DEBUG] 已清理无效UTF-8字符，使用清理后的消息")
		decisionMsg = cleanMsg
	} else {
		log.Printf("✅ [DEBUG] 消息UTF-8编码有效")
	}

	chunks := splitDecisionMessage(decisionMsg, telegramMessageChunkSize)
	totalChunks := len(chunks)

	if totalChunks > 1 {
		log.Printf("📝 决策消息较长，准备分段发送 (%d 段)", totalChunks)
	}

	successCount := 0
	failedChunks := make([]int, 0)

	for i, chunk := range chunks {
		chunkText := formatDecisionChunk(chunk, i, totalChunks)
		chunkSize := len(chunkText)

		log.Printf("📤 发送第 %d/%d 段 (大小: %d 字符, ChatID: %d)",
			i+1, totalChunks, chunkSize, telegramID)

		// Add delay between chunks to respect rate limits
		if i > 0 {
			time.Sleep(1 * time.Second)
		}

		// Track success/failure
		if err := tbm.sendMessageWithError(telegramID, chunkText); err != nil {
			log.Printf("❌ 第 %d 段发送失败: %v", i+1, err)
			failedChunks = append(failedChunks, i+1)
		} else {
			successCount++
			log.Printf("✅ 第 %d/%d 段发送成功", i+1, totalChunks)
		}
	}

	// Summary logging
	if len(failedChunks) > 0 {
		log.Printf("⚠️ 部分段落发送失败 - 成功: %d/%d, 失败段号: %v",
			successCount, totalChunks, failedChunks)
		return fmt.Errorf("部分段落发送失败: %v", failedChunks)
	}

	log.Printf("✅ 成功推送AI决策到Telegram (ChatID: %d, 段数: %d)", telegramID, successCount)
	return nil
}

// GetTraderManager 获取TraderManager实例
func (tbm *TelegramBotManager) GetTraderManager() *manager.TraderManager {
	return tbm.traderMgr
}

// GetTelegramTraderManager 获取Telegram交易员管理器
func (tbm *TelegramBotManager) GetTelegramTraderManager() *TelegramTraderManager {
	return tbm.tgTraderMgr
}

// sendStopConfirmationMessage 发送停止确认消息
func (tbm *TelegramBotManager) sendStopConfirmationMessage(chatID int64, telegramID int64, positionCount int) {
	// 创建内联键盘
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔄 平仓并停止", fmt.Sprintf("stop_with_close|%d", telegramID)),
			tgbotapi.NewInlineKeyboardButtonData("⏹️ 仅停止交易", fmt.Sprintf("stop_only|%d", telegramID)),
		),
	)

	// 构建确认消息
	messageText := fmt.Sprintf(`⚠️ <b>停止交易员确认</b>

📊 检测到您有 %s 个未平仓位

请选择停止方式：`, esc(positionCount))

	// 发送带有内联键盘的消息
	msg := tgbotapi.NewMessage(chatID, messageText)
	msg.ParseMode = "HTML"
	msg.ReplyMarkup = keyboard

	if _, err := tbm.bot.Send(msg); err != nil {
		log.Printf("❌ 发送停止确认消息失败 (ChatID: %d): %v", chatID, err)
		// 如果内联键盘失败，发送普通消息
		tbm.sendMessage(chatID, fmt.Sprintf("检测到您有 %s 个未平仓位。请手动确认是否平仓后停止交易员。", esc(positionCount)))
	} else {
		log.Printf("✅ 发送停止确认消息成功 (ChatID: %d)", chatID)
	}
}

// handleCallbackQuery 处理回调查询
func (tbm *TelegramBotManager) handleCallbackQuery(update tgbotapi.Update) {
	callback := update.CallbackQuery
	if callback == nil {
		return
	}

	chatID := callback.Message.Chat.ID
	userID := callback.From.ID
	data := callback.Data

	log.Printf("🔍 收到回调查询 (ChatID: %d, UserID: %d, Data: %s)", chatID, userID, data)

	// 解析回调数据
	parts := strings.Split(data, "|")
	if len(parts) != 2 {
		log.Printf("❌ 无效的回调数据格式: %s", data)
		tbm.answerCallbackQuery(callback.ID, "无效的请求")
		return
	}

	action, telegramIDStr := parts[0], parts[1]
	telegramID, err := strconv.ParseInt(telegramIDStr, 10, 64)
	if err != nil {
		log.Printf("❌ 解析用户ID失败: %v", err)
		tbm.answerCallbackQuery(callback.ID, "请求格式错误")
		return
	}

	// 验证用户身份
	if userID != telegramID {
		log.Printf("❌ 用户身份验证失败: 请求用户 %d, 目标用户 %d", userID, telegramID)
		tbm.answerCallbackQuery(callback.ID, "权限不足")
		return
	}

	// 处理不同的动作
	switch action {
	case "start_trader":
		tbm.handleStartTraderCallback(callback, chatID, telegramID)
	case "stop_trader":
		tbm.handleStopTraderCallback(callback, chatID, telegramID)
	case "stop_with_close":
		tbm.handleStopWithClose(callback, chatID, telegramID)
	case "stop_only":
		tbm.handleStopOnly(callback, chatID, telegramID)
	case "menu_export":
		tbm.handleExportPrivateKeyRequest(callback, chatID, telegramID)
	case "confirm_export":
		tbm.handleExportPrivateKeyCallback(callback, chatID, telegramID)
	case "ack_export":
		tbm.handleAcknowledgePrivateKey(callback, chatID, telegramID)
	case "menu_set_api":
		tbm.handleMenuSetAPI(callback, chatID, telegramID)
	default:
		log.Printf("❌ 未知动作: %s", action)
		tbm.answerCallbackQuery(callback.ID, "未知操作")
	}
}

func (tbm *TelegramBotManager) handleStartTraderCallback(callback *tgbotapi.CallbackQuery, chatID int64, telegramID int64) {
	tbm.answerCallbackQuery(callback.ID, "▶️ 正在启动交易员...")

	if _, err := tbm.db.GetTGUserByTelegramID(telegramID); err != nil {
		tbm.sendMessage(chatID, "❌ 请先使用 /start 初始化账号")
		return
	}

	progressMsgID := tbm.sendProgressMessage(chatID, "⏳ <b>启动中...</b>")

	if err := tbm.ensureTraderHasFunds(telegramID); err != nil {
		tbm.updateProgressMessage(chatID, progressMsgID, err.Error())
		return
	}

	if err := tbm.tgTraderMgr.StartTrader(telegramID); err != nil {
		if err.Error() == "交易员已经在运行中" {
			tbm.updateProgressMessage(chatID, progressMsgID, "✅ 交易员已经在运行中！使用 /trader_status 查看运行状态")
		} else {
			tbm.updateProgressMessage(chatID, progressMsgID, fmt.Sprintf("❌ 启动交易员失败: %s", esc(err)))
		}
		return
	}

	tbm.updateProgressMessage(chatID, progressMsgID, "🚀 交易员启动成功！使用 /trader_status 查看运行状态")
}

func (tbm *TelegramBotManager) handleExportPrivateKeyCallback(callback *tgbotapi.CallbackQuery, chatID int64, telegramID int64) {
	tbm.answerCallbackQuery(callback.ID, "🔐 正在准备私钥...")

	if _, err := tbm.db.GetTGUserByTelegramID(telegramID); err != nil {
		tbm.sendMessage(chatID, "❌ 请先使用 /start 初始化账号")
		return
	}

	if !tbm.hasHyperliquidAccount(telegramID) {
		tbm.sendMessage(chatID, "❌ 尚未创建 Hyperliquid 账户，请先使用 /start 完成初始化")
		return
	}

	privateKey, walletAddr, err := tbm.extractAgentKeyAndWallet(telegramID)
	if err != nil {
		tbm.sendMessage(chatID, fmt.Sprintf("❌ 获取账户信息失败: %s", esc(err)))
		return
	}

	keyToShow := strings.TrimSpace(privateKey)
	if keyToShow == "" {
		tbm.sendMessage(chatID, "❌ 未找到可导出的私钥，请稍后重试")
		return
	}
	if !strings.HasPrefix(strings.ToLower(keyToShow), "0x") {
		keyToShow = "0x" + keyToShow
	}

	message := fmt.Sprintf(`⚠️ <b>私钥导出</b>

• 请勿泄露此私钥
• 请在复制后立即删除本聊天记录
• 点击“已安全备份”即可立即删除此消息
• 系统也会在 10 秒后自动删除

Agent 私钥:
<code>%s</code>

钱包地址:
<code>%s</code>`,
		esc(keyToShow),
		esc(walletAddr),
	)

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🗑️ 已安全备份", fmt.Sprintf("ack_export|%d", telegramID)),
		),
	)

	sentMsg, err := tbm.sendSensitiveMessageWithMarkupAndReturn(chatID, message, keyboard)
	if err != nil || sentMsg == nil {
		return
	}
	tbm.scheduleDeleteMessage(chatID, sentMsg.MessageID, 10*time.Second)
}

func (tbm *TelegramBotManager) handleAcknowledgePrivateKey(callback *tgbotapi.CallbackQuery, chatID int64, telegramID int64) {
	tbm.answerCallbackQuery(callback.ID, "🗑️ 已删除敏感消息")
	if callback.Message != nil {
		tbm.deleteMessage(chatID, callback.Message.MessageID)
	}
}

func (tbm *TelegramBotManager) handleMenuSetAPI(callback *tgbotapi.CallbackQuery, chatID int64, telegramID int64) {
	tbm.answerCallbackQuery(callback.ID, "🔐 正在打开 AI 配置...")
	tbm.startAPIKeyUpdate(chatID, telegramID)
}

func (tbm *TelegramBotManager) handleStopTraderCallback(callback *tgbotapi.CallbackQuery, chatID int64, telegramID int64) {
	tbm.answerCallbackQuery(callback.ID, "⏹️ 正在检查交易员状态...")

	if _, err := tbm.db.GetTGUserByTelegramID(telegramID); err != nil {
		tbm.sendMessage(chatID, "❌ 请先使用 /start 初始化账号")
		return
	}

	status, err := tbm.tgTraderMgr.GetTraderStatus(telegramID)
	if err != nil {
		tbm.sendMessage(chatID, fmt.Sprintf("❌ 获取交易员状态失败: %s", esc(err)))
		return
	}

	if !status["has_trader"].(bool) {
		tbm.sendMessage(chatID, "❌ 您还没有创建交易员，请先使用 /start 初始化账号")
		return
	}

	if !status["is_running"].(bool) {
		tbm.sendMessage(chatID, "✅ 交易员已经停止")
		return
	}

	hasPositions, positionCount, err := tbm.tgTraderMgr.CheckPositionsBeforeStop(telegramID)
	if err != nil {
		tbm.sendMessage(chatID, fmt.Sprintf("❌ 检查仓位失败: %s", esc(err)))
		return
	}

	if !hasPositions {
		if err := tbm.tgTraderMgr.StopTrader(telegramID); err != nil {
			tbm.sendMessage(chatID, fmt.Sprintf("❌ 停止交易员失败: %s", esc(err)))
			return
		}
		tbm.sendMessage(chatID, "⏹️ 交易员已停止")
		return
	}

	tbm.sendStopConfirmationMessage(chatID, telegramID, positionCount)
}

// handleStopWithClose 处理平仓并停止
func (tbm *TelegramBotManager) handleStopWithClose(callback *tgbotapi.CallbackQuery, chatID int64, telegramID int64) {
	// 先回答回调，显示"正在处理"
	tbm.answerCallbackQuery(callback.ID, "🔄 正在平仓并停止交易员...")

	// 执行平仓并停止
	closeResults, err := tbm.tgTraderMgr.StopTraderWithPositions(telegramID)
	if err != nil {
		errorMsg := fmt.Sprintf("❌ 平仓并停止交易员失败: %s", esc(err))
		tbm.sendMessage(chatID, errorMsg)

		// 编辑原消息显示错误
		tbm.editCallbackMessage(callback.Message.MessageID, chatID, "❌ 操作失败: "+err.Error())
		return
	}

	// 构建成功消息
	var successMsg strings.Builder

	if len(closeResults) > 0 {
		// 有仓位被平仓的情况
		successMsg.WriteString("✅ 交易员已停止，仓位平仓完成\n\n")
		successMsg.WriteString("📋 平仓结果:\n")
		for i, result := range closeResults {
			successMsg.WriteString(fmt.Sprintf("%d. %s\n", i+1, result))
		}
		successMsg.WriteString("\n⏹️ 交易员已完全停止运行")
	} else {
		// 无需平仓的情况 - 无未平仓合约
		successMsg.WriteString("✅ 交易员已停止\n\n")
		successMsg.WriteString("ℹ️ 无未平仓合约，交易员已安全停止")
	}

	// 发送成功消息
	tbm.sendMessage(chatID, successMsg.String())

	// 编辑原消息显示成功
	if len(closeResults) > 0 {
		tbm.editCallbackMessage(callback.Message.MessageID, chatID, "✅ 已成功平仓并停止交易员")
	} else {
		tbm.editCallbackMessage(callback.Message.MessageID, chatID, "✅ 交易员已安全停止（无未平仓合约）")
	}
}

// handleStopOnly 处理仅停止交易
func (tbm *TelegramBotManager) handleStopOnly(callback *tgbotapi.CallbackQuery, chatID int64, telegramID int64) {
	// 先回答回调，显示"正在处理"
	tbm.answerCallbackQuery(callback.ID, "⏹️ 正在停止交易员...")

	// 仅停止交易员，不平仓
	err := tbm.tgTraderMgr.StopTrader(telegramID)
	if err != nil {
		errorMsg := fmt.Sprintf("❌ 停止交易员失败: %s", esc(err))
		tbm.sendMessage(chatID, errorMsg)

		// 编辑原消息显示错误
		tbm.editCallbackMessage(callback.Message.MessageID, chatID, "❌ 操作失败: "+err.Error())
		return
	}

	// 发送成功消息
	successMsg := "⏹️ 交易员已停止\n\n" +
		"⚠️ 重要提醒: 您的未平仓位仍然保留\n" +
		"• 请密切关注市场变化\n" +
		"• 可随时手动平仓\n" +
		"• 使用 /positions 查看当前持仓"

	tbm.sendMessage(chatID, successMsg)

	// 编辑原消息显示成功
	tbm.editCallbackMessage(callback.Message.MessageID, chatID, "✅ 交易员已停止")
}

// answerCallbackQuery 回答回调查询
func (tbm *TelegramBotManager) answerCallbackQuery(callbackID string, text string) {
	callbackConfig := tgbotapi.NewCallback(callbackID, text)
	if _, err := tbm.bot.Request(callbackConfig); err != nil {
		log.Printf("❌ 回答回调查询失败 (CallbackID: %s): %v", callbackID, err)
	}
}

// editCallbackMessage 编辑回调消息
func (tbm *TelegramBotManager) editCallbackMessage(messageID int, chatID int64, text string) {
	editConfig := tgbotapi.NewEditMessageText(chatID, messageID, text)
	editConfig.ParseMode = "HTML"

	if _, err := tbm.bot.Request(editConfig); err != nil {
		log.Printf("⚠️ 编辑消息HTML格式失败，尝试纯文本 (MessageID: %d)", messageID)
		editConfig.ParseMode = ""
		if _, err2 := tbm.bot.Request(editConfig); err2 != nil {
			log.Printf("❌ 编辑回调消息失败 (MessageID: %d): %v", messageID, err2)
		}
	} else {
		log.Printf("✅ 编辑回调消息成功 (MessageID: %d)", messageID)
	}
}

func splitMessageIntoChunks(text string, chunkSize int) []string {
	if chunkSize <= 0 {
		return []string{text}
	}

	lines := strings.Split(text, "\n")
	var chunks []string
	var builder strings.Builder

	for _, line := range lines {
		lineLen := len(line)
		if builder.Len() > 0 {
			lineLen++ // newline char
		}

		if builder.Len() > 0 && builder.Len()+lineLen > chunkSize {
			chunks = append(chunks, builder.String())
			builder.Reset()
		}

		if builder.Len() > 0 {
			builder.WriteString("\n")
		}
		builder.WriteString(line)
	}

	if builder.Len() > 0 {
		chunks = append(chunks, builder.String())
	}

	if len(chunks) == 0 {
		return []string{""}
	}

	return chunks
}

func splitDecisionMessage(text string, chunkSize int) []decisionChunk {
	if chunkSize <= 0 {
		return []decisionChunk{{Text: text}}
	}

	if len(text) <= chunkSize {
		return []decisionChunk{{Text: text}}
	}

	markerIdx := strings.Index(text, decisionJSONMarker)

	if markerIdx <= 0 {
		return wrapPlainChunks(splitMessageIntoChunks(text, chunkSize), false)
	}

	var leadingChunks []string

	before := strings.TrimRight(text[:markerIdx], "\n")
	if strings.TrimSpace(before) != "" {
		leadingChunks = splitMessageIntoChunks(before, chunkSize)
	}

	after := strings.TrimLeft(text[markerIdx:], "\n")
	if strings.TrimSpace(after) == "" {
		return wrapPlainChunks(leadingChunks, false)
	}

	after = strings.TrimPrefix(after, decisionJSONMarker)
	after = strings.TrimLeft(after, "\n")

	jsonChunks := splitMessageIntoChunks(after, chunkSize)

	result := wrapPlainChunks(leadingChunks, false)
	result = append(result, wrapPlainChunks(jsonChunks, true)...)

	if len(result) == 0 {
		return []decisionChunk{{Text: text}}
	}

	return result
}

func wrapPlainChunks(chunks []string, isJSON bool) []decisionChunk {
	result := make([]decisionChunk, 0, len(chunks))
	for _, chunk := range chunks {
		result = append(result, decisionChunk{
			Text:   chunk,
			IsJSON: isJSON,
		})
	}
	return result
}

func formatDecisionChunk(chunk decisionChunk, index int, total int) string {
	content := chunk.Text
	header := ""

	if total > 1 {
		if chunk.IsJSON {
			header = fmt.Sprintf("%s（第%d/%d段）", decisionJSONMarker, index+1, total)
		} else {
			header = fmt.Sprintf("📄 第 %d/%d 段", index+1, total)
		}
	}

	formattedContent := strings.TrimLeft(content, "\n")
	if formattedContent == "" {
		formattedContent = content
	}

	preBlock := fmt.Sprintf("<pre>%s</pre>", esc(formattedContent))
	if header != "" {
		return fmt.Sprintf("%s\n%s", header, preBlock)
	}
	return preBlock
}

// maskPrivateKey 隐藏私钥的敏感部分用于日志记录
// 输入: "abcdef1234567890"
// 输出: "abcd****7890"
func maskPrivateKey(privateKey string) string {
	if len(privateKey) == 0 {
		return ""
	}
	// 完全隐藏私钥，只用于调试时的临时显示，不记录到日志
	// 返回固定长度的掩码，不泄露任何实际字符
	return "[PRIVATE_KEY_HIDDEN]"
}

// handleNaturalLanguageCommand 处理自然语言交易命令
// 返回 true 表示成功处理了交易命令，false 表示不是交易命令
func (tbm *TelegramBotManager) handleNaturalLanguageCommand(update tgbotapi.Update) bool {
	chatID := update.Message.Chat.ID
	telegramID := update.Message.From.ID
	message := update.Message.Text

	// 确保解析器可用（允许无 MCP 时使用正则后备）
	if tbm.nlParser == nil {
		tbm.nlParser = NewNLParser(nil)
	}
	if !tbm.nlParser.IsEnabled() {
		return false
	}

	// 记录原始消息
	log.Printf("🤖 检测自然语言命令 [用户:%d]: %s", telegramID, message)

	// 解析命令
	cmd, err := tbm.nlParser.ParseCommand(message)
	if err != nil {
		log.Printf("❌ 解析失败: %v", err)
		return false
	}

	if cmd == nil {
		// 不是交易命令
		return false
	}

	// 记录解析结果
	tbm.nlParser.LogCommand(telegramID, message, cmd)

	// 验证命令
	if err := tbm.cmdValidator.ValidateCommand(telegramID, cmd); err != nil {
		tbm.cmdValidator.LogValidation(telegramID, cmd, err)
		tbm.sendMessage(chatID, fmt.Sprintf("❌ 命令验证失败: %s", esc(err)))
		return true // 已处理，虽然是错误
	}

	// 获取风险警告
	warnings := tbm.cmdValidator.GetRiskWarnings(cmd)
	if len(warnings) > 0 {
		warningMsg := strings.Join(warnings, "\n")
		tbm.sendMessage(chatID, warningMsg)
	}

	// 检查是否需要确认
	if tbm.cmdValidator.NeedsConfirmation(cmd) {
		confirmationMsg := tbm.nlParser.GetConfirmationMessage(cmd)
		confirmationMsg += "\n\n⚠️ 请回复 '确认' 或 'cancel' 来继续或取消操作。"

		// 保存待确认的命令到会话
		session := tbm.tgTraderMgr.GetSessionManager().GetOrCreateSession(telegramID)
		session.State = "awaiting_trade_confirmation"
		// TODO: 需要在会话中保存命令对象

		tbm.sendMessage(chatID, confirmationMsg)
		return true
	}

	// 直接执行交易
	return tbm.executeNaturalLanguageCommand(chatID, telegramID, cmd)
}

// getMCPClientForUser 获取用户的 MCP 客户端
func (tbm *TelegramBotManager) getMCPClientForUser(telegramID int64) (*mcp.Client, error) {
	// 1. 获取用户的 TG 交易员记录
	tgTraders, err := tbm.db.GetTgTraders(telegramID)
	if err != nil || len(tgTraders) == 0 {
		return nil, nil
	}

	// 2. 暂时简化逻辑：返回 nil，让解析器使用正则表达式模式
	// 这样可以避免访问私有字段的问题
	log.Printf("⚠️ 未配置 MCP 客户端，使用正则解析 [用户:%d]", telegramID)

	return nil, nil
}

// executeNaturalLanguageCommand 执行自然语言交易命令
func (tbm *TelegramBotManager) executeNaturalLanguageCommand(chatID int64, telegramID int64, cmd *ParsedCommand) bool {
	log.Printf("🚀 执行自然语言交易命令 [用户:%d]: %s %s", telegramID, cmd.Action, cmd.Symbol)

	// 1. 获取用户的 TG 交易员
	tgTraders, err := tbm.db.GetTgTraders(telegramID)
	if err != nil || len(tgTraders) == 0 {
		tbm.sendMessage(chatID, "❌ 未找到交易员配置，请先创建交易员")
		return true
	}

	// 2. 查找运行中的交易员
	var runningTrader *config.TgTraderRecord
	for _, trader := range tgTraders {
		if trader.IsRunning {
			runningTrader = &trader
			break
		}
	}

	if runningTrader == nil {
		tbm.sendMessage(chatID, "❌ 交易员未运行，请先启动交易员")
		return true
	}

	// 3. 获取 AutoTrader 实例（优先使用 TG 管理器持有的实例）
	autoTrader, err := tbm.tgTraderMgr.GetTgTrader(runningTrader.ID)
	if err != nil || autoTrader == nil {
		tbm.sendMessage(chatID, fmt.Sprintf("❌ 获取交易员失败: %v", err))
		return true
	}

	// 4. 根据命令类型执行交易
	var result map[string]interface{}
	var tradeErr error

	switch cmd.Action {
	case "long":
		tbm.sendMessage(chatID, "🔄 正在执行开多订单...")
		result, tradeErr = autoTrader.ExecuteNaturalLanguageTrade("long", cmd.Symbol, cmd.Amount, cmd.Leverage)

	case "short":
		tbm.sendMessage(chatID, "🔄 正在执行开空订单...")
		result, tradeErr = autoTrader.ExecuteNaturalLanguageTrade("short", cmd.Symbol, cmd.Amount, cmd.Leverage)

	case "close":
		tbm.sendMessage(chatID, "🔄 正在执行平仓...")
		result, tradeErr = autoTrader.ExecuteNaturalLanguageTrade("close", cmd.Symbol, cmd.Amount, 0)

	case "close_all":
		tbm.sendMessage(chatID, "🔄 正在执行全部平仓...")
		result, tradeErr = autoTrader.ExecuteNaturalLanguageTrade("close_all", "", 0, 0)

	case "stop_loss":
		tbm.sendMessage(chatID, "🔄 正在设置止损...")
		// TODO: 实现止损设置
		result = nil
		tradeErr = fmt.Errorf("止损功能正在开发中")

	case "take_profit":
		tbm.sendMessage(chatID, "🔄 正在设置止盈...")
		// TODO: 实现止盈设置
		result = nil
		tradeErr = fmt.Errorf("止盈功能正在开发中")

	default:
		tbm.sendMessage(chatID, fmt.Sprintf("❌ 不支持的操作类型: %s", cmd.Action))
		return true
	}

	// 7. 处理交易结果
	if tradeErr != nil {
		tbm.sendMessage(chatID, fmt.Sprintf("❌ 交易执行失败: %v", tradeErr))
		log.Printf("❌ 交易失败 [用户:%d]: %v", telegramID, tradeErr)
	} else {
		tbm.sendMessage(chatID, "✅ 交易执行成功！")

		log.Printf("✅ 交易成功 [用户:%d]: %s %s", telegramID, cmd.Action, cmd.Symbol)
	}

	return true
}
