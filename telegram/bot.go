package telegram

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ethereum/go-ethereum/crypto"
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

	// 检查用户是否已存在
	user, err := tbm.db.GetTGUserByTelegramID(telegramID)
	if err == nil {
		// 用户已存在，检查是否有 Hyperliquid 账号
		if tbm.hasHyperliquidAccount(user) {
			msg := fmt.Sprintf("👋 欢迎回来，%s！\n\n您的 Hyperliquid 账号已创建完成。\n\n💡 可用命令:\n/deposit - 获取充值地址\n/balance - 查看余额\n/positions - 查看持仓\n/help - 显示此帮助信息", firstName)
			tbm.sendMessage(chatID, msg)
			return
		}
		// 用户存在但没有有效的 Hyperliquid 账号，继续创建新账号
	}

	// 创建新用户（如果不存在）
	if err != nil {
		if err := tbm.db.CreateTGUser(telegramID, username, firstName, chatID, languageCode); err != nil {
			log.Printf("创建 TGUser 失败: %v", err)
			tbm.sendMessage(chatID, "❌ 创建用户失败，请稍后重试。")
			return
		}
	}

	// 生成 Hyperliquid 账号
	agentKey, walletAddr, err := tbm.generateHyperliquidAccount()
	if err != nil {
		log.Printf("生成 Hyperliquid 账号失败: %v", err)
		tbm.sendMessage(chatID, "❌ 生成账号失败，请稍后重试。")
		return
	}

	// 更新 session_data
	sessionData := map[string]interface{}{
		"agent_key":      agentKey,
		"wallet_address": walletAddr,
		"created_at":     time.Now(),
		"account_status": "active",
	}

	if err := tbm.db.UpdateTGUserSession(telegramID, sessionData); err != nil {
		log.Printf("更新用户 session_data 失败: %v", err)
		tbm.sendMessage(chatID, "❌ 保存账号信息失败，请稍后重试。")
		return
	}

	// 发送欢迎消息
	welcomeMsg := fmt.Sprintf(`🎉 Hyperliquid 账号创建成功！

🔐 Agent Key:
%s

💼 钱包地址:
%s

⚠️ 重要安全提醒:
• 请安全保存 Agent Key，不要泄露给他人
• Agent Key 仅用于交易，余额应接近 0
• 主钱包资金安全，请妥善保管

💡 下一步操作:
使用 /deposit 获取充值地址
充值 USDC 后用 /balance 查看余额`, agentKey, walletAddr)

	tbm.sendMessage(chatID, welcomeMsg)
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

💡 提示: 每个用户自动创建一个 Agent`, firstName)

	tbm.sendMessage(chatID, helpMsg)
}

// handleBalance 处理 /balance 命令
func (tbm *TelegramBotManager) handleBalance(update tgbotapi.Update) {
	chatID := update.Message.Chat.ID
	telegramID := update.Message.From.ID

	// 获取用户信息
	user, err := tbm.db.GetTGUserByTelegramID(telegramID)
	if err != nil {
		tbm.sendMessage(chatID, "❌ 请先使用 /create_trader 创建交易员")
		return
	}

	// 检查是否有 Hyperliquid 账号
	if !tbm.hasHyperliquidAccount(user) {
		tbm.sendMessage(chatID, "❌ 请先使用 /create_trader 创建交易员")
		return
	}

	// 发送查询中消息
	tbm.sendMessage(chatID, "🔄 正在查询余额，请稍候...")

	// 提取 Agent Key 和 Wallet Address
	agentKey, walletAddr, err := tbm.extractAgentKeyAndWallet(user)
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
	user, err := tbm.db.GetTGUserByTelegramID(telegramID)
	if err != nil {
		tbm.sendMessage(chatID, "❌ 请先使用 /create_trader 创建交易员")
		return
	}

	// 检查是否有 Hyperliquid 账号
	if !tbm.hasHyperliquidAccount(user) {
		tbm.sendMessage(chatID, "❌ 请先使用 /create_trader 创建交易员")
		return
	}

	// 发送查询中消息
	tbm.sendMessage(chatID, "🔄 正在查询持仓，请稍候...")

	// 提取 Agent Key 和 Wallet Address
	agentKey, walletAddr, err := tbm.extractAgentKeyAndWallet(user)
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
	user, err := tbm.db.GetTGUserByTelegramID(telegramID)
	if err != nil {
		log.Printf("获取用户信息失败 %d: %v", telegramID, err)
		tbm.sendMessage(chatID, "❌ 请先使用 /create_trader 创建交易员")
		return
	}

	log.Printf("用户 %d session_data: %v", telegramID, user)

	// 检查是否有 Hyperliquid 账号
	hasAccount := tbm.hasHyperliquidAccount(user)
	log.Printf("用户 %d 是否有 Hyperliquid 账号: %v", telegramID, hasAccount)

	if !hasAccount {
		tbm.sendMessage(chatID, "❌ 请先使用 /create_trader 创建交易员")
		return
	}

	// 提取 Agent Key 和 Wallet Address
	_, walletAddr, err := tbm.extractAgentKeyAndWallet(user)
	if err != nil {
		log.Printf("提取账号信息失败: %v", err)
		tbm.sendMessage(chatID, "❌ 获取充值地址失败，请稍后重试")
		return
	}

	// 生成充值消息
	depositMsg := fmt.Sprintf("💰 USDC 充值地址\n\n```\n%s\n```\n\n📋 充值说明:\n• 支持资产: USDC\n• 网络: Arbitrum One\n• 最小充值: 20 USDC\n• 到账时间: 通常 2-5 分钟\n\n⚠️ 注意事项:\n• 请勿充值其他资产到该地址\n• 充值后可在 /balance 中查看余额", walletAddr)

	tbm.sendMessage(chatID, depositMsg)
}

// handleRegularMessage 处理普通消息
func (tbm *TelegramBotManager) handleRegularMessage(update tgbotapi.Update) {
	chatID := update.Message.Chat.ID
	telegramID := update.Message.From.ID
	message := update.Message.Text

	// 检查是否在配置向导过程中
	session := tbm.tgTraderMgr.GetSessionManager().GetOrCreateSession(telegramID)
	if session.State != StateIdle {
		// 如果正在输入API KEY，保存消息ID以便后续删除
		if session.State == StateSettingAPIKey {
			tbm.tgTraderMgr.GetSessionManager().UpdateLastMessageID(telegramID, update.Message.MessageID)
			log.Printf("🔍 保存API KEY消息ID: %d (用户: %d)", update.Message.MessageID, telegramID)
		}

		// 处理配置向导输入
		response, isComplete, err := tbm.configWizard.ProcessInput(telegramID, message)
		if err != nil {
			tbm.sendMessage(chatID, fmt.Sprintf("❌ 处理输入失败: %v", err))
			return
		}

		tbm.sendMessage(chatID, response)

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
	// UTF-8编码验证（参考PushDecisionToUser的实现）
	log.Printf("🔍 [DEBUG] 检查消息UTF-8编码 (ChatID: %d, 长度: %d)", chatID, len(text))
	if !utf8.ValidString(text) {
		log.Printf("❌ [DEBUG] 消息包含无效UTF-8字符！")
		// 清理无效字符
		cleanMsg := strings.ToValidUTF8(text, "�")
		log.Printf("✅ [DEBUG] 已清理无效UTF-8字符，使用清理后的消息")
		text = cleanMsg
	} else {
		log.Printf("✅ [DEBUG] 消息UTF-8编码有效")
	}

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown" // 统一使用Markdown格式

	log.Printf("📤 准备发送消息到 ChatID %d: %s", chatID, text)

	if _, err := tbm.bot.Send(msg); err != nil {
		// 如果Markdown格式失败，重试为普通文本
		if strings.Contains(err.Error(), "can't parse entities") {
			log.Printf("⚠️ Markdown格式解析失败，发送纯文本消息 (ChatID: %d)", chatID)
			msg.ParseMode = ""
			if _, err2 := tbm.bot.Send(msg); err2 != nil {
				log.Printf("❌ 发送消息失败 (ChatID: %d): %v", chatID, err2)
			} else {
				log.Printf("✅ 消息发送成功 (ChatID: %d)", chatID)
			}
		} else if strings.Contains(err.Error(), "text must be encoded in UTF-8") {
			// 如果仍然是UTF-8编码错误，强制清理
			log.Printf("⚠️ UTF-8编码错误，强制清理消息 (ChatID: %d)", chatID)
			cleanMsg := strings.ToValidUTF8(text, "�")
			msg.Text = cleanMsg
			msg.ParseMode = "" // 使用纯文本
			if _, err2 := tbm.bot.Send(msg); err2 != nil {
				log.Printf("❌ 发送消息失败 (ChatID: %d): %v", chatID, err2)
			} else {
				log.Printf("✅ 消息发送成功 (ChatID: %d)", chatID)
			}
		} else {
			log.Printf("❌ 发送消息失败 (ChatID: %d): %v", chatID, err)
		}
	} else {
		log.Printf("✅ 消息发送成功 (ChatID: %d)", chatID)
	}
}

// sendMessageWithInlineKeyboard 发送带内联键盘的消息
func (tbm *TelegramBotManager) sendMessageWithInlineKeyboard(chatID int64, text string, keyboard tgbotapi.InlineKeyboardMarkup) {
	log.Printf("🔍 [DEBUG] 检查消息UTF-8编码 (ChatID: %d, 长度: %d)", chatID, len(text))
	if !utf8.ValidString(text) {
		log.Printf("❌ [DEBUG] 消息包含无效UTF-8字符！")
		text = strings.ToValidUTF8(text, "�")
		log.Printf("✅ [DEBUG] 已清理无效UTF-8字符，使用清理后的消息")
	} else {
		log.Printf("✅ [DEBUG] 消息UTF-8编码有效")
	}

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = keyboard

	log.Printf("📤 准备发送带键盘的消息到 ChatID %d: %s", chatID, text)

	if _, err := tbm.bot.Send(msg); err != nil {
		if strings.Contains(err.Error(), "can't parse entities") {
			log.Printf("⚠️ Markdown格式解析失败，发送纯文本消息 (ChatID: %d)", chatID)
			msg.ParseMode = ""
			if _, err2 := tbm.bot.Send(msg); err2 != nil {
				log.Printf("❌ 发送消息失败 (ChatID: %d): %v", chatID, err2)
			} else {
				log.Printf("✅ 带键盘消息发送成功 (ChatID: %d)", chatID)
			}
		} else if strings.Contains(err.Error(), "text must be encoded in UTF-8") {
			log.Printf("⚠️ UTF-8编码错误，强制清理消息 (ChatID: %d)", chatID)
			msg.Text = strings.ToValidUTF8(text, "�")
			msg.ParseMode = ""
			if _, err2 := tbm.bot.Send(msg); err2 != nil {
				log.Printf("❌ 发送消息失败 (ChatID: %d): %v", chatID, err2)
			} else {
				log.Printf("✅ 带键盘消息发送成功 (ChatID: %d)", chatID)
			}
		} else {
			log.Printf("❌ 发送带键盘消息失败 (ChatID: %d): %v", chatID, err)
		}
	} else {
		log.Printf("✅ 带键盘消息发送成功 (ChatID: %d)", chatID)
	}
}

// generateHyperliquidAccount 生成 Hyperliquid 账号
func (tbm *TelegramBotManager) generateHyperliquidAccount() (agentKey, walletAddr string, err error) {
	// 1. 生成 Agent Private Key
	privateKey, err := crypto.GenerateKey()
	if err != nil {
		return "", "", fmt.Errorf("生成私钥失败: %w", err)
	}
	agentKey = hex.EncodeToString(privateKey.D.Bytes())

	// 2. 从 Agent Key 派生钱包地址
	privateKeyECDSA, err := crypto.ToECDSA(privateKey.D.Bytes())
	if err != nil {
		return "", "", fmt.Errorf("转换私钥格式失败: %w", err)
	}
	address := crypto.PubkeyToAddress(privateKeyECDSA.PublicKey)
	walletAddr = address.Hex()

	return agentKey, walletAddr, nil
}

// hasHyperliquidAccount 检查用户是否有 Hyperliquid 账号
func (tbm *TelegramBotManager) hasHyperliquidAccount(user interface{}) bool {
	userMap, ok := user.(map[string]interface{})
	if !ok {
		return false
	}

	sessionData, ok := userMap["session_data"]
	if !ok {
		return false
	}

	// 尝试将 session_data 转换为字符串
	var sessionDataStr string

	// 首先尝试直接作为字符串
	if str, ok := sessionData.(string); ok {
		sessionDataStr = str
	} else if bytes, ok := sessionData.([]byte); ok {
		// 如果是字节数组，转换为字符串
		sessionDataStr = string(bytes)
	} else {
		// 其他类型，尝试转换为 JSON 字符串
		if jsonBytes, err := json.Marshal(sessionData); err == nil {
			sessionDataStr = string(jsonBytes)
		} else {
			return false
		}
	}

	// 使用 HyperliquidService 的方法检查
	_, _, err := tbm.hlService.ExtractAgentKeyAndWallet(sessionDataStr)
	return err == nil
}

// extractAgentKeyAndWallet 从用户数据中提取 Agent Key 和 Wallet Address
func (tbm *TelegramBotManager) extractAgentKeyAndWallet(user interface{}) (agentKey, walletAddr string, err error) {
	userMap, ok := user.(map[string]interface{})
	if !ok {
		return "", "", fmt.Errorf("用户数据格式错误")
	}

	sessionData, ok := userMap["session_data"]
	if !ok {
		return "", "", fmt.Errorf("未找到 session_data")
	}

	// 尝试将 session_data 转换为字符串
	var sessionDataStr string

	// 首先尝试直接作为字符串
	if str, ok := sessionData.(string); ok {
		sessionDataStr = str
	} else if bytes, ok := sessionData.([]byte); ok {
		// 如果是字节数组，转换为字符串
		sessionDataStr = string(bytes)
	} else {
		// 其他类型，尝试转换为 JSON 字符串
		if jsonBytes, err := json.Marshal(sessionData); err == nil {
			sessionDataStr = string(jsonBytes)
		} else {
			return "", "", fmt.Errorf("session_data 格式错误")
		}
	}

	// 使用 HyperliquidService 提取
	return tbm.hlService.ExtractAgentKeyAndWallet(sessionDataStr)
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

	// 检查是否已有交易员
	status, err := tbm.tgTraderMgr.GetTraderStatus(telegramID)
	if err != nil {
		log.Printf("获取交易员状态失败: %v", err)
	} else if status["has_trader"].(bool) {
		tbm.sendMessage(chatID, "❌ 您已经创建过交易员了，每个用户只能创建一个交易员")
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

	// 启动交易员
	err = tbm.tgTraderMgr.StartTrader(telegramID)
	if err != nil {
		if err.Error() == "交易员已经在运行中" {
			tbm.sendMessage(chatID, "✅ 交易员已经在运行中！使用 /trader_status 查看运行状态")
		} else {
			tbm.sendMessage(chatID, fmt.Sprintf("❌ 启动交易员失败: %v", err))
		}
		return
	}

	tbm.sendMessage(chatID, "🚀 交易员启动成功！使用 /trader_status 查看运行状态")
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
		tbm.sendMessage(chatID, fmt.Sprintf("❌ 获取交易员状态失败: %v", err))
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
		tbm.sendMessage(chatID, fmt.Sprintf("❌ 检查仓位失败: %v", err))
		return
	}

	// 如果没有仓位，直接停止
	if !hasPositions {
		if err := tbm.tgTraderMgr.StopTrader(telegramID); err != nil {
			tbm.sendMessage(chatID, fmt.Sprintf("❌ 停止交易员失败: %v", err))
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
		tbm.sendMessage(chatID, fmt.Sprintf("❌ 获取交易员状态失败: %v", err))
		return
	}

	if !status["has_trader"].(bool) {
		noTraderMsg := `🤖 **交易员状态**

📊 您还没有创建交易员

💡 *下一步操作:*
/create_trader - 创建新的 AI 交易员`
		tbm.sendMessage(chatID, noTraderMsg)
		return
	}

	// 格式化状态消息
	statusEmoji := "⏹️"
	if status["is_running"].(bool) {
		statusEmoji = "🟢"
	}

	traderStatusMsg := fmt.Sprintf(`🤖 **交易员状态**

📊 基本信息
• 名称: %s
• 状态: %s %s
• 初始资金: %.0f USDC
• 策略: %s

⚙️ 配置参数
• BTC/ETH杠杆: %dx
• 山寨币杠杆: %dx
• 扫描间隔: %d 分钟`,
		status["name"],
		statusEmoji,
		status["status"],
		status["initial_balance"],
		tbm.getPromptDisplayName(status["prompt_template"].(string)),
		status["btc_eth_leverage"],
		status["altcoin_leverage"],
		status["scan_interval_minutes"],
	)

	// 如果有实时状态，添加详细信息
	if currentStatus, ok := status["current_status"]; ok {
		if currentStatusMap, ok := currentStatus.(map[string]interface{}); ok {
			traderStatusMsg += fmt.Sprintf(`

📈 **实时状态**
• 调用次数: %v
• 最后决策时间: %v`,
				currentStatusMap["call_count"],
				currentStatusMap["last_decision_time"],
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

	// 发送决策消息到用户
	msg := tgbotapi.NewMessage(telegramID, decisionMsg)
	msg.ParseMode = "Markdown" // 使用Markdown格式支持粗体和代码块

	// 发送消息
	_, err := tbm.bot.Send(msg)
	if err != nil {
		// 如果Markdown格式失败，重试为普通文本
		if strings.Contains(err.Error(), "can't parse entities") {
			log.Printf("⚠️ Markdown格式解析失败，发送纯文本消息 (ChatID: %d)", telegramID)
			msg.ParseMode = ""
			_, err = tbm.bot.Send(msg)
		} else if strings.Contains(err.Error(), "text must be encoded in UTF-8") {
			// 如果仍然是UTF-8编码错误，强制清理
			log.Printf("⚠️ UTF-8编码错误，强制清理消息 (ChatID: %d)", telegramID)
			cleanMsg := strings.ToValidUTF8(decisionMsg, "�")
			msg.Text = cleanMsg
			msg.ParseMode = "" // 使用纯文本
			_, err = tbm.bot.Send(msg)
		}
		return fmt.Errorf("发送决策消息失败: %w", err)
	}

	log.Printf("✅ 成功推送AI决策到Telegram (ChatID: %d)", telegramID)
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
	messageText := fmt.Sprintf(`⚠️ **停止交易员确认**

📊 检测到您有 %d 个未平仓位

请选择停止方式：`, positionCount)

	// 发送带有内联键盘的消息
	msg := tgbotapi.NewMessage(chatID, messageText)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = keyboard

	if _, err := tbm.bot.Send(msg); err != nil {
		log.Printf("❌ 发送停止确认消息失败 (ChatID: %d): %v", chatID, err)
		// 如果内联键盘失败，发送普通消息
		tbm.sendMessage(chatID, fmt.Sprintf("检测到您有 %d 个未平仓位。请手动确认是否平仓后停止交易员。", positionCount))
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

	if err := tbm.tgTraderMgr.StartTrader(telegramID); err != nil {
		if err.Error() == "交易员已经在运行中" {
			tbm.sendMessage(chatID, "✅ 交易员已经在运行中！使用 /trader_status 查看运行状态")
		} else {
			tbm.sendMessage(chatID, fmt.Sprintf("❌ 启动交易员失败: %v", err))
		}
		return
	}

	tbm.sendMessage(chatID, "🚀 交易员启动成功！使用 /trader_status 查看运行状态")
}

func (tbm *TelegramBotManager) handleStopTraderCallback(callback *tgbotapi.CallbackQuery, chatID int64, telegramID int64) {
	tbm.answerCallbackQuery(callback.ID, "⏹️ 正在检查交易员状态...")

	if _, err := tbm.db.GetTGUserByTelegramID(telegramID); err != nil {
		tbm.sendMessage(chatID, "❌ 请先使用 /create_trader 创建交易员")
		return
	}

	status, err := tbm.tgTraderMgr.GetTraderStatus(telegramID)
	if err != nil {
		tbm.sendMessage(chatID, fmt.Sprintf("❌ 获取交易员状态失败: %v", err))
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
		tbm.sendMessage(chatID, fmt.Sprintf("❌ 检查仓位失败: %v", err))
		return
	}

	if !hasPositions {
		if err := tbm.tgTraderMgr.StopTrader(telegramID); err != nil {
			tbm.sendMessage(chatID, fmt.Sprintf("❌ 停止交易员失败: %v", err))
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
		errorMsg := fmt.Sprintf("❌ 平仓并停止交易员失败: %v", err)
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
		errorMsg := fmt.Sprintf("❌ 停止交易员失败: %v", err)
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
	editConfig.ParseMode = "Markdown"

	if _, err := tbm.bot.Request(editConfig); err != nil {
		// 如果Markdown格式失败，尝试纯文本
		log.Printf("⚠️ 编辑消息Markdown格式失败，尝试纯文本 (MessageID: %d)", messageID)
		editConfig.ParseMode = ""
		if _, err2 := tbm.bot.Request(editConfig); err2 != nil {
			log.Printf("❌ 编辑回调消息失败 (MessageID: %d): %v", messageID, err2)
		}
	} else {
		log.Printf("✅ 编辑回调消息成功 (MessageID: %d)", messageID)
	}
}
