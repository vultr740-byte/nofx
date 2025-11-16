package telegram

import (
	"fmt"
	"html"
	"log"
	"strconv"
	"strings"
	"unicode/utf8"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"nofx/config"
	"nofx/manager"
)

// TelegramBotManager Telegram Bot 管理器
type TelegramBotManager struct {
	bot          *tgbotapi.BotAPI
	db           config.DatabaseInterface
	hlService    *HyperliquidService
	debug        bool
	testnet      bool
	traderMgr    *manager.TraderManager
	tgTraderMgr  *TelegramTraderManager
	configWizard *ConfigWizard
}

func esc(v interface{}) string {
	return html.EscapeString(fmt.Sprint(v))
}

const (
	telegramMessageChunkSize = 3500
	decisionJSONMarker       = "📋 决策JSON"
)

type decisionChunk struct {
	Text   string
	IsJSON bool
}

// NewTelegramBotManager 创建 Telegram Bot 管理器
func NewTelegramBotManager(token string, db config.DatabaseInterface, debug bool, testnet bool, traderMgr *manager.TraderManager) (*TelegramBotManager, error) {
	bot, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return nil, fmt.Errorf("创建 Telegram Bot 失败: %w", err)
	}

	bot.Debug = debug

	network := "主网"
	if testnet {
		network = "测试网"
	}
	log.Printf("✅ Telegram Bot 初始化成功: %s (Hyperliquid: %s)", bot.Self.UserName, network)

	// 创建交易员管理器
	tgTraderMgr := NewTelegramTraderManager(db, traderMgr)
	configWizard := NewConfigWizard(tgTraderMgr)

	tgBotMgr := &TelegramBotManager{
		bot:          bot,
		db:           db,
		hlService:    NewHyperliquidService(),
		debug:        debug,
		testnet:      testnet,
		traderMgr:    traderMgr,
		tgTraderMgr:  tgTraderMgr,
		configWizard: configWizard,
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
			log.Printf("收到消息 [%s] %s", update.Message.From.UserName, update.Message.Text)
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
	case "set_api_key":
		tbm.handleSetAPIKey(update)
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

	if tbm.hasHyperliquidAccount(telegramID) {
		msg := fmt.Sprintf("👋 欢迎回来，%s！\n\n🤖 你的 AI 交易员已准备就绪。\n\n💡 快速入口:\n/deposit - 充值\n/start_trader - 启动交易\n/help - 帮助", esc(firstName))
		tbm.sendMessage(chatID, msg)
		return
	}

	defaultConfig := tbm.defaultTraderConfig()
	traderRecord, err := tbm.tgTraderMgr.CreateTrader(telegramID, defaultConfig)
	if err != nil {
		log.Printf("创建默认TG交易员失败: %v", err)
		tbm.sendMessage(chatID, "❌ 创建默认交易员失败，请稍后使用 /create_trader 重试。")
		return
	}

	// 发送欢迎消息（移除私钥显示，确保安全）
	welcomeMsg := fmt.Sprintf(`🎉 已为你创建默认 AI 交易员 <b>%s</b>

💼 钱包地址:
<code>%s</code>

🔐 安全说明:
• 交易私钥已安全加密存储在服务器
• 系统将自动使用私钥进行交易授权
• 私钥不会通过 Telegram 传输，确保安全

⚠️ 重要提醒:
• 此钱包由系统托管，仅用于交易
• 请勿向此地址转入大额资金
• 建议初始资金控制在风险可承受范围内

💡 下一步操作:
1. 使用 /deposit 获取充值地址
2. 充值 USDC 后用 /balance 查看余额
3. 如需更多策略，使用 /create_trader 再创建新的交易员`,
		esc(traderRecord.Name),
		esc(traderRecord.WalletAddress),
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

🤖 AI Agent 管理:
/start_trader - 启动 Agent 开始交易
/stop_trader - 停止 Agent
/trader_status - 查看 Agent 运行状态

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
		tbm.sendMessage(chatID, "❌ 请先使用 /create_trader 创建交易员")
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
}

// handlePositions 处理 /positions 命令
func (tbm *TelegramBotManager) handlePositions(update tgbotapi.Update) {
	chatID := update.Message.Chat.ID
	telegramID := update.Message.From.ID

	// 获取用户信息
	if _, err := tbm.db.GetTGUserByTelegramID(telegramID); err != nil {
		tbm.sendMessage(chatID, "❌ 请先使用 /start 初始化账号")
		return
	}

	// 检查是否有 Hyperliquid 账号
	if !tbm.hasHyperliquidAccount(telegramID) {
		tbm.sendMessage(chatID, "❌ 请先使用 /create_trader 创建交易员")
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
	positionsMsg, err := tbm.hlService.GetPositions(agentKey, walletAddr, tbm.testnet)
	if err != nil {
		log.Printf("查询持仓失败: %v", err)
		tbm.sendMessage(chatID, "❌ 查询持仓失败，请稍后重试")
		return
	}

	// 发送持仓信息
	tbm.sendMessage(chatID, positionsMsg)
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
		tbm.sendMessage(chatID, "❌ 请先使用 /create_trader 创建交易员")
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
	depositMsg := fmt.Sprintf(`💰 USDC 充值地址

<code>%s</code>

📋 充值说明:
• 支持资产: USDC
• 网络: Arbitrum One
• 最小充值: 20 USDC
• 到账时间: 通常 2-5 分钟

⚠️ 注意事项:
• 请勿充值其他资产到该地址
• 充值后可在 /balance 查看余额`, esc(walletAddr))

	tbm.sendMessage(chatID, depositMsg)
}

// handleSetAPIKey 处理 /set_api_key 命令
func (tbm *TelegramBotManager) handleSetAPIKey(update tgbotapi.Update) {
	chatID := update.Message.Chat.ID
	telegramID := update.Message.From.ID

	if _, err := tbm.db.GetTGUserByTelegramID(telegramID); err != nil {
		tbm.sendMessage(chatID, "❌ 请先使用 /start 与机器人建立连接")
		return
	}

	trader, err := tbm.getPrimaryTrader(telegramID)
	if err != nil {
		tbm.sendMessage(chatID, "❌ 您还没有创建交易员，请先使用 /create_trader")
		return
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
			if strings.TrimSpace(session.TraderConfig.AIModelAPIURL) != "" {
				session.TraderConfig.Step = StateSettingAIModelName
				sessionMgr.UpdateTraderConfig(telegramID, session.TraderConfig)
				sessionMgr.UpdateSessionState(telegramID, StateSettingAIModelName)
				tbm.sendMessageRemovingKeyboard(chatID, tbm.getAIModelNameMessage())
			} else {
				session.TraderConfig.Step = StateUpdatingAPIKey
				sessionMgr.UpdateTraderConfig(telegramID, session.TraderConfig)
				sessionMgr.UpdateSessionState(telegramID, StateUpdatingAPIKey)
				tbm.sendMessageRemovingKeyboard(chatID, tbm.configWizard.getAPIKeyMessage(provider))
			}
			return
		}

		tbm.sendAIProviderSelectionMessage(chatID, "❌ 无法识别的选项，请点击按钮选择 DeepSeek 或 Qwen，或输入 1 / 2。输入 \"cancel\" 可取消。")
	case StateSettingAIModelName:
		if strings.EqualFold(input, "skip") || input == "默认" || input == "default" || input == "" {
			session.TraderConfig.AIModelName = ""
		} else {
			session.TraderConfig.AIModelName = input
		}
		session.TraderConfig.Step = StateUpdatingAPIKey
		sessionMgr.UpdateTraderConfig(telegramID, session.TraderConfig)
		sessionMgr.UpdateSessionState(telegramID, StateUpdatingAPIKey)
		tbm.sendMessageRemovingKeyboard(chatID, tbm.configWizard.getAPIKeyMessage(session.TraderConfig.AIProvider))
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
	if session.State == StateUpdatingAIProvider || session.State == StateUpdatingAPIKey || session.State == StateSettingAIModelName {
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
			Description: "💰 查看账户余额",
		},
		{
			Command:     "positions",
			Description: "📊 查看当前持仓",
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
			Command:     "set_api_key",
			Description: "🔑 更新 AI API KEY",
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
	tbm.sendMessageWithMarkup(chatID, text, nil)
}

// sendMessageWithInlineKeyboard 发送带内联键盘的消息
func (tbm *TelegramBotManager) sendMessageWithInlineKeyboard(chatID int64, text string, keyboard tgbotapi.InlineKeyboardMarkup) {
	tbm.sendMessageWithMarkup(chatID, text, keyboard)
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
	tbm.sendMessageWithMarkup(chatID, text, keyboard)
}

// sendMessageRemovingKeyboard 发送消息并移除键盘
func (tbm *TelegramBotManager) sendMessageRemovingKeyboard(chatID int64, text string) {
	removeKeyboard := tgbotapi.NewRemoveKeyboard(true)
	tbm.sendMessageWithMarkup(chatID, text, removeKeyboard)
}

func (tbm *TelegramBotManager) getAIModelNameMessage() string {
	return `🧠 <b>自定义模型名称</b>

如果你在使用自定义 API URL，请输入后端要求的模型名称（如 qwen-turbo / deepseek-chat 等）。

直接按回车或输入 skip/default 表示使用默认模型。`
}

// sendMessageWithMarkup 通用的消息发送方法（可附带自定义键盘）
func (tbm *TelegramBotManager) sendMessageWithMarkup(chatID int64, text string, replyMarkup interface{}) {
	tbm.sendMessageWithMarkupAndReturn(chatID, text, replyMarkup)
}

// sendMessageWithMarkupAndReturn 发送消息并返回消息对象，便于后续更新
func (tbm *TelegramBotManager) sendMessageWithMarkupAndReturn(chatID int64, text string, replyMarkup interface{}) (*tgbotapi.Message, error) {
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

	log.Printf("📤 准备发送消息到 ChatID %d: %s", chatID, text)

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
		log.Printf("✅ 消息发送成功 (ChatID: %d)", chatID)
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
		return fmt.Errorf("❌ 尚未创建 Hyperliquid 账户，请先使用 /create_trader 创建交易员")
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

// handleCreateTrader 处理 /create_trader 命令
func (tbm *TelegramBotManager) handleCreateTrader(update tgbotapi.Update) {
	chatID := update.Message.Chat.ID
	telegramID := update.Message.From.ID

	// 检查用户是否已存在
	_, err := tbm.db.GetTGUserByTelegramID(telegramID)
	if err != nil {
		tbm.sendMessage(chatID, "❌ 请先使用 /create_trader 创建交易员")
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
		tbm.sendMessage(chatID, "❌ 请先使用 /create_trader 创建交易员")
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
		tbm.sendMessage(chatID, "❌ 请先使用 /create_trader 创建交易员")
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
		tbm.sendMessage(chatID, "❌ 您还没有创建交易员，请先使用 /create_trader 创建交易员")
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
		tbm.sendMessage(chatID, "❌ 请先使用 /create_trader 创建交易员")
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
	if len(chunks) > 1 {
		log.Printf("📝 决策消息较长，分段发送 (%d 段)", len(chunks))
	}

	for i, chunk := range chunks {
		chunkText := formatDecisionChunk(chunk, i, len(chunks))
		tbm.sendMessage(telegramID, chunkText)
	}

	log.Printf("✅ 成功推送AI决策到Telegram (ChatID: %d, 段数: %d)", telegramID, len(chunks))
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
	default:
		log.Printf("❌ 未知动作: %s", action)
		tbm.answerCallbackQuery(callback.ID, "未知操作")
	}
}

func (tbm *TelegramBotManager) handleStartTraderCallback(callback *tgbotapi.CallbackQuery, chatID int64, telegramID int64) {
	tbm.answerCallbackQuery(callback.ID, "▶️ 正在启动交易员...")

	if _, err := tbm.db.GetTGUserByTelegramID(telegramID); err != nil {
		tbm.sendMessage(chatID, "❌ 请先使用 /create_trader 创建交易员")
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

func (tbm *TelegramBotManager) handleStopTraderCallback(callback *tgbotapi.CallbackQuery, chatID int64, telegramID int64) {
	tbm.answerCallbackQuery(callback.ID, "⏹️ 正在检查交易员状态...")

	if _, err := tbm.db.GetTGUserByTelegramID(telegramID); err != nil {
		tbm.sendMessage(chatID, "❌ 请先使用 /create_trader 创建交易员")
		return
	}

	status, err := tbm.tgTraderMgr.GetTraderStatus(telegramID)
	if err != nil {
		tbm.sendMessage(chatID, fmt.Sprintf("❌ 获取交易员状态失败: %s", esc(err)))
		return
	}

	if !status["has_trader"].(bool) {
		tbm.sendMessage(chatID, "❌ 您还没有创建交易员，请先使用 /create_trader 创建交易员")
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

func (tbm *TelegramBotManager) defaultTraderConfig() *TraderConfig {
	template := findPromptTemplateByName("default")
	return &TraderConfig{
		PromptTemplate:      template.Name,
		InitialBalance:      1000,
		RiskLevel:           template.RiskLevel,
		BTCETHLeverage:      template.BTCETHLeverage,
		AltcoinLeverage:     template.AltcoinLeverage,
		ScanIntervalMinutes: template.ScanIntervalMinutes,
		AIProvider:          "deepseek",
	}
}

func findPromptTemplateByName(name string) PromptTemplate {
	templates := GetAvailablePromptTemplates()
	for _, tpl := range templates {
		if strings.EqualFold(tpl.Name, name) {
			return tpl
		}
	}
	if len(templates) == 0 {
		return PromptTemplate{
			Name:                "default",
			DisplayName:         "默认策略",
			RiskLevel:           "标准",
			BTCETHLeverage:      5,
			AltcoinLeverage:     3,
			ScanIntervalMinutes: 30,
		}
	}
	return templates[0]
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
