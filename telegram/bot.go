package telegram

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log"
	"math"
	"math/big"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"nofx/config"
	"nofx/manager"
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
	stockCatCache map[int64][]StockCategory
	ordersCache   map[int64][]OrderHistoryItem
	gasSponsorKey string
	gasSponsorWei *big.Int
	gasProcMu     sync.Mutex
	gasProcessing map[string]struct{}
	mediaTextMu   sync.Mutex
	mediaText     map[string]cachedMediaText
	forwardMu     sync.Mutex
	forwardSeen   map[string]time.Time
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

type cachedMediaText struct {
	Text string
	At   time.Time
}

// MessageStructure 消息结构信息
type MessageStructure struct {
	HasCodeBlocks bool
	HasJSON       bool
	HasReasoning  bool
	Sections      []Section
}

// Section 消息段落信息
type Section struct {
	Name  string
	Start int
	End   int
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
	cmdValidator.SetTraderManager(traderMgr) // 设置交易管理器引用

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
		stockCatCache: make(map[int64][]StockCategory),
		ordersCache:   make(map[int64][]OrderHistoryItem),
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
				tbm.rememberMediaGroupText(update.Message)
				text, from := tbm.messageAnyText(update.Message)
				meta := messagePayloadSummary(update.Message)
				if text == "" {
					log.Printf("收到消息 [%s] <empty> (%s)", update.Message.From.UserName, meta)
				} else {
					log.Printf("收到消息 [%s] (%s) %s (%s)", update.Message.From.UserName, from, text, meta)
				}
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
	case "chart":
		tbm.handleChart(update)
	case "stocks":
		tbm.handleStocks(update)
	case "orders":
		tbm.handleOrders(update)
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

	// 查询余额（并在此处显式触发现货->合约划转）
	balanceMsg, err := tbm.hlService.GetBalanceWithAutoTransfer(agentKey, walletAddr, tbm.testnet)
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

	// 获取股票资产信息（分类）
	categories, err := tbm.hlService.GetStockCategories(agentKey, walletAddr, tbm.testnet)
	if err != nil {
		log.Printf("获取股票资产失败: %v", err)
		tbm.sendMessage(chatID, "❌ 获取股票资产失败，请稍后重试")
		return
	}

	// 构造分类按钮
	if len(categories) == 0 {
		tbm.sendMessage(chatID, "📊 HIP-3 股票资产\n暂无可用股票资产。")
		return
	}

	// 缓存分类列表，便于点击按钮后返回资产列表
	tbm.stockCatCache[telegramID] = categories

	var rows [][]tgbotapi.InlineKeyboardButton
	for _, cat := range categories {
		label := fmt.Sprintf("%s（%d）", cat.Name, len(cat.List))
		data := fmt.Sprintf("stocks_cat|%d|%s", telegramID, cat.Name)
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(label, data),
		))
	}
	keyboard := tgbotapi.NewInlineKeyboardMarkup(rows...)
	msg := tgbotapi.NewMessage(chatID, "📊 选择市场查看股票列表")
	msg.ReplyMarkup = keyboard
	tbm.bot.Send(msg)
}

// handleOrders 处理 /orders 命令
func (tbm *TelegramBotManager) handleOrders(update tgbotapi.Update) {
	chatID := update.Message.Chat.ID
	telegramID := update.Message.From.ID

	// 确认用户
	if _, err := tbm.db.GetTGUserByTelegramID(telegramID); err != nil {
		tbm.sendMessage(chatID, "❌ 请先使用 /start 初始化账号")
		return
	}
	if !tbm.hasHyperliquidAccount(telegramID) {
		tbm.sendMessage(chatID, "❌ 请先使用 /start 完成账号初始化")
		return
	}

	// 需要交易信息才能估算可用笔数
	agentKey, walletAddr, err := tbm.extractAgentKeyAndWallet(telegramID)
	if err != nil {
		tbm.sendMessage(chatID, "❌ 获取账户信息失败，请稍后重试")
		return
	}

	// 预设查询配置
	type orderOption struct {
		labelHours int
		lookback   time.Duration
		limit      int
		count      int
	}
	options := []orderOption{
		{labelHours: 24, lookback: 24 * time.Hour, limit: 20},
		{labelHours: 168, lookback: 168 * time.Hour, limit: 100},
	}

	for i := range options {
		cnt, err := tbm.hlService.CountFills(agentKey, walletAddr, tbm.testnet, options[i].lookback)
		if err != nil {
			log.Printf("统计成交数失败 (uid=%d, hours=%d): %v", telegramID, options[i].labelHours, err)
			options[i].count = -1 // 标记失败，使用默认文案
			continue
		}
		options[i].count = cnt
	}

	buildLabel := func(opt orderOption) string {
		if opt.count < 0 {
			return fmt.Sprintf("最近%d条 (近%d天)", opt.limit, opt.labelHours/24)
		}
		display := opt.count
		if display > opt.limit {
			display = opt.limit
		}
		if opt.labelHours%24 == 0 {
			return fmt.Sprintf("最近%d条 (近%d天)", display, opt.labelHours/24)
		}
		return fmt.Sprintf("最近%d条 (近%d小时)", display, opt.labelHours)
	}

	msg := "📜 请选择历史成交查询范围"
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(buildLabel(options[0]), fmt.Sprintf("orders_recent|%d|%d|%d", telegramID, options[0].limit, options[0].labelHours)),
			tgbotapi.NewInlineKeyboardButtonData(buildLabel(options[1]), fmt.Sprintf("orders_recent|%d|%d|%d", telegramID, options[1].limit, options[1].labelHours)),
		),
	)
	tbm.sendMessageWithInlineKeyboard(chatID, msg, keyboard)
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

	// 只保留盈利的交易员（收益率 > 0）
	profitable := make([]map[string]interface{}, 0, len(traders))
	for _, tr := range traders {
		pnlPct, _ := tr["total_pnl_pct"].(float64)
		if pnlPct > 0 {
			profitable = append(profitable, tr)
		}
	}

	if len(profitable) == 0 {
		tbm.sendMessage(chatID, "⚠️ 暂无交易员数据。")
		return
	}

	limit := 10
	if len(profitable) < limit {
		limit = len(profitable)
	}

	var sb strings.Builder
	sb.WriteString("🏆 盈利排行榜（按收益率排名）\n\n")

	for i := 0; i < limit; i++ {
		tr := profitable[i]
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
	depositMsg := fmt.Sprintf(`🏦 Hyperliquid 充值地址（Arbitrum 网络）

<code>%s</code>（点击复制）

📋 充值说明:
• 网络: Arbitrum One
• 最小充值: 20 USDC
• 到账时间: 通常 2-5 分钟

⚠️ 注意事项:
• 仅支持 Arbitrum 网络转账
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
			tgbotapi.NewInlineKeyboardButtonData("📝 自定义 Prompt", fmt.Sprintf("menu_custom_prompt|%d", telegramID)),
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
	message, _ := tbm.messageAnyText(update.Message)

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

	// 处理自定义 Prompt 编辑
	if session.State == StateEditingCustomPrompt {
		tbm.handleCustomPromptInput(update, session)
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

	// 转发消息专用处理：仅解析文本转发，忽略其他类型
	if tbm.handleForwardedSentiment(update) {
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

// handleForwardedSentiment 处理转发文本的多空倾向解析
func (tbm *TelegramBotManager) handleForwardedSentiment(update tgbotapi.Update) bool {
	if update.Message == nil {
		return false
	}

	msg := update.Message
	chatID := msg.Chat.ID
	telegramID := msg.From.ID

	isForward := msg.ForwardDate != 0 || msg.ForwardFrom != nil || msg.ForwardFromChat != nil || msg.ForwardSenderName != ""
	if !isForward {
		return false
	}

	tbm.rememberMediaGroupText(msg)
	content, _ := tbm.messageAnyText(msg)

	if content == "" {
		meta := messagePayloadSummary(msg)
		if msg.MediaGroupID != "" {
			log.Printf("⚠️ [转发解析] 用户:%d 内容为空，跳过相册单条消息 (%s)", telegramID, meta)
			return true
		}
		tbm.sendMessage(chatID, "❌ 这条转发消息没有可解析的文本。\n\n提示：\n- 机器人只会读取消息的文字/Caption；图片本身会被忽略。\n- 如果你是“转发时加了评论”，请把那段评论文字单独发一条（或转发带Caption的那张图）。")
		log.Printf("⚠️ [转发解析] 用户:%d 内容为空 (%s)", telegramID, meta)
		return true
	}

	// 去重：相册/多媒体转发会拆成多条消息，避免对同一组重复解析与推送。
	dedupeKey := tbm.forwardDedupeKey(msg)
	if tbm.forwardAlreadySeen(dedupeKey, 2*time.Minute) {
		log.Printf("ℹ️ [转发解析] 用户:%d 去重跳过 (%s)", telegramID, dedupeKey)
		return true
	}
	tbm.forwardMarkSeen(dedupeKey)

	// 获取运行中的交易员与 MCP 客户端
	tgTraders, err := tbm.db.GetTgTraders(telegramID)
	if err != nil || len(tgTraders) == 0 {
		tbm.sendMessage(chatID, "❌ 未找到交易员配置，请先创建交易员")
		return true
	}
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

	autoTrader, err := tbm.tgTraderMgr.GetTgTrader(runningTrader.ID)
	if err != nil || autoTrader == nil {
		tbm.sendMessage(chatID, fmt.Sprintf("❌ 获取交易员失败: %v", err))
		return true
	}
	client := autoTrader.GetMCPClient()
	if client == nil {
		tbm.sendMessage(chatID, "❌ AI 解析服务未配置，请先完成模型配置")
		return true
	}

	// 截断超长文本，避免 prompt 过大
	const maxLen = 3000
	truncated := false
	if utf8.RuneCountInString(content) > maxLen {
		runes := []rune(content)
		head := string(runes[:1500])
		tail := string(runes[len(runes)-1500:])
		content = head + "\n...【内容已截断】...\n" + tail
		truncated = true
	}

	// 来源描述
	var source string
	switch {
	case msg.ForwardFromChat != nil && msg.ForwardFromChat.Title != "":
		source = msg.ForwardFromChat.Title
	case msg.ForwardFrom != nil && msg.ForwardFrom.UserName != "":
		source = msg.ForwardFrom.UserName
	case msg.ForwardSenderName != "":
		source = msg.ForwardSenderName
	default:
		source = "未知来源"
	}
	fwdTime := time.Unix(int64(msg.ForwardDate), 0).Format("2006-01-02 15:04:05")

	systemPrompt := `你是一个加密市场多空倾向判定器。只基于给定文本判断“做多/做空/等待”，不要输出交易对、金额或杠杆。输出严格 JSON 对象：
{
  "action": "open_long|open_short|wait",
  "long_confidence": 0-100,
  "short_confidence": 0-100,
  "reasoning": "不超过200字的简洁依据"
}
规则：
- 若任一信心值 <60，或长短差值 <10，则 action=wait，并说明原因
- 若文本无明确方向，action=wait
- 禁止任何代码块/Markdown/额外文本`

	userPrompt := fmt.Sprintf("来源: %s\n转发时间: %s\n是否截断: %t\n请判断多空倾向并按要求输出JSON。\n\n转发内容:\n%s", source, fwdTime, truncated, content)

	log.Printf("🤖 [转发解析] 用户:%d 来源:%s 截断:%t 长度:%d", telegramID, source, truncated, len(content))

	aiResp, err := client.CallWithMessages(systemPrompt, userPrompt)
	if err != nil {
		log.Printf("❌ 转发消息AI解析失败: %v", err)
		tbm.sendMessage(chatID, "⚠️ AI 解析服务暂时不可用，请稍后重试。")
		return true
	}

	type sentimentDecision struct {
		Action          string  `json:"action"`
		LongConfidence  float64 `json:"long_confidence"`
		ShortConfidence float64 `json:"short_confidence"`
		Reasoning       string  `json:"reasoning"`
	}

	cleanResp := stripCodeFences(strings.TrimSpace(aiResp))
	var decision sentimentDecision
	if err := json.Unmarshal([]byte(cleanResp), &decision); err != nil {
		log.Printf("❌ 解析 AI JSON 失败: %v | 原始响应: %s", err, aiResp)
		tbm.sendMessage(chatID, "⚠️ AI 返回格式异常，无法解析。")
		return true
	}

	// 本地安全校验与降级
	validAction := map[string]bool{"open_long": true, "open_short": true, "wait": true}
	if !validAction[decision.Action] {
		decision.Action = "wait"
	}
	diff := math.Abs(decision.LongConfidence - decision.ShortConfidence)
	if decision.LongConfidence < 60 && decision.ShortConfidence < 60 {
		decision.Action = "wait"
	}
	if diff < 10 {
		decision.Action = "wait"
	}
	if decision.Action == "open_long" && decision.LongConfidence < decision.ShortConfidence {
		decision.Action = "wait"
	}
	if decision.Action == "open_short" && decision.ShortConfidence < decision.LongConfidence {
		decision.Action = "wait"
	}

	pretty, _ := json.MarshalIndent(decision, "", "  ")
	reply := fmt.Sprintf("📥 已解析转发文本（来源: %s）\n🤖 AI决策 JSON:\n```json\n%s\n```", source, string(pretty))

	if decision.Action == "open_long" || decision.Action == "open_short" {
		sideText := map[string]string{"open_long": "做多", "open_short": "做空"}[decision.Action]
		btn1 := tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("%s BTC", sideText), fmt.Sprintf("fwd_pick|%d|%s|BTCUSDT", telegramID, map[string]string{"open_long": "long", "open_short": "short"}[decision.Action]))
		btn2 := tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("%s ETH", sideText), fmt.Sprintf("fwd_pick|%d|%s|ETHUSDT", telegramID, map[string]string{"open_long": "long", "open_short": "short"}[decision.Action]))
		keyboard := tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(btn1, btn2))
		msg := tgbotapi.NewMessage(chatID, reply)
		msg.ReplyMarkup = keyboard
		tbm.bot.Send(msg)
	} else {
		tbm.sendMessage(chatID, reply)
	}
	return true
}

// stripCodeFences 去除 ``` 包裹的代码块
func stripCodeFences(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```json")
		s = strings.TrimPrefix(s, "```")
		s = strings.TrimSuffix(s, "```")
		s = strings.TrimSpace(s)
	}
	return s
}

func (tbm *TelegramBotManager) rememberMediaGroupText(msg *tgbotapi.Message) {
	if msg == nil || msg.MediaGroupID == "" {
		return
	}
	text, _ := messageTextOrCaption(msg)
	if text == "" {
		return
	}
	key := mediaGroupKey(msg.Chat.ID, msg.MediaGroupID)
	now := time.Now()
	tbm.mediaTextMu.Lock()
	if tbm.mediaText == nil {
		tbm.mediaText = make(map[string]cachedMediaText, 64)
	}
	tbm.mediaText[key] = cachedMediaText{Text: text, At: now}
	tbm.mediaTextMu.Unlock()
}

func (tbm *TelegramBotManager) messageAnyText(msg *tgbotapi.Message) (text string, from string) {
	if msg == nil {
		return "", ""
	}
	if t, src := messageTextOrCaption(msg); t != "" {
		return t, src
	}
	if msg.ReplyToMessage != nil {
		if t, src := messageTextOrCaption(msg.ReplyToMessage); t != "" {
			return t, "reply_" + src
		}
	}
	if msg.MediaGroupID == "" {
		return "", ""
	}
	key := mediaGroupKey(msg.Chat.ID, msg.MediaGroupID)
	tbm.mediaTextMu.Lock()
	defer tbm.mediaTextMu.Unlock()
	if tbm.mediaText == nil {
		return "", ""
	}
	cached, ok := tbm.mediaText[key]
	if !ok {
		return "", ""
	}
	if time.Since(cached.At) > 2*time.Minute {
		delete(tbm.mediaText, key)
		return "", ""
	}
	return cached.Text, "album_cache"
}

func messageTextOrCaption(msg *tgbotapi.Message) (text string, from string) {
	if msg == nil {
		return "", ""
	}
	if t := strings.TrimSpace(msg.Text); t != "" {
		return t, "text"
	}
	if c := strings.TrimSpace(msg.Caption); c != "" {
		return c, "caption"
	}
	return "", ""
}

func mediaGroupKey(chatID int64, mediaGroupID string) string {
	return fmt.Sprintf("%d:%s", chatID, mediaGroupID)
}

func (tbm *TelegramBotManager) forwardDedupeKey(msg *tgbotapi.Message) string {
	if msg == nil {
		return "nil"
	}
	chatID := msg.Chat.ID
	if msg.MediaGroupID != "" {
		return "album:" + mediaGroupKey(chatID, msg.MediaGroupID)
	}
	// 单条消息用 MessageID 去重即可（同一 chat 内唯一）
	return fmt.Sprintf("msg:%d:%d", chatID, msg.MessageID)
}

func (tbm *TelegramBotManager) forwardAlreadySeen(key string, ttl time.Duration) bool {
	if key == "" {
		return false
	}
	now := time.Now()
	tbm.forwardMu.Lock()
	defer tbm.forwardMu.Unlock()
	if tbm.forwardSeen == nil {
		return false
	}
	at, ok := tbm.forwardSeen[key]
	if !ok {
		return false
	}
	if now.Sub(at) > ttl {
		delete(tbm.forwardSeen, key)
		return false
	}
	return true
}

func (tbm *TelegramBotManager) forwardMarkSeen(key string) {
	if key == "" {
		return
	}
	now := time.Now()
	tbm.forwardMu.Lock()
	if tbm.forwardSeen == nil {
		tbm.forwardSeen = make(map[string]time.Time, 64)
	}
	// 简单清理，避免无限增长
	if len(tbm.forwardSeen) > 512 {
		for k, at := range tbm.forwardSeen {
			if now.Sub(at) > 5*time.Minute {
				delete(tbm.forwardSeen, k)
			}
		}
	}
	tbm.forwardSeen[key] = now
	tbm.forwardMu.Unlock()
}

func messagePayloadSummary(msg *tgbotapi.Message) string {
	if msg == nil {
		return "unknown"
	}
	if strings.TrimSpace(msg.Text) != "" {
		return "text"
	}
	parts := make([]string, 0, 4)
	if strings.TrimSpace(msg.Caption) != "" {
		parts = append(parts, "caption")
	}
	if len(msg.Photo) > 0 {
		parts = append(parts, "photo")
	}
	if msg.Document != nil {
		parts = append(parts, "document")
	}
	if msg.Video != nil {
		parts = append(parts, "video")
	}
	if msg.MediaGroupID != "" {
		parts = append(parts, "album")
	}
	if len(parts) == 0 {
		return "unknown"
	}
	return strings.Join(parts, "+")
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
			Command:     "chart",
			Description: "📈 查看资产K线图",
		},
		{
			Command:     "orders",
			Description: "📜 历史订单",
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
	// 这里不要使用 selective=true：在非 reply / 未 @mention 的情况下，部分客户端不会移除键盘。
	removeKeyboard := tgbotapi.NewRemoveKeyboard(false)
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
		return errors.New(msg)
	}

	if availableBalance <= 0 {
		if spotBalance > 0 {
			msg := fmt.Sprintf("⚠️ 账户 Spot 余额 %.2f USDC，但合约账户可用余额为 0。\n\n"+
				"请在 Hyperliquid 中将现货资金转入 Perpetuals 账户后再启动交易员。", spotBalance)
			return errors.New(msg)
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

// PushDecisionToUser 推送AI决策到指定用户（用户友好的分段指示）
func (tbm *TelegramBotManager) PushDecisionToUser(telegramID int64, decisionMsg string) error {
	if tbm.bot == nil {
		return fmt.Errorf("Telegram Bot未初始化")
	}

	// 1. 预处理消息 - 确保UTF-8有效性和编码处理
	processedMsg := tbm.preprocessMessage(decisionMsg)

	// 2. 简单按长度分段（保持内容原样）
	rawChunks := splitRawMessage(processedMsg, telegramMessageChunkSize)
	totalChunks := len(rawChunks)

	// 3. 发送每段并添加导航信息
	successCount := 0
	failedChunks := make([]int, 0)

	// 使用 sendMessageDraft 流式更新同一草稿（仅在私聊可见，失败则忽略）
	draftID := rand.Int31n(1<<30) + 1 // 非零
	for i, chunk := range rawChunks {
		chunkNum := i + 1
		enhancedChunk := tbm.enhanceChunkWithNavigation(decisionChunk{Text: chunk, IsJSON: strings.Contains(chunk, "```json")}, chunkNum, totalChunks)
		_ = tbm.sendMessageDraft(telegramID, int(draftID), enhancedChunk) // 流式展示；失败忽略
	}

	for i, chunk := range rawChunks {
		chunkNum := i + 1
		enhancedChunk := tbm.enhanceChunkWithNavigation(decisionChunk{Text: chunk, IsJSON: strings.Contains(chunk, "```json")}, chunkNum, totalChunks)

		log.Printf("📤 准备发送消息到 ChatID %d: 📄 AI决策报告 [%d/%d]", telegramID, chunkNum, totalChunks)

		// 段间延迟优化（减少到500ms，提高响应速度）
		if i > 0 {
			time.Sleep(300 * time.Millisecond)
		}

		// 发送消息
		if err := tbm.sendMessageWithError(telegramID, enhancedChunk); err != nil {
			log.Printf("❌ 第 %d 段发送失败: %v", chunkNum, err)
			failedChunks = append(failedChunks, chunkNum)

			// 如果发送失败，尝试简化格式重试
			if retryErr := tbm.sendSimplifiedChunk(telegramID, decisionChunk{Text: chunk, IsJSON: strings.Contains(chunk, "```json")}, chunkNum, totalChunks); retryErr != nil {
				log.Printf("❌ 第 %d 段简化重试也失败: %v", chunkNum, retryErr)
				continue
			} else {
				log.Printf("✅ 第 %d 段简化重试成功", chunkNum)
				successCount++
			}
		} else {
			successCount++
			log.Printf("✅ 第 %d/%d 段发送成功", chunkNum, totalChunks)

			// 如果不是最后一段，添加进度提示
			if chunkNum < totalChunks {
				time.Sleep(100 * time.Millisecond) // 短暂延迟确保用户体验
			}
		}
	}

	// 4. 总结日志
	if len(failedChunks) > 0 {
		log.Printf("⚠️ 部分段落发送失败 - 成功: %d/%d, 失败段号: %v",
			successCount, totalChunks, failedChunks)
		return fmt.Errorf("部分段落发送失败，已成功发送 %d/%d 段", successCount, totalChunks)
	}

	log.Printf("✅ 成功推送完整AI决策报告 (%d段, ChatID: %d)", totalChunks, telegramID)
	return nil
}

// PushRawMessageToUser 将AI原始响应直接推送到用户（无格式化/转义）
func (tbm *TelegramBotManager) PushRawMessageToUser(telegramID int64, rawMsg string) error {
	if tbm.bot == nil {
		return fmt.Errorf("Telegram Bot未初始化")
	}

	if rawMsg == "" {
		return fmt.Errorf("原始消息为空")
	}

	sections := splitRawSections(rawMsg)
	totalSent := 0

	for _, section := range sections {
		formatted := normalizeAIResponseText(section)
		if formatted == "" {
			formatted = section
		}

		// 保守一些，留足转义开销
		const telegramLimit = 3500
		// 预先转义为HTML，方便整体放入<pre>，确保可复制
		escaped := html.EscapeString(formatted)

		// 如果原始段落已经包含 ```json 代码块，尽量保持与决策块一致的整体发送
		preOverhead := len("<pre></pre>")
		chunks := splitRawMessage(escaped, telegramLimit-preOverhead)

		for i, chunk := range chunks {
			text := fmt.Sprintf("<pre>%s</pre>", chunk)
			msg := tgbotapi.NewMessage(telegramID, text)
			msg.ParseMode = "HTML" // 使用HTML预格式化，便于整体复制

			if _, err := tbm.bot.Send(msg); err != nil {
				log.Printf("❌ 发送原始消息失败 (段 %d/%d): %v", i+1, len(chunks), err)
				return err
			}

			totalSent++

			if len(chunks) > 1 && i < len(chunks)-1 {
				time.Sleep(200 * time.Millisecond)
			}
		}
	}

	log.Printf("✅ 成功推送原始AI响应 (%d段, ChatID: %d)", totalSent, telegramID)
	return nil
}

// normalizeAIResponseText 轻量格式化AI响应：统一换行、去除多余空行、裁剪首尾空白
func normalizeAIResponseText(text string) string {
	if text == "" {
		return ""
	}

	// 统一换行
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")

	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))

	blankCount := 0
	for _, line := range lines {
		line = strings.TrimRight(line, " \t")
		if strings.TrimSpace(line) == "" {
			blankCount++
			if blankCount > 2 {
				continue // 最多保留两个连续空行
			}
			out = append(out, "")
			continue
		}
		blankCount = 0
		out = append(out, line)
	}

	// 去除首尾空行
	start, end := 0, len(out)
	for start < end && strings.TrimSpace(out[start]) == "" {
		start++
	}
	for end > start && strings.TrimSpace(out[end-1]) == "" {
		end--
	}

	return strings.Join(out[start:end], "\n")
}

// preprocessMessage 预处理消息确保有效性
func (tbm *TelegramBotManager) preprocessMessage(message string) string {
	// UTF-8有效性检查
	log.Printf("🔍 [DEBUG] 检查消息UTF-8编码 (长度: %d)", len(message))
	if !utf8.ValidString(message) {
		log.Printf("❌ 消息包含无效UTF-8字符，进行清理")
		cleanMsg := ensureUTF8Validity(message)
		log.Printf("✅ 已清理无效UTF-8字符")
		return cleanMsg
	} else {
		log.Printf("✅ 消息UTF-8编码有效")
		return message
	}
}

// enhanceChunkWithEnhancement 为分段添加导航信息
func (tbm *TelegramBotManager) enhanceChunkWithNavigation(chunk decisionChunk, chunkNum, totalChunks int) string {
	content := chunk.Text

	// 如果有多段，添加段头和导航信息
	if totalChunks > 1 {
		var header strings.Builder

		// 段头信息
		header.WriteString(fmt.Sprintf("📄 AI决策报告 [%d/%d]\n\n", chunkNum, totalChunks))

		// 添加导航提示
		if chunkNum == 1 {
			header.WriteString(fmt.Sprintf("📋 完整报告共%d段，正在继续发送...\n\n", totalChunks))
		} else if chunkNum == totalChunks {
			header.WriteString("✅ 报告发送完成\n\n")
		} else {
			header.WriteString(fmt.Sprintf("📄 继续发送第%d段...\n\n", chunkNum+1))
		}

		// 确保内容不以换行开头
		content = strings.TrimLeft(content, "\n")

		return header.String() + content
	}

	return content
}

// sendSimplifiedChunk 发送简化格式的段落（重试机制）
func (tbm *TelegramBotManager) sendSimplifiedChunk(telegramID int64, chunk decisionChunk, chunkNum, totalChunks int) error {
	simplifiedContent := fmt.Sprintf("📄 AI决策报告 [%d/%d]\n\n%s", chunkNum, totalChunks, chunk.Text)

	msg := tgbotapi.NewMessage(telegramID, simplifiedContent)
	_, err := tbm.bot.Send(msg)
	return err
}

// sendMessageDraft 使用 sendMessageDraft 流式更新草稿（失败由调用方决定是否忽略）
func (tbm *TelegramBotManager) sendMessageDraft(chatID int64, draftID int, text string) error {
	if tbm.bot == nil {
		return fmt.Errorf("Telegram Bot未初始化")
	}
	if draftID == 0 {
		draftID = 1
	}
	params := map[string]string{
		"chat_id":    strconv.FormatInt(chatID, 10),
		"draft_id":   strconv.Itoa(draftID),
		"text":       text,
		"parse_mode": "HTML",
	}
	_, err := tbm.bot.MakeRequest("sendMessageDraft", params)
	return err
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
	if len(parts) < 2 {
		log.Printf("❌ 无效的回调数据格式: %s", data)
		tbm.answerCallbackQuery(callback.ID, "无效的请求")
		return
	}

	action := parts[0]
	telegramID, err := strconv.ParseInt(parts[1], 10, 64)
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
	case "menu_custom_prompt":
		tbm.handleMenuCustomPrompt(callback, chatID, telegramID)
	case "edit_custom_prompt":
		tbm.handleEditCustomPrompt(callback, chatID, telegramID)
	case "clear_custom_prompt":
		tbm.handleClearCustomPrompt(callback, chatID, telegramID)
	case "fwd_pick":
		tbm.handleForwardPickCallback(callback, chatID, telegramID, parts)
	case "fwd_exec":
		tbm.handleForwardExecCallback(callback, chatID, telegramID, parts)
	case "stocks_cat":
		tbm.handleStocksCategoryCallback(callback, chatID, telegramID, parts)
	case "orders_recent":
		tbm.handleOrdersRecentCallback(callback, chatID, telegramID, parts)
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

// handleForwardTradeCallback 处理转发消息快捷多空按钮
// handleForwardPickCallback 首层方向+标的选择，弹出金额/比例菜单
func (tbm *TelegramBotManager) handleForwardPickCallback(callback *tgbotapi.CallbackQuery, chatID int64, telegramID int64, parts []string) {
	// fwd_pick|user|side|symbol
	if len(parts) != 4 {
		tbm.answerCallbackQuery(callback.ID, "请求格式错误")
		return
	}
	side := parts[2]
	symbol := parts[3]
	tbm.answerCallbackQuery(callback.ID, "请选择金额/比例")

	// 预设档位
	fixed := []float64{15, 30, 50}
	percent := []int{5, 10, 20}

	// 构造回调数据 fwd_exec|user|side|symbol|mode|value
	var rows [][]tgbotapi.InlineKeyboardButton
	amtRow := []tgbotapi.InlineKeyboardButton{}
	for _, amt := range fixed {
		amtRow = append(amtRow, tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("%.0fU", amt),
			fmt.Sprintf("fwd_exec|%d|%s|%s|amt|%.0f", telegramID, side, symbol, amt)))
	}
	if len(amtRow) > 0 {
		rows = append(rows, amtRow)
	}
	pctRow := []tgbotapi.InlineKeyboardButton{}
	for _, p := range percent {
		pctRow = append(pctRow, tgbotapi.NewInlineKeyboardButtonData(
			fmt.Sprintf("%d%%资金", p),
			fmt.Sprintf("fwd_exec|%d|%s|%s|pct|%d", telegramID, side, symbol, p),
		))
	}
	if len(pctRow) > 0 {
		rows = append(rows, pctRow)
	}
	keyboard := tgbotapi.NewInlineKeyboardMarkup(rows...)
	prompt := fmt.Sprintf("选择下单金额/比例（杠杆固定3x）\n标的: %s\n方向: %s", symbol, map[string]string{"long": "做多", "short": "做空"}[side])
	msg := tgbotapi.NewMessage(chatID, prompt)
	msg.ReplyMarkup = keyboard
	tbm.bot.Send(msg)
}

// handleForwardExecCallback 第二层金额/比例选择，执行下单
func (tbm *TelegramBotManager) handleForwardExecCallback(callback *tgbotapi.CallbackQuery, chatID int64, telegramID int64, parts []string) {
	// fwd_exec|user|side|symbol|mode|value
	if len(parts) != 6 {
		tbm.answerCallbackQuery(callback.ID, "请求格式错误")
		return
	}
	side := parts[2]
	symbol := parts[3]
	mode := parts[4]
	value := parts[5]
	tbm.answerCallbackQuery(callback.ID, "⏳ 正在执行...")

	// 获取运行中的交易员
	tgTraders, err := tbm.db.GetTgTraders(telegramID)
	if err != nil || len(tgTraders) == 0 {
		tbm.sendMessage(chatID, "❌ 未找到交易员配置，请先创建交易员")
		return
	}
	var runningTrader *config.TgTraderRecord
	for _, trader := range tgTraders {
		if trader.IsRunning {
			runningTrader = &trader
			break
		}
	}
	if runningTrader == nil {
		tbm.sendMessage(chatID, "❌ 交易员未运行，请先启动交易员")
		return
	}

	autoTrader, err := tbm.tgTraderMgr.GetTgTrader(runningTrader.ID)
	if err != nil || autoTrader == nil {
		tbm.sendMessage(chatID, fmt.Sprintf("❌ 获取交易员失败: %v", err))
		return
	}

	// 获取可用余额用于百分比换算
	agentKey, walletAddr, err := tbm.extractAgentKeyAndWallet(telegramID)
	if err != nil {
		tbm.sendMessage(chatID, fmt.Sprintf("❌ 获取账户信息失败: %v", err))
		return
	}
	balance, err := tbm.hlService.FetchBalance(agentKey, walletAddr, tbm.testnet)
	if err != nil {
		tbm.sendMessage(chatID, fmt.Sprintf("❌ 获取余额失败: %v", err))
		return
	}
	avail, _ := balance["availableBalance"].(float64)
	if avail <= 0 {
		tbm.sendMessage(chatID, "❌ 可用余额不足，无法下单")
		return
	}

	const leverage = 3
	const minNominal = 15.0
	maxNominal := avail * 0.9
	if maxNominal < minNominal {
		tbm.sendMessage(chatID, fmt.Sprintf("❌ 可用余额不足，最小名义下单需 %.0f U，可用 %.2f U", minNominal, avail))
		return
	}

	var amount float64
	switch mode {
	case "amt":
		amt, _ := strconv.ParseFloat(value, 64)
		amount = amt
	case "pct":
		pct, _ := strconv.Atoi(value)
		amount = avail * float64(pct) / 100.0
	default:
		tbm.sendMessage(chatID, "❌ 请求格式错误")
		return
	}

	adjustNote := ""
	if amount < minNominal {
		amount = minNominal
		adjustNote = fmt.Sprintf("（已按最小单 %.0f U 执行）", minNominal)
	}
	if amount > maxNominal {
		amount = maxNominal
		adjustNote = fmt.Sprintf("（已按可用余额上限 %.2f U 执行）", maxNominal)
	}

	tbm.sendMessage(chatID, fmt.Sprintf("🔄 提交下单: %s %s | 名义约 %.2f U | 杠杆 %dx%s", map[string]string{"long": "做多", "short": "做空"}[side], symbol, amount, leverage, adjustNote))
	_, tradeErr := autoTrader.ExecuteNaturalLanguageTrade(side, symbol, amount, 0, leverage, "crypto")
	if tradeErr != nil {
		tbm.sendMessage(chatID, fmt.Sprintf("❌ 下单失败: %v", tradeErr))
		return
	}
	tbm.sendMessage(chatID, fmt.Sprintf("✅ 已提交: %s %s | 名义约 %.2f U | 杠杆 %dx", map[string]string{"long": "做多", "short": "做空"}[side], symbol, amount, leverage))
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

// handleMenuCustomPrompt 处理自定义 Prompt 菜单
func (tbm *TelegramBotManager) handleMenuCustomPrompt(callback *tgbotapi.CallbackQuery, chatID int64, telegramID int64) {
	tbm.answerCallbackQuery(callback.ID, "📝 打开自定义 Prompt 设置...")

	// 获取当前交易员配置
	trader, err := tbm.getPrimaryTrader(telegramID)
	if err != nil {
		tbm.sendMessage(chatID, "❌ 未找到交易员，请先使用 /start 初始化账号")
		return
	}

	if !trader.IsConfigured {
		tbm.sendMessage(chatID, "❌ 交易员尚未配置，请先使用 /create_trader 完成设置")
		return
	}

	// 显示当前自定义 Prompt
	var message string
	if trader.CustomPrompt != "" {
		// 截断显示，避免消息过长（编辑时会展示完整内容）
		displayPrompt := trader.CustomPrompt
		if len(displayPrompt) > 500 {
			displayPrompt = displayPrompt[:500] + "...(已截断)"
		}
		message = fmt.Sprintf(`📝 <b>自定义 Prompt 设置</b>

<b>当前自定义 Prompt:</b>
<code>%s</code>

💡 <b>使用说明:</b>
• 自定义 Prompt 会附加到基础策略 Prompt 之后
• 可用于添加个人交易偏好、风险控制规则等
• 修改后立即生效，无需重启交易员

点击下方按钮开始操作：`, esc(displayPrompt))
	} else {
		message = `📝 <b>自定义 Prompt 设置</b>

<b>当前状态:</b> 未设置自定义 Prompt

💡 <b>使用说明:</b>
• 自定义 Prompt 会附加到基础策略 Prompt 之后
• 可用于添加个人交易偏好、风险控制规则等
• 例如: "优先做多，避免追涨杀跌，单笔仓位不超过总资金的20%"

请直接发送您的自定义 Prompt 内容：`
	}

	// 创建按钮
	if trader.CustomPrompt != "" {
		keyboard := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("✏️ 编辑 Prompt", fmt.Sprintf("edit_custom_prompt|%d", telegramID)),
				tgbotapi.NewInlineKeyboardButtonData("🗑️ 清除 Prompt", fmt.Sprintf("clear_custom_prompt|%d", telegramID)),
			),
		)
		tbm.sendMessageWithInlineKeyboard(chatID, message, keyboard)
	} else {
		// 无 Prompt 时，直接进入编辑状态，并提供“取消”回复键盘按钮
		sessionMgr := tbm.tgTraderMgr.GetSessionManager()
		sessionMgr.ClearSession(telegramID)
		session := sessionMgr.GetOrCreateSession(telegramID)
		session.State = StateEditingCustomPrompt
		sessionMgr.UpdateSessionState(telegramID, StateEditingCustomPrompt)

		keyboard := tgbotapi.NewReplyKeyboard(
			tgbotapi.NewKeyboardButtonRow(
				tgbotapi.NewKeyboardButton("取消"),
			),
		)
		keyboard.ResizeKeyboard = true
		keyboard.OneTimeKeyboard = true
		keyboard.InputFieldPlaceholder = "输入 Prompt，或点取消"

		if msg, err := tbm.sendMessageWithMarkupAndReturn(chatID, message, keyboard); err == nil && msg != nil {
			sessionMgr.UpdateLastMessageID(telegramID, msg.MessageID)
		}
	}
}

func (tbm *TelegramBotManager) handleEditCustomPrompt(callback *tgbotapi.CallbackQuery, chatID int64, telegramID int64) {
	tbm.answerCallbackQuery(callback.ID, "✏️ 正在打开编辑...")

	trader, err := tbm.getPrimaryTrader(telegramID)
	if err != nil {
		tbm.sendMessage(chatID, "❌ 未找到交易员")
		return
	}

	if !trader.IsConfigured {
		tbm.sendMessage(chatID, "❌ 交易员尚未配置，请先使用 /create_trader 完成设置")
		return
	}

	if trader.CustomPrompt == "" {
		tbm.sendMessage(chatID, "⚠️ 当前未设置自定义 Prompt，请直接发送新的 Prompt 内容")
	} else {
		msg := fmt.Sprintf(`✏️ <b>编辑 Prompt</b>

请发送修改后的 Prompt（可点击下方提示词复制后修改）：

<code>%s</code>`, esc(trader.CustomPrompt))

		keyboard := tgbotapi.NewReplyKeyboard(
			tgbotapi.NewKeyboardButtonRow(
				tgbotapi.NewKeyboardButton("取消"),
			),
		)
		keyboard.ResizeKeyboard = true
		keyboard.OneTimeKeyboard = true
		keyboard.InputFieldPlaceholder = "粘贴/修改 Prompt 后发送"

		if sent, err := tbm.sendMessageWithMarkupAndReturn(chatID, msg, keyboard); err == nil && sent != nil {
			tbm.tgTraderMgr.GetSessionManager().UpdateLastMessageID(telegramID, sent.MessageID)
		}
	}

	// 设置会话状态为编辑自定义 Prompt
	sessionMgr := tbm.tgTraderMgr.GetSessionManager()
	sessionMgr.ClearSession(telegramID)
	session := sessionMgr.GetOrCreateSession(telegramID)
	session.State = StateEditingCustomPrompt
	sessionMgr.UpdateSessionState(telegramID, StateEditingCustomPrompt)
}

// handleStocksCategoryCallback 处理股票市场分类回调
func (tbm *TelegramBotManager) handleStocksCategoryCallback(callback *tgbotapi.CallbackQuery, chatID int64, telegramID int64, parts []string) {
	// stocks_cat|user|CAT
	if len(parts) != 3 {
		tbm.answerCallbackQuery(callback.ID, "请求格式错误")
		return
	}
	catName := parts[2]

	// 获取分类列表缓存
	categories := tbm.stockCatCache[telegramID]
	if len(categories) == 0 {
		tbm.answerCallbackQuery(callback.ID, "缓存已失效，请重新 /stocks")
		return
	}

	var target *StockCategory
	for i := range categories {
		if categories[i].Name == catName {
			target = &categories[i]
			break
		}
	}
	if target == nil {
		tbm.answerCallbackQuery(callback.ID, "未找到该市场")
		return
	}

	// 构造资产列表
	var b strings.Builder
	b.WriteString(fmt.Sprintf("📈 %s 市场资产（%d）\n", catName, len(target.List)))

	for _, asset := range target.List {
		parts := strings.SplitN(asset.Name, ":", 2)
		symbol := asset.Name
		prefix := ""
		if len(parts) == 2 {
			prefix = parts[0]
			symbol = parts[1]
		}
		b.WriteString(fmt.Sprintf("• %s:%s | 杠杆上限: %dx\n", prefix, symbol, asset.MaxLeverage))
	}

	msg := tgbotapi.NewMessage(chatID, b.String())
	tbm.bot.Send(msg)
	tbm.answerCallbackQuery(callback.ID, "✅ 已加载")
}

// handleOrdersRecentCallback 处理历史成交范围选择
func (tbm *TelegramBotManager) handleOrdersRecentCallback(callback *tgbotapi.CallbackQuery, chatID int64, telegramID int64, parts []string) {
	// orders_recent|user|limit|hours
	if len(parts) < 3 || len(parts) > 4 {
		tbm.answerCallbackQuery(callback.ID, "请求格式错误")
		return
	}
	limit, err := strconv.Atoi(parts[2])
	if err != nil || limit <= 0 {
		tbm.answerCallbackQuery(callback.ID, "参数错误")
		return
	}
	lookback := 7 * 24 * time.Hour
	if len(parts) == 4 {
		hours, convErr := strconv.Atoi(parts[3])
		if convErr != nil || hours <= 0 {
			tbm.answerCallbackQuery(callback.ID, "时间窗口错误")
			return
		}
		lookback = time.Duration(hours) * time.Hour
	}

	// 提取账号
	agentKey, walletAddr, err := tbm.extractAgentKeyAndWallet(telegramID)
	if err != nil {
		tbm.answerCallbackQuery(callback.ID, "账号信息获取失败")
		return
	}

	// 推断用户时区（优先使用 DB 存储的 language_code，再尝试 Telegram 回调里的语言，最终回退 UTC）
	lang := ""
	if userAny, err := tbm.db.GetTGUserByTelegramID(telegramID); err == nil {
		if userMap, ok := userAny.(map[string]interface{}); ok {
			if lc, ok := userMap["language_code"].(string); ok {
				lang = lc
			}
		}
	}
	// 如果数据库没有记录，再尝试使用回调里的语言码
	if lang == "" && callback != nil && callback.From != nil {
		lang = callback.From.LanguageCode
	}
	loc := inferLocationFromLanguage(lang)

	tbm.answerCallbackQuery(callback.ID, "⏳ 正在查询...")

	go func() {
		text, err := tbm.hlService.GetOrderHistory(agentKey, walletAddr, tbm.testnet, lookback, limit, loc)
		if err != nil {
			tbm.sendMessage(chatID, fmt.Sprintf("❌ 查询失败: %s", esc(err)))
			return
		}
		tbm.sendMessage(chatID, text)
	}()
}

// handleClearCustomPrompt 处理清除自定义 Prompt
func (tbm *TelegramBotManager) handleClearCustomPrompt(callback *tgbotapi.CallbackQuery, chatID int64, telegramID int64) {
	tbm.answerCallbackQuery(callback.ID, "🗑️ 正在清除...")

	// 清除会话状态
	sessionMgr := tbm.tgTraderMgr.GetSessionManager()
	sessionMgr.ClearSession(telegramID)

	trader, err := tbm.getPrimaryTrader(telegramID)
	if err != nil {
		tbm.sendMessage(chatID, "❌ 未找到交易员")
		return
	}

	// 更新数据库
	if err := tbm.db.UpdateTgTraderCustomPrompt(telegramID, trader.ID, ""); err != nil {
		tbm.sendMessage(chatID, fmt.Sprintf("❌ 清除失败: %s", esc(err)))
		return
	}

	// 同步更新运行中的交易员
	if trader.IsRunning {
		if traderObj, err := tbm.tgTraderMgr.GetTgTrader(trader.ID); err == nil {
			traderObj.SetCustomPrompt("")
			log.Printf("🔁 已同步清除运行中交易员 %s 的自定义 Prompt", trader.Name)
		}
	}

	tbm.sendMessage(chatID, "✅ 已清除自定义 Prompt")
}

// handleCustomPromptInput 处理自定义 Prompt 输入
func (tbm *TelegramBotManager) handleCustomPromptInput(update tgbotapi.Update, session *UserSession) {
	chatID := update.Message.Chat.ID
	telegramID := update.Message.From.ID
	input := strings.TrimSpace(update.Message.Text)

	sessionMgr := tbm.tgTraderMgr.GetSessionManager()

	// 检查取消命令
	if strings.EqualFold(input, "cancel") || input == "取消" {
		// 记录需要清理的“编辑提示消息”，避免多端不同步（删除 removeKeyboard 消息后键盘又回到上一条带键盘的消息）
		promptMsgID := 0
		if session != nil {
			promptMsgID = session.LastMessageID
		}

		sessionMgr.ClearSession(telegramID)

		// 尽量静默取消：移除键盘并清理提示消息/用户取消消息，减少对话噪音
		// 不使用 selective=true，确保所有客户端/设备都能收到“移除键盘”的指令
		removeKeyboard := tgbotapi.NewRemoveKeyboard(false)
		msg, err := tbm.sendMessageWithMarkupAndReturn(chatID, "已取消", removeKeyboard)
		if err == nil && msg != nil {
			tbm.scheduleDeleteMessage(chatID, msg.MessageID, 800*time.Millisecond)
		}

		// 删除触发取消的用户消息（部分客户端/场景可能不允许，失败则忽略）
		if update.Message != nil {
			tbm.deleteMessage(chatID, update.Message.MessageID)
		}

		// 删除“编辑提示消息”，确保键盘不会在其他设备上回弹
		if promptMsgID != 0 {
			tbm.scheduleDeleteMessage(chatID, promptMsgID, 900*time.Millisecond)
		}
		return
	}

	// 验证输入长度
	if len(input) > 2000 {
		tbm.sendMessage(chatID, "❌ Prompt 内容过长，请限制在 2000 字符以内")
		return
	}

	if len(input) < 5 {
		tbm.sendMessage(chatID, "❌ Prompt 内容过短，请至少输入 5 个字符，或输入 'cancel' 取消")
		return
	}

	// 获取交易员
	trader, err := tbm.getPrimaryTrader(telegramID)
	if err != nil {
		sessionMgr.ClearSession(telegramID)
		tbm.sendMessage(chatID, "❌ 未找到交易员")
		return
	}

	// 更新数据库
	if err := tbm.db.UpdateTgTraderCustomPrompt(telegramID, trader.ID, input); err != nil {
		tbm.sendMessage(chatID, fmt.Sprintf("❌ 保存失败: %s", esc(err)))
		return
	}

	// 同步更新运行中的交易员（关键：立即生效）
	immediateEffect := false
	if trader.IsRunning {
		if traderObj, err := tbm.tgTraderMgr.GetTgTrader(trader.ID); err == nil {
			traderObj.SetCustomPrompt(input)
			immediateEffect = true
			log.Printf("🔁 已同步更新运行中交易员 %s 的自定义 Prompt", trader.Name)
		}
	}

	sessionMgr.ClearSession(telegramID)

	// 显示成功消息
	displayPrompt := input

	effectMsg := "💡 将在交易员启动后生效"
	if immediateEffect {
		effectMsg = "🟢 已立即生效，下次 AI 决策将使用新的 Prompt"
	}

	successMsg := fmt.Sprintf(`✅ <b>自定义 Prompt 已保存</b>

<code>%s</code>

%s`, esc(displayPrompt), effectMsg)

	tbm.sendMessageRemovingKeyboard(chatID, successMsg)
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

// analyzeMessageStructure 分析消息结构
func analyzeMessageStructure(message string) MessageStructure {
	structure := MessageStructure{
		HasCodeBlocks: strings.Contains(message, "```"),
		HasJSON:       strings.Contains(message, "📋 决策JSON"),
		HasReasoning:  strings.Contains(message, "🤖 AI思维链"),
		Sections:      make([]Section, 0),
	}

	// 识别主要分段
	sections := []string{
		"📊 周期信息",
		"🤖 AI思维链",
		"📋 决策JSON",
		"⚡ 执行结果",
		"💰 账户状态",
	}

	lastIndex := 0
	for _, section := range sections {
		if index := strings.Index(message, section); index >= lastIndex && index != -1 {
			structure.Sections = append(structure.Sections, Section{
				Name:  section,
				Start: index,
				End:   len(message), // 默认到结尾，会被下一节更新
			})

			// 更新上一节的结束位置
			if len(structure.Sections) > 1 {
				structure.Sections[len(structure.Sections)-2].End = index
			}
			lastIndex = index
		}
	}

	// 保留在第一个已知章节前的前言内容（标题/状态行），避免被分段逻辑丢弃
	if len(structure.Sections) > 0 && structure.Sections[0].Start > 0 {
		preambleEnd := structure.Sections[0].Start
		preamble := Section{
			Name:  "preamble",
			Start: 0,
			End:   preambleEnd,
		}
		structure.Sections = append([]Section{preamble}, structure.Sections...)
	}

	// 如果未识别到任何章节，确保整个消息作为一个分段返回，避免内容被清空
	if len(structure.Sections) == 0 {
		structure.Sections = []Section{
			{
				Name:  "full",
				Start: 0,
				End:   len(message),
			},
		}
	} else {
		// 确保最后一段覆盖到消息末尾
		structure.Sections[len(structure.Sections)-1].End = len(message)
	}

	return structure
}

// intelligentChunking 基于结构进行智能分段
func intelligentChunking(message string, structure MessageStructure, chunkSize int) []string {
	var chunks []string

	for _, section := range structure.Sections {
		sectionContent := message[section.Start:section.End]

		// 如果单个章节超过限制，需要进一步分割
		if len(sectionContent) > chunkSize {
			// 智能分割长章节
			subChunks := chunkLongSection(sectionContent, chunkSize)
			chunks = append(chunks, subChunks...)
		} else {
			// 检查添加这个章节是否会超出当前块的限制
			if len(chunks) == 0 {
				chunks = append(chunks, sectionContent)
			} else {
				lastChunk := chunks[len(chunks)-1]
				if len(lastChunk)+len(sectionContent) <= chunkSize {
					chunks[len(chunks)-1] = lastChunk + sectionContent
				} else {
					chunks = append(chunks, sectionContent)
				}
			}
		}
	}

	return chunks
}

// chunkLongSection 智能分割长章节
func chunkLongSection(content string, chunkSize int) []string {
	var chunks []string
	lines := strings.Split(content, "\n")
	var currentChunk strings.Builder
	currentChunk.Grow(chunkSize)

	for _, line := range lines {
		// 如果单行就超过限制，强制截断
		if len(line) > chunkSize {
			// 先添加当前累积的内容
			if currentChunk.Len() > 0 {
				chunks = append(chunks, currentChunk.String())
				currentChunk.Reset()
				currentChunk.Grow(chunkSize)
			}

			// 分割长行
			for len(line) > chunkSize {
				splitPos := chunkSize - 50 // 保留空间给省略号
				chunk := line[:splitPos] + "...[长行截断]"
				chunks = append(chunks, chunk)
				line = line[splitPos:]
			}
			currentChunk.WriteString(line)
			currentChunk.WriteString("\n")
			continue
		}

		// 检查添加这一行是否会超出限制
		testLength := currentChunk.Len() + len(line) + 1 // +1 for newline

		if testLength > chunkSize {
			// 保存当前块
			chunks = append(chunks, currentChunk.String())
			// 重置并开始新块
			currentChunk.Reset()
			currentChunk.Grow(chunkSize)
		}

		currentChunk.WriteString(line)
		currentChunk.WriteString("\n")
	}

	// 添加最后一个块
	if currentChunk.Len() > 0 {
		chunks = append(chunks, currentChunk.String())
	}

	return chunks
}

// validateChunks 验证分段完整性
func validateChunks(chunks []string) []string {
	var validChunks []string

	for i, chunk := range chunks {
		// UTF-8验证
		if !utf8.ValidString(chunk) {
			log.Printf("❌ 第%d段UTF-8无效，尝试修复", i+1)
			chunk = ensureUTF8Validity(chunk)
		}

		// 长度验证 (Telegram API limit is 4096 characters)
		const maxTelegramLength = 4096
		if len(chunk) > maxTelegramLength {
			log.Printf("❌ 第%d段超过长度限制(%d)，强制截断", i+1, len(chunk))
			chunk = chunk[:maxTelegramLength-50] + "...[截断]"
		}

		// 内容验证
		if len(strings.TrimSpace(chunk)) == 0 {
			log.Printf("⚠️ 第%d段内容为空，跳过", i+1)
			continue
		}

		validChunks = append(validChunks, chunk)
	}

	return validChunks
}

// ensureUTF8Validity 确保字符串的UTF-8有效性（telegram/bot.go版本）
func ensureUTF8Validity(text string) string {
	// 移除无效的UTF-8字符和不可见控制字符
	valid := make([]rune, 0, len(text))
	for _, r := range text {
		// 保留有效的UTF-8字符，移除控制字符（除了换行和制表符）
		if r == '\n' || r == '\t' || r == '\r' {
			valid = append(valid, r)
		} else if r >= 32 && r <= 126 || r > 127 {
			// 保留可打印ASCII字符和有效的多字节UTF-8字符
			valid = append(valid, r)
		}
	}
	return string(valid)
}

func splitDecisionMessage(text string, chunkSize int) []decisionChunk {
	// 1. 识别消息结构
	structure := analyzeMessageStructure(text)

	// 2. 基于结构进行智能分段
	chunks := intelligentChunking(text, structure, chunkSize)

	// 3. 验证分段完整性
	validatedChunks := validateChunks(chunks)

	// 4. 转换为decisionChunk格式
	result := make([]decisionChunk, 0, len(validatedChunks))
	for _, chunk := range validatedChunks {
		isJSON := strings.Contains(chunk, "📋 决策JSON") || strings.Contains(chunk, "```json")
		result = append(result, decisionChunk{
			Text:   chunk,
			IsJSON: isJSON,
		})
	}

	// 如果没有有效分段，返回原始内容
	if len(result) == 0 {
		log.Printf("⚠️ 智能分段失败，回退到原始内容")
		return []decisionChunk{{Text: text}}
	}

	log.Printf("✅ 智能分段完成: %d 段", len(result))
	return result
}

// splitRawMessage 将长消息按字符数切分，保持内容原样
func splitRawMessage(text string, limit int) []string {
	if limit <= 0 {
		return []string{text}
	}

	runes := []rune(text)
	if len(runes) == 0 {
		return []string{text}
	}

	var chunks []string
	for len(runes) > limit {
		chunks = append(chunks, string(runes[:limit]))
		runes = runes[limit:]
	}
	chunks = append(chunks, string(runes))
	return chunks
}

// splitRawSections 将原始消息分段：决策前、决策块、决策后，避免决策被截断
func splitRawSections(text string) []string {
	lower := strings.ToLower(text)
	start := strings.Index(lower, "<decision>")
	if start == -1 {
		return []string{text}
	}

	endRel := strings.Index(lower[start:], "</decision>")

	var sections []string

	if before := strings.TrimSpace(text[:start]); before != "" {
		sections = append(sections, before)
	}

	if endRel != -1 {
		end := start + endRel + len("</decision>")
		if decision := strings.TrimSpace(text[start:end]); decision != "" {
			sections = append(sections, decision)
		}
		if after := strings.TrimSpace(text[end:]); after != "" {
			after = trimLeadingFence(after)
			if after != "" {
				sections = append(sections, after)
			}
		}
	} else {
		// 无闭合标签，剩余全部作为决策块
		if decision := strings.TrimSpace(text[start:]); decision != "" {
			sections = append(sections, decision)
		}
	}

	if len(sections) == 0 {
		return []string{text}
	}
	return sections
}

// trimLeadingFence 去掉开头单独一行的 ``` 代码块标记，避免决策后内容多出一个围栏
func trimLeadingFence(s string) string {
	lines := strings.Split(s, "\n")
	i := 0
	// 跳过空行
	for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
		i++
	}
	if i < len(lines) && strings.TrimSpace(lines[i]) == "```" {
		i++
		// 跳过后续空行
		for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
			i++
		}
	}
	return strings.Join(lines[i:], "\n")
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
	tbm.rememberMediaGroupText(update.Message)
	message, _ := tbm.messageAnyText(update.Message)

	// 确保解析器可用
	if tbm.nlParser == nil {
		tbm.nlParser = NewNLParser(nil)
	}

	// 获取运行中的交易员并注入 MCP 客户端
	tgTraders, err := tbm.db.GetTgTraders(telegramID)
	if err != nil || len(tgTraders) == 0 {
		tbm.sendMessage(chatID, "❌ 未找到交易员配置，请先创建交易员")
		return true
	}
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
	autoTrader, err := tbm.tgTraderMgr.GetTgTrader(runningTrader.ID)
	if err != nil || autoTrader == nil {
		tbm.sendMessage(chatID, fmt.Sprintf("❌ 获取交易员失败: %v", err))
		return true
	}
	if client := autoTrader.GetMCPClient(); client != nil {
		tbm.nlParser = NewNLParser(client)
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
		// 对AI调用失败给出友好提示，避免用户看到HTML/报错原文
		if strings.Contains(err.Error(), "AI 调用失败") || strings.Contains(err.Error(), "API返回错误") {
			tbm.sendMessage(chatID, "⚠️ AI 解析服务暂时不可用或超时，请稍后重试或更换模型。")
			return true
		}
		return false
	}

	if cmd == nil {
		// 不是交易命令
		return false
	}

	// 防止误触全平：仅当用户显式提到“全部/全平/all”才允许 close_all
	if strings.EqualFold(cmd.Action, "close_all") {
		lower := strings.ToLower(message)
		explicitAll := strings.Contains(lower, "全") || strings.Contains(lower, "all") || strings.Contains(lower, "所有")
		if !explicitAll {
			if cmd.Symbol != "" {
				cmd.Action = "close"
			} else {
				tbm.sendMessage(chatID, "⚠️ 检测到平仓指令，但未明确写明“全部平仓”。请指定币种，如“平仓 ETH”。")
				return true
			}
		}
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
	// 简化流程：目前直接执行，不走会话确认
	if tbm.cmdValidator.NeedsConfirmation(cmd) {
		log.Printf("⚠️ 跳过确认，直接执行自然语言命令 [用户:%d]: %s %s", telegramID, cmd.Action, cmd.Symbol)
	}

	// 直接执行交易
	return tbm.executeNaturalLanguageCommand(chatID, telegramID, cmd)
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
	var tradeErr error
	displaySym := strings.ToUpper(strings.TrimSpace(cmd.Symbol))
	if displaySym == "" && cmd.Action != "close_all" {
		displaySym = "指定币种"
	}

	switch cmd.Action {
	case "long":
		tbm.sendMessage(chatID, fmt.Sprintf("🔄 正在执行开多 %s...", displaySym))
		_, tradeErr = autoTrader.ExecuteNaturalLanguageTrade("long", cmd.Symbol, cmd.Amount, cmd.Price, cmd.Leverage, cmd.AssetType)

	case "short":
		tbm.sendMessage(chatID, fmt.Sprintf("🔄 正在执行开空 %s...", displaySym))
		_, tradeErr = autoTrader.ExecuteNaturalLanguageTrade("short", cmd.Symbol, cmd.Amount, cmd.Price, cmd.Leverage, cmd.AssetType)

	case "close":
		tbm.sendMessage(chatID, fmt.Sprintf("🔄 正在执行平仓 %s...", displaySym))
		_, tradeErr = autoTrader.ExecuteNaturalLanguageTrade("close", cmd.Symbol, cmd.Amount, cmd.Price, 0, cmd.AssetType)

	case "close_all":
		tbm.sendMessage(chatID, "🔄 正在执行全部平仓...")
		_, tradeErr = autoTrader.ExecuteNaturalLanguageTrade("close_all", "", 0, 0, 0, cmd.AssetType)

	case "stop_loss":
		tbm.sendMessage(chatID, fmt.Sprintf("🔄 正在为 %s 设置止损...", displaySym))
		_, tradeErr = autoTrader.ExecuteNaturalLanguageTrade("stop_loss", cmd.Symbol, 0, cmd.Price, 0, cmd.AssetType)

	case "take_profit":
		tbm.sendMessage(chatID, fmt.Sprintf("🔄 正在为 %s 设置止盈...", displaySym))
		_, tradeErr = autoTrader.ExecuteNaturalLanguageTrade("take_profit", cmd.Symbol, 0, cmd.Price, 0, cmd.AssetType)

	default:
		tbm.sendMessage(chatID, fmt.Sprintf("❌ 不支持的操作类型: %s", cmd.Action))
		return true
	}

	// 7. 处理交易结果
	if tradeErr != nil {
		tbm.sendMessage(chatID, fmt.Sprintf("❌ 交易执行失败: %v", tradeErr))
		log.Printf("❌ 交易失败 [用户:%d]: %v", telegramID, tradeErr)
	} else {
		actionText := map[string]string{
			"long":        "开多",
			"short":       "开空",
			"close":       "平仓",
			"close_all":   "全部平仓",
			"stop_loss":   "设置止损",
			"take_profit": "设置止盈",
		}[cmd.Action]
		successMsg := "✅ 交易执行成功！"
		if cmd.Action == "close_all" {
			successMsg = "✅ 已提交全部平仓指令"
		} else if displaySym != "" {
			successMsg = fmt.Sprintf("✅ 已提交 %s %s", actionText, displaySym)
		}
		tbm.sendMessage(chatID, successMsg)

		log.Printf("✅ 交易成功 [用户:%d]: %s %s", telegramID, cmd.Action, cmd.Symbol)
	}

	return true
}
