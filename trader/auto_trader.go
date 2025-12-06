package trader

import (
	"encoding/json"
	"fmt"
	"html"
	"log"
	"math"
	"nofx/config"
	"nofx/decision"
	"nofx/logger"
	"nofx/market"
	"nofx/mcp"
	"nofx/pool"
	"nofx/utils"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/sonirico/go-hyperliquid"
)

// decodeUnicodeEscapes 解码Unicode转义序列，确保UTF-8安全
func decodeUnicodeEscapes(text string) string {
	if text == "" {
		return text
	}

	// 扩展的Unicode转义序列映射，包含JSON和HTML常用字符
	unicodeMap := map[string]string{
		// HTML/XML特殊字符
		"\\u003e": ">", "\\u003c": "<", "\\u0026": "&", "\\u003d": "=",
		"\\u0022": "\"", "\\u0027": "'", "\\u005c": "\\",

		// 标点符号
		"\\u003b": ";", "\\u003a": ":", "\\u002c": ",", "\\u002e": ".",
		"\\u003f": "?", "\\u0021": "!", "\\u0060": "`", "\\u007e": "~",

		// 括号和符号
		"\\u007b": "{", "\\u007d": "}", "\\u005b": "[", "\\u005d": "]",
		"\\u0028": "(", "\\u0029": ")", "\\u007c": "|", "\\u002f": "/",

		// 数学和特殊符号
		"\\u002b": "+", "\\u002d": "-", "\\u002a": "*", "\\u0025": "%",
		"\\u005e": "^", "\\u0023": "#", "\\u0024": "$", "\\u0040": "@",

		// 控制字符
		"\\u000a": "\n", "\\u000d": "\r", "\\u0009": "\t",

		// 大写版本的Unicode转义（某些情况下出现）
		"\\u003E": ">", "\\u003C": "<", "\\u003D": "=", "\\u005C": "\\",
	}

	// 应用所有Unicode映射
	for unicode, char := range unicodeMap {
		text = strings.ReplaceAll(text, unicode, char)
	}

	// 移除不可见的Unicode字符（零宽度字符等）
	invisibleChars := []string{"\u200b", "\u200c", "\u200d", "\ufeff", "\u2060", "\u180e"}
	for _, char := range invisibleChars {
		text = strings.ReplaceAll(text, char, "")
	}

	// HTML解码
	text = html.UnescapeString(text)

	// UTF-8验证和清理
	var safe strings.Builder
	for _, r := range text {
		if r == utf8.RuneError {
			continue // 跳过无效的UTF-8字符
		}
		if r < 32 && r != '\n' && r != '\r' && r != '\t' {
			continue // 跳过控制字符（除了换行、回车、制表符）
		}
		safe.WriteRune(r)
	}

	return safe.String()
}

// decodeAllEncodings 统一处理HTML实体编码和Unicode转义序列
func decodeAllEncodings(text string) string {
	if text == "" {
		return text
	}

	// 先处理HTML实体编码（如 &lt;, &gt;, &#34; 等）
	text = html.UnescapeString(text)

	// 再处理Unicode转义序列（如 \u003c, \u003e 等）
	text = decodeUnicodeEscapes(text)

	return text
}

// formatDecisionJSON 格式化决策JSON，确保Unicode解码和 proper indentation
func formatDecisionJSON(jsonStr string) string {
	if jsonStr == "" {
		return jsonStr
	}

	// 首先解码任何Unicode转义序列
	decoded := decodeUnicodeEscapes(jsonStr)

	// 然后使用utils包进行适当的缩进格式化
	formatted := utils.FormatJSONError(decoded)

	return formatted
}

// smartTruncate 智能截断文本，保持逻辑结构和完整性
func smartTruncate(text string, maxLen int) string {
	if len(text) <= maxLen {
		return text
	}

	// 为省略号保留空间
	ellipsis := "...\n[思维链已截断，完整内容请查看日志]"
	targetLen := maxLen - len(ellipsis)

	// 尝试找到逻辑断点
	breakPoints := []string{
		"\n\n",  // 段落断点
		". ",    // 句子结束
		";\n",   // 语句结束
		"\n• ",  // 列表项
		"\n- ",  // 列表项
		"\n1. ", // 编号列表
		"\n**",  // Markdown标题
	}

	for _, breakPoint := range breakPoints {
		if idx := strings.LastIndex(text[:targetLen], breakPoint); idx > 0 {
			return text[:idx] + ellipsis
		}
	}

	// 回退到单词边界
	if idx := strings.LastIndex(text[:targetLen], " "); idx > 0 {
		return text[:idx] + ellipsis
	}

	// 最后的选择：字符边界
	return text[:targetLen] + ellipsis
}

// sanitizeErrorMessage 清理错误信息，避免HTML/控制字符污染Telegram推送
func sanitizeErrorMessage(errMsg string) string {
	if errMsg == "" {
		return ""
	}

	msg := html.UnescapeString(errMsg)

	// 移除HTML标签/DOCTYPE等
	tagStripper := regexp.MustCompile(`(?s)<[^>]+>`)
	msg = tagStripper.ReplaceAllString(msg, " ")

	// 去除控制字符，保留常见可见字符
	controlStripper := regexp.MustCompile(`[\x00-\x08\x0B\x0C\x0E-\x1F]`)
	msg = controlStripper.ReplaceAllString(msg, " ")

	// 压缩空白
	msg = strings.TrimSpace(regexp.MustCompile(`\s+`).ReplaceAllString(msg, " "))

	// 提示友好的超时描述
	lower := strings.ToLower(msg)
	if strings.Contains(lower, "status 504") || strings.Contains(lower, "gateway timeout") {
		msg = "AI 服务超时 (HTTP 504)，请稍后重试或更换模型/网络。详情: " + msg
	}

	// 限长，避免推送过长HTML正文
	if len(msg) > 400 {
		msg = msg[:397] + "..."
	}

	return msg
}

// AutoTraderConfig 自动交易配置（简化版 - AI全权决策）
type AutoTraderConfig struct {
	// Trader标识
	ID      string // Trader唯一标识（用于日志目录等）
	Name    string // Trader显示名称
	AIModel string // AI模型: "qwen" 或 "deepseek"

	// 交易平台选择
	Exchange string // "binance", "hyperliquid" 或 "aster"

	// 币安API配置
	BinanceAPIKey    string
	BinanceSecretKey string

	// Hyperliquid配置
	HyperliquidPrivateKey string
	HyperliquidWalletAddr string
	HyperliquidTestnet    bool

	// Aster配置
	AsterUser       string // Aster主钱包地址
	AsterSigner     string // Aster API钱包地址
	AsterPrivateKey string // Aster API钱包私钥

	CoinPoolAPIURL string

	// AI配置
	UseQwen     bool
	DeepSeekKey string
	QwenKey     string

	// 自定义AI API配置
	CustomAPIURL    string
	CustomAPIKey    string
	CustomModelName string

	// 扫描配置
	ScanInterval time.Duration // 扫描间隔（建议3分钟）

	// 账户配置
	InitialBalance float64 // 初始金额（用于计算盈亏，需手动设置）

	// 杠杆配置
	BTCETHLeverage  int // BTC和ETH的杠杆倍数
	AltcoinLeverage int // 山寨币的杠杆倍数

	// 风险控制（仅作为提示，AI可自主决定）
	MaxDailyLoss    float64       // 最大日亏损百分比（提示）
	MaxDrawdown     float64       // 最大回撤百分比（提示）
	StopTradingTime time.Duration // 触发风控后暂停时长

	// 仓位模式
	IsCrossMargin bool // true=全仓模式, false=逐仓模式

	// 反向交易配置
	ReverseTrading bool // 是否启用反向交易（true=开多时做空，开空时做多）

	// 币种配置
	DefaultCoins []string // 默认币种列表（从数据库获取）
	TradingCoins []string // 实际交易币种列表

	// 系统提示词模板
	SystemPromptTemplate string // 系统提示词模板名称（如 "default", "aggressive"）
}

// AutoTrader 自动交易器
type AutoTrader struct {
	id             string // Trader唯一标识
	name           string // Trader显示名称
	aiModel        string // AI模型名称
	exchange       string // 交易平台名称
	config         AutoTraderConfig
	trader         Trader // 使用Trader接口（支持多平台）
	mcpClient      *mcp.Client
	decisionLogger *logger.DecisionLogger // 决策日志记录器

	// 初始余额相关字段
	initialBalance     float64 // 被自动同步覆盖的余额（仅用于同步显示）
	userInitialBalance float64 // 用户配置的初始投资余额（用于收益率计算）

	dailyPnL              float64
	customPrompt          string   // 自定义交易策略prompt
	overrideBasePrompt    bool     // 是否覆盖基础prompt
	systemPromptTemplate  string   // 系统提示词模板名称
	defaultCoins          []string // 默认币种列表（从数据库获取）
	tradingCoins          []string // 实际交易币种列表
	lastResetTime         time.Time
	stopUntil             time.Time
	isRunning             bool
	startTime             time.Time          // 系统启动时间
	callCount             int                // AI调用次数
	positionFirstSeenTime map[string]int64   // 持仓首次出现时间 (symbol_side -> timestamp毫秒)
	stopMonitorCh         chan struct{}      // 用于停止监控goroutine
	monitorWg             sync.WaitGroup     // 用于等待监控goroutine结束
	peakPnLCache          map[string]float64 // 最高收益缓存 (symbol -> 峰值盈亏百分比)
	peakPnLCacheMutex     sync.RWMutex       // 缓存读写锁
	lastBalanceSyncTime   time.Time          // 上次余额同步时间
	database              interface{}        // 数据库引用（用于自动更新余额）
	userID                string             // 用户ID
	telegramBotManager    interface{}        // Telegram Bot管理器引用（用于TG交易员推送决策）
	reverseTrading        bool               // 是否启用反向交易（true=开多时做空，开空时做多）
}

// NewAutoTrader 创建自动交易器
func NewAutoTrader(config AutoTraderConfig, database interface{}, userID string) (*AutoTrader, error) {
	// 设置默认值
	if config.ID == "" {
		config.ID = "default_trader"
	}
	if config.Name == "" {
		config.Name = "Default Trader"
	}
	if config.AIModel == "" {
		if config.UseQwen {
			config.AIModel = "qwen"
		} else {
			config.AIModel = "deepseek"
		}
	}

	mcpClient := mcp.New()

	// 初始化AI
	if config.AIModel == "custom" {
		// 使用自定义API
		mcpClient.SetCustomAPI(config.CustomAPIURL, config.CustomAPIKey, config.CustomModelName)
		log.Printf("🤖 [%s] 使用自定义AI API: %s (模型: %s)", config.Name, config.CustomAPIURL, config.CustomModelName)
	} else if config.UseQwen || config.AIModel == "qwen" {
		// 使用Qwen (支持自定义URL和Model)
		mcpClient.SetQwenAPIKey(config.QwenKey, config.CustomAPIURL, config.CustomModelName)
		if config.CustomAPIURL != "" || config.CustomModelName != "" {
			log.Printf("🤖 [%s] 使用阿里云Qwen AI (自定义URL: %s, 模型: %s)", config.Name, config.CustomAPIURL, config.CustomModelName)
		} else {
			log.Printf("🤖 [%s] 使用阿里云Qwen AI", config.Name)
		}
	} else {
		// 默认使用DeepSeek (支持自定义URL和Model)
		mcpClient.SetDeepSeekAPIKey(config.DeepSeekKey, config.CustomAPIURL, config.CustomModelName)
		if config.CustomAPIURL != "" || config.CustomModelName != "" {
			log.Printf("🤖 [%s] 使用DeepSeek AI (自定义URL: %s, 模型: %s)", config.Name, config.CustomAPIURL, config.CustomModelName)
		} else {
			log.Printf("🤖 [%s] 使用DeepSeek AI", config.Name)
		}
	}

	// 初始化币种池API
	if config.CoinPoolAPIURL != "" {
		pool.SetCoinPoolAPI(config.CoinPoolAPIURL)
	}

	// 设置默认交易平台
	if config.Exchange == "" {
		config.Exchange = "binance"
	}

	// 设置杠杆默认值（防止为零）
	if config.BTCETHLeverage <= 0 {
		config.BTCETHLeverage = 3 // 默认3倍
		log.Printf("⚙️ [%s] BTC/ETH杠杆设置为默认值: %dx", config.Name, config.BTCETHLeverage)
	}
	if config.AltcoinLeverage <= 0 {
		config.AltcoinLeverage = 2 // 默认2倍
		log.Printf("⚙️ [%s] 山寨币杠杆设置为默认值: %dx", config.Name, config.AltcoinLeverage)
	}

	// 根据配置创建对应的交易器
	var trader Trader
	var err error

	// 记录仓位模式（通用）
	marginModeStr := "全仓"
	if !config.IsCrossMargin {
		marginModeStr = "逐仓"
	}
	log.Printf("📊 [%s] 仓位模式: %s", config.Name, marginModeStr)

	switch config.Exchange {
	case "binance":
		log.Printf("🏦 [%s] 使用币安合约交易", config.Name)
		trader = NewFuturesTrader(config.BinanceAPIKey, config.BinanceSecretKey)
	case "hyperliquid":
		log.Printf("🏦 [%s] 使用Hyperliquid交易", config.Name)
		trader, err = NewHyperliquidTrader(config.HyperliquidPrivateKey, config.HyperliquidWalletAddr, config.HyperliquidTestnet)
		if err != nil {
			return nil, fmt.Errorf("初始化Hyperliquid交易器失败: %w", err)
		}
	case "aster":
		log.Printf("🏦 [%s] 使用Aster交易", config.Name)
		trader, err = NewAsterTrader(config.AsterUser, config.AsterSigner, config.AsterPrivateKey)
		if err != nil {
			return nil, fmt.Errorf("初始化Aster交易器失败: %w", err)
		}
	default:
		return nil, fmt.Errorf("不支持的交易平台: %s", config.Exchange)
	}

	// 如果初始余额为0或负数，将在启动时自动获取
	if config.InitialBalance <= 0 {
		log.Printf("💰 [%s] 初始余额为 %.2f，将在启动时自动获取当前交易所余额", config.Name, config.InitialBalance)
	}

	// 初始化决策日志记录器（使用trader ID创建独立目录）
	logDir := fmt.Sprintf("decision_logs/%s", config.ID)
	decisionLogger := logger.NewDecisionLogger(logDir)

	// 设置默认系统提示词模板
	systemPromptTemplate := config.SystemPromptTemplate
	if systemPromptTemplate == "" {
		// 默认使用 default 提示词模板
		systemPromptTemplate = "default"
	}

	return &AutoTrader{
		id:             config.ID,
		name:           config.Name,
		aiModel:        config.AIModel,
		exchange:       config.Exchange,
		config:         config,
		trader:         trader,
		mcpClient:      mcpClient,
		decisionLogger: decisionLogger,

		// 初始化余额字段
		initialBalance:     config.InitialBalance, // 被自动同步覆盖的余额
		userInitialBalance: config.InitialBalance, // 用户配置的初始投资余额

		systemPromptTemplate:  systemPromptTemplate,
		defaultCoins:          config.DefaultCoins,
		tradingCoins:          config.TradingCoins,
		lastResetTime:         time.Now(),
		startTime:             time.Now(),
		callCount:             0,
		isRunning:             false,
		positionFirstSeenTime: make(map[string]int64),
		stopMonitorCh:         make(chan struct{}),
		monitorWg:             sync.WaitGroup{},
		peakPnLCache:          make(map[string]float64),
		peakPnLCacheMutex:     sync.RWMutex{},
		lastBalanceSyncTime:   time.Now(), // 初始化为当前时间
		database:              database,
		userID:                userID,
		telegramBotManager:    nil,                   // 初始化为空，后续通过SetTelegramBotManager设置
		reverseTrading:        config.ReverseTrading, // 从配置中读取反向交易设置
	}, nil
}

// SetTelegramBotManager 设置Telegram Bot管理器（用于TG交易员推送决策）
func (at *AutoTrader) SetTelegramBotManager(telegramBotManager interface{}) {
	at.telegramBotManager = telegramBotManager
}

// GetReverseTrading 获取反向交易配置
func (at *AutoTrader) GetReverseTrading() bool {
	return at.reverseTrading
}

// IsTGTrader 判断是否为TG交易员（userID为纯数字的Telegram ID）
func (at *AutoTrader) IsTGTrader() bool {
	return isTGTrader(at.userID)
}

// pushDecisionToTelegram 推送AI决策到Telegram（仅适用于TG交易员）
func (at *AutoTrader) pushDecisionToTelegram(record *logger.DecisionRecord) {
	// 检查是否有Telegram Bot管理器
	if at.telegramBotManager == nil {
		return
	}

	// 检查是否为TG交易员（通过userID判断是否为数字）
	// TG交易员的userID是Telegram ID，通常是数字
	if at.userID == "" || at.userID == "default" {
		return
	}

	// 使用类型断言来获取TelegramBotManager
	tgBotMgr, ok := at.telegramBotManager.(interface {
		PushDecisionToUser(telegramID int64, decisionMsg string) error
		PushRawMessageToUser(telegramID int64, rawMsg string) error
	})
	if !ok {
		log.Printf("⚠️ TelegramBotManager类型不匹配，无法推送决策")
		return
	}

	// 将userID转换为int64（Telegram ID）
	telegramID, err := strconv.ParseInt(at.userID, 10, 64)
	if err != nil {
		log.Printf("⚠️ 无法解析Telegram ID: %v", err)
		return
	}

	// 格式化为多段消息：1) 周期+决策JSON(+执行结果) 2) 思维链 3) 账户信息/错误
	messages := at.formatDecisionMessagesForTelegram(record)

	for idx, msg := range messages {
		if err := tgBotMgr.PushDecisionToUser(telegramID, msg); err != nil {
			log.Printf("⚠️ 推送决策到Telegram失败 (段 %d/%d): %v", idx+1, len(messages), err)
			return
		}
	}

	log.Printf("✅ 成功推送AI决策到Telegram (用户ID: %s)", at.userID)
}

// pushTradeExecutionToTelegram 推送单笔交易执行结果（仅TG交易员）
func (at *AutoTrader) pushTradeExecutionToTelegram(action *logger.DecisionAction) {
	if action == nil || at.telegramBotManager == nil || !isTGTrader(at.userID) {
		return
	}

	// “wait/hold” 已包含在主决策报告中，避免重复推送
	if action.Action == "wait" || action.Action == "hold" {
		return
	}

	type decisionPusher interface {
		PushDecisionToUser(telegramID int64, decisionMsg string) error
	}

	tgBotMgr, ok := at.telegramBotManager.(decisionPusher)
	if !ok {
		return
	}

	telegramID, err := strconv.ParseInt(at.userID, 10, 64)
	if err != nil {
		return
	}

	statusEmoji := "✅"
	statusText := "执行成功"
	if !action.Success {
		statusEmoji = "❌"
		statusText = "执行失败"
	}

	actionName := strings.ToUpper(strings.ReplaceAll(action.Action, "_", " "))
	quantity := math.Abs(action.Quantity)

	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("%s %s\n", statusEmoji, statusText))
	builder.WriteString(fmt.Sprintf("%s %s\n", actionName, action.Symbol))
	if quantity > 0 {
		builder.WriteString(fmt.Sprintf("• 数量: %.4f\n", quantity))
	}
	if action.Leverage > 0 {
		builder.WriteString(fmt.Sprintf("• 杠杆: %dx\n", action.Leverage))
	}
	if action.Price > 0 {
		builder.WriteString(fmt.Sprintf("• 价格: %.4f\n", action.Price))
	}
	if action.Profit != 0 {
		builder.WriteString(fmt.Sprintf("• 盈亏: %.2f USDT\n", action.Profit))
	}
	if action.StopLoss != nil {
		builder.WriteString(fmt.Sprintf("• 止损: %.4f\n", *action.StopLoss))
	}
	if action.TakeProfit != nil {
		builder.WriteString(fmt.Sprintf("• 止盈: %.4f\n", *action.TakeProfit))
	}
	if action.Error != "" {
		builder.WriteString(fmt.Sprintf("⚠️ %s\n", action.Error))
	}

	message := builder.String()

	// 包装为 <pre> 方便复制
	formatted := fmt.Sprintf("<pre>%s</pre>", html.EscapeString(message))

	if err := tgBotMgr.PushDecisionToUser(telegramID, formatted); err != nil {
		log.Printf("⚠️ 推送交易执行信息到Telegram失败: %v", err)
	}
}

// formatDecisionForTelegram 格式化AI决策为Telegram消息
// ⚠️ 重要编码注意事项：
// 1. Telegram API 要求所有文本必须是有效的UTF-8编码
// 2. 避免使用特殊字符、控制字符、不可打印字符
// 3. 使用标准ASCII和常见Unicode符号（如 ✅❌📊等）
// 4. 不要直接复制粘贴系统提示词中的特殊字符（如 \u0026 等转义序列）
// formatDecisionSummary 将AI决策转换为中文描述
func (at *AutoTrader) formatDecisionSummary(decisions []logger.DecisionAction) string {
	if len(decisions) == 0 {
		return "无操作"
	}

	// 过滤掉 hold/wait，不在标题展示
	filtered := make([]logger.DecisionAction, 0, len(decisions))
	for _, d := range decisions {
		if d.Action == "hold" || d.Action == "wait" {
			continue
		}
		filtered = append(filtered, d)
	}
	if len(filtered) == 0 {
		return ""
	}

	// 优先展示新增开仓（open_long/open_short）；若不存在开仓，则展示其它非 hold/wait 操作
	openDecisions := make([]logger.DecisionAction, 0)
	for _, d := range filtered {
		if d.Action == "open_long" || d.Action == "open_short" {
			openDecisions = append(openDecisions, d)
		}
	}
	target := filtered
	if len(openDecisions) > 0 {
		target = openDecisions
	}

	// 提取币种名称的辅助函数
	getCoinName := func(symbol string) string {
		// 移除USDT后缀，BTCUSDT -> BTC
		if strings.HasSuffix(symbol, "USDT") {
			return strings.TrimSuffix(symbol, "USDT")
		}
		return symbol
	}

	// 动作翻译
	actionTranslation := map[string]string{
		"open_long":          "做多开仓",
		"open_short":         "做空开仓",
		"close_long":         "做多平仓",
		"close_short":        "做空平仓",
		"update_stop_loss":   "调整止损",
		"update_take_profit": "调整止盈",
		"partial_close":      "部分平仓",
	}

	if len(target) == 1 {
		// 单个决策
		decision := target[0]
		coinName := getCoinName(decision.Symbol)
		actionText, exists := actionTranslation[decision.Action]
		if !exists {
			actionText = decision.Action
		}

		// 只有开仓和平仓操作显示杠杆
		if decision.Leverage > 0 && (strings.Contains(decision.Action, "open") || strings.Contains(decision.Action, "close")) {
			return fmt.Sprintf("%s(%dx)", coinName+actionText, decision.Leverage)
		}
		return coinName + actionText
	} else {
		// 多个决策
		var coinNames []string
		var leverages []string

		for _, decision := range target {
			coinNames = append(coinNames, getCoinName(decision.Symbol))
			if decision.Leverage > 0 && (strings.Contains(decision.Action, "open") || strings.Contains(decision.Action, "close")) {
				leverages = append(leverages, fmt.Sprintf("%dx", decision.Leverage))
			} else {
				leverages = append(leverages, "")
			}
		}

		// 合并币种名称
		coinsStr := strings.Join(coinNames, "/")

		// 确定主要动作（通常第一个决策的动作）
		mainAction := target[0].Action
		actionText, exists := actionTranslation[mainAction]
		if !exists {
			actionText = mainAction
		}

		// 合并杠杆信息（如果有）
		nonEmptyLeverages := []string{}
		for _, lev := range leverages {
			if lev != "" {
				nonEmptyLeverages = append(nonEmptyLeverages, lev)
			}
		}

		if len(nonEmptyLeverages) > 0 {
			return fmt.Sprintf("%s%s(%s)", coinsStr, actionText, strings.Join(nonEmptyLeverages, "/"))
		}
		return coinsStr + actionText
	}
}

// formatRawAIResponse 参考通用样式对AI原始输出做轻量格式化（不增加复杂规则）
func (at *AutoTrader) formatRawAIResponse(record *logger.DecisionRecord) string {
	var statusEmoji string
	if record.Success {
		statusEmoji = "✅"
	} else {
		statusEmoji = "❌"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s AI决策报告\n\n", statusEmoji)
	fmt.Fprintf(&b, "📊 周期信息\n")
	fmt.Fprintf(&b, "• 决策时间: %s\n", record.Timestamp.Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&b, "• 周期编号: #%d\n\n", record.CycleNumber)

	raw := strings.TrimSpace(record.RawAIResponse)
	if raw != "" {
		fmt.Fprintf(&b, "🤖 AI思维链\n```\n%s\n```\n\n", raw)
	}

	if len(record.Decisions) > 0 {
		b.WriteString("⚡ 执行结果\n")
		for _, d := range record.Decisions {
			status := "⏳"
			if d.Success {
				if d.Action != "wait" && d.Action != "hold" {
					status = "✅"
				}
			} else {
				status = "❌"
			}
			line := fmt.Sprintf("%s %s %s", status, d.Symbol, d.Action)
			if d.Error != "" {
				line += fmt.Sprintf(" (%s)", d.Error)
			}
			fmt.Fprintf(&b, "%s\n", line)
		}
		b.WriteString("\n")
	}

	return b.String()
}

// preprocessDecisionRecord 统一处理决策记录中的所有编码问题
func (at *AutoTrader) preprocessDecisionRecord(record *logger.DecisionRecord) *logger.DecisionRecord {
	// 创建副本避免修改原始记录
	processed := *record

	// 处理CoT思维链编码问题
	processed.CoTTrace = decodeAllEncodings(record.CoTTrace)

	// 处理JSON编码问题
	if record.DecisionJSON != "" {
		processed.DecisionJSON = decodeAllEncodings(record.DecisionJSON)
	}

	// 确保UTF-8有效性
	processed.CoTTrace = ensureUTF8Validity(processed.CoTTrace)
	processed.DecisionJSON = ensureUTF8Validity(processed.DecisionJSON)

	// 处理错误信息编码
	if record.ErrorMessage != "" {
		processed.ErrorMessage = decodeAllEncodings(record.ErrorMessage)
		processed.ErrorMessage = ensureUTF8Validity(processed.ErrorMessage)
	}

	return &processed
}

// ensureUTF8Validity 确保字符串的UTF-8有效性
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

// buildCompleteDecisionMessage 构建完整的决策消息（不截断）
func (at *AutoTrader) buildCompleteDecisionMessage(record *logger.DecisionRecord) string {
	var statusEmoji string
	if record.Success {
		statusEmoji = "✅"
	} else {
		statusEmoji = "❌"
	}

	// 生成决策摘要
	decisionSummary := at.formatDecisionSummary(record.Decisions)
	title := "AI决策报告"
	if decisionSummary != "" {
		title += " - " + decisionSummary
	}

	var msg strings.Builder
	msg.Grow(10000) // 预分配足够空间

	fmt.Fprintf(&msg, "%s %s\n\n", statusEmoji, html.EscapeString(title))
	fmt.Fprintf(&msg, "📊 周期信息\n")
	fmt.Fprintf(&msg, "• 决策时间: %s\n", record.Timestamp.Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&msg, "• 周期编号: #%d\n", record.CycleNumber)

	// 思维链
	if record.CoTTrace != "" {
		cot := record.CoTTrace
		fmt.Fprintf(&msg, "\n🤖 AI思维链\n<pre>%s</pre>\n", html.EscapeString(cot))
	}

	// 决策JSON
	if record.DecisionJSON != "" {
		var jsonBlock string
		if at.isValidJSON(record.DecisionJSON) {
			jsonBlock = formatDecisionJSON(record.DecisionJSON)
		} else {
			jsonBlock = record.DecisionJSON
		}
		fmt.Fprintf(&msg, "\n📋 决策JSON\n<pre>%s</pre>\n", html.EscapeString(jsonBlock))
	}

	// 执行结果
	if len(record.Decisions) > 0 {
		msg.WriteString("\n⚡ 执行结果\n")
		for _, decision := range record.Decisions {
			decisionStatus := "❌"
			if decision.Success {
				if decision.Action == "wait" || decision.Action == "hold" {
					decisionStatus = "⏳"
				} else {
					decisionStatus = "✅"
				}
			}
			line := fmt.Sprintf("%s %s %s", decisionStatus, decision.Symbol, decision.Action)
			if decision.Error != "" {
				line += fmt.Sprintf(" (%s)", decision.Error)
			}
			fmt.Fprintf(&msg, "%s\n", html.EscapeString(line))
		}
	}

	// 账户状态
	fmt.Fprintf(&msg, "\n💰 账户状态\n")
	fmt.Fprintf(&msg, "• 总余额: %.2f USDT\n", record.AccountState.TotalBalance)
	fmt.Fprintf(&msg, "• 可用余额: %.2f USDT\n", record.AccountState.AvailableBalance)
	if record.AccountState.PositionCount > 0 {
		fmt.Fprintf(&msg, "• 持仓数量: %d\n", record.AccountState.PositionCount)
		fmt.Fprintf(&msg, "• 未实现盈亏: %.2f USDT\n", record.AccountState.TotalUnrealizedProfit)
	}

	// 错误信息
	if record.ErrorMessage != "" {
		safeError := sanitizeErrorMessage(record.ErrorMessage)
		if safeError == "" {
			safeError = "AI 服务暂时不可用，请稍后重试或检查网络/模型配置"
		}
		fmt.Fprintf(&msg, "\n⚠️ 错误信息: %s\n", html.EscapeString(safeError))
	}

	fmt.Fprintf(&msg, "\n🤖 由 %s 自动推送", html.EscapeString(at.name))

	return msg.String()
}

// isValidJSON 验证JSON字符串是否有效
func (at *AutoTrader) isValidJSON(jsonStr string) bool {
	var js json.RawMessage
	return json.Unmarshal([]byte(jsonStr), &js) == nil
}

// validateMessageIntegrity 验证消息完整性
func (at *AutoTrader) validateMessageIntegrity(msg string, originalRecord *logger.DecisionRecord) error {
	// 1. 长度验证
	if len(msg) == 0 {
		return fmt.Errorf("消息为空")
	}

	// 2. 关键内容验证
	if originalRecord.CoTTrace != "" && !strings.Contains(msg, "🤖 AI思维链") {
		return fmt.Errorf("思维链内容丢失")
	}

	if originalRecord.DecisionJSON != "" && !strings.Contains(msg, "📋 决策JSON") {
		return fmt.Errorf("决策JSON内容丢失")
	}

	// 3. UTF-8有效性验证
	if !utf8.ValidString(msg) {
		return fmt.Errorf("消息包含无效UTF-8字符")
	}

	// 4. 逻辑完整性验证
	expectedSections := []string{
		"📊 周期信息",
		"💰 账户状态",
	}

	for _, section := range expectedSections {
		if !strings.Contains(msg, section) {
			log.Printf("⚠️ 缺少预期章节: %s", section)
		}
	}

	return nil
}

// 5. 如果遇到编码错误，检查：
//   - 数据源是否包含不可见字符
//   - 字符串拼接是否正确处理了转义
//   - 是否有直接从外部源复制的内容
// formatDecisionMessagesForTelegram 将决策拆分为三段：周期+决策JSON(+执行结果) / 思维链 / 账户信息
func (at *AutoTrader) formatDecisionMessagesForTelegram(record *logger.DecisionRecord) []string {
	// 1. 统一编码处理 - 在最开始就处理所有编码问题
	processedRecord := at.preprocessDecisionRecord(record)

	var messages []string

	// 状态图标
	statusEmoji := "❌"
	if record.Success {
		statusEmoji = "✅"
	}

	// 决策摘要作为标题
	decisionSummary := at.formatDecisionSummary(processedRecord.Decisions)
	title := "AI决策报告"
	if decisionSummary != "" {
		title += " - " + decisionSummary
	}

	// 1) 决策周期 + 决策JSON + 执行结果（整合为一条）
	{
		var b strings.Builder
		b.Grow(4000)
		fmt.Fprintf(&b, "%s AI决策周期: #%d\n\n", statusEmoji, processedRecord.CycleNumber)

		if len(processedRecord.Decisions) > 0 {
			b.WriteString("📋 决策内容\n")
			for _, d := range processedRecord.Decisions {
				line := fmt.Sprintf("• %s %s", html.EscapeString(d.Symbol), html.EscapeString(d.Action))
				if d.Leverage > 0 {
					line += fmt.Sprintf(" x%d", d.Leverage)
				}
				if d.Quantity > 0 {
					line += fmt.Sprintf(" | 数量: %.4f", d.Quantity)
				}
				if d.Price > 0 {
					line += fmt.Sprintf(" | 价格: %.4f", d.Price)
				}
				if d.StopLoss != nil {
					line += fmt.Sprintf(" | 止损: %.4f", *d.StopLoss)
				}
				if d.TakeProfit != nil {
					line += fmt.Sprintf(" | 止盈: %.4f", *d.TakeProfit)
				}
				if d.Profit != 0 {
					line += fmt.Sprintf(" | 本次盈亏: %.2f", d.Profit)
				}
				if d.Error != "" {
					line += fmt.Sprintf("\n  错误: %s", html.EscapeString(d.Error))
				}
				fmt.Fprintf(&b, "%s\n", line)
			}
		} else {
			b.WriteString("📋 决策内容\n")
			b.WriteString("• 无结构化决策（AI未输出 decision 标签/JSON）\n")
		}

		messages = append(messages, b.String())
	}

	// 2) 精简思维链
	if processedRecord.CoTTrace != "" {
		var b strings.Builder
		b.Grow(len(processedRecord.CoTTrace) + 200)
		b.WriteString("🤖 AI思维链\n")
		fmt.Fprintf(&b, "<pre>%s</pre>\n", html.EscapeString(processedRecord.CoTTrace))
		messages = append(messages, b.String())
	}

	// 3) 账户信息 + 错误
	{
		var b strings.Builder
		b.Grow(500)
		b.WriteString("💰 账户状态\n")
		fmt.Fprintf(&b, "• 总余额: %.2f USDT\n", processedRecord.AccountState.TotalBalance)
		fmt.Fprintf(&b, "• 可用余额: %.2f USDT\n", processedRecord.AccountState.AvailableBalance)
		if processedRecord.AccountState.PositionCount > 0 {
			fmt.Fprintf(&b, "• 持仓数量: %d\n", processedRecord.AccountState.PositionCount)
			fmt.Fprintf(&b, "• 未实现盈亏: %.2f USDT\n", processedRecord.AccountState.TotalUnrealizedProfit)
		}

		if processedRecord.ErrorMessage != "" {
			safeError := sanitizeErrorMessage(processedRecord.ErrorMessage)
			if safeError == "" {
				safeError = "AI 服务暂时不可用，请稍后重试或检查网络/模型配置"
			}
			fmt.Fprintf(&b, "\n⚠️ 错误信息: %s\n", html.EscapeString(safeError))
		}

		fmt.Fprintf(&b, "\n🤖 由 %s 自动推送", html.EscapeString(at.name))
		messages = append(messages, b.String())
	}

	return messages
}

// buildFallbackMessage 构建备用消息（当完整性验证失败时使用）
func (at *AutoTrader) buildFallbackMessage(record *logger.DecisionRecord) string {
	var statusEmoji string
	if record.Success {
		statusEmoji = "✅"
	} else {
		statusEmoji = "❌"
	}

	log.Printf("🔧 构建备用消息确保内容完整性")

	// 简化的备用消息，确保核心信息不丢失
	var msg strings.Builder
	msg.Grow(5000)

	msg.WriteString(fmt.Sprintf("%s AI决策报告（备用格式）\n\n", statusEmoji))
	msg.WriteString(fmt.Sprintf("📊 决策时间: %s\n", record.Timestamp.Format("2006-01-02 15:04:05")))
	msg.WriteString(fmt.Sprintf("📊 周期编号: #%d\n\n", record.CycleNumber))

	// 包含思维链（原样，不截断）
	if record.CoTTrace != "" {
		msg.WriteString("🤖 AI思维链:\n")
		msg.WriteString(fmt.Sprintf("<pre>%s</pre>\n\n", html.EscapeString(record.CoTTrace)))
	}

	// 包含决策JSON（原样）
	if record.DecisionJSON != "" {
		msg.WriteString("📋 决策数据:\n")
		msg.WriteString(fmt.Sprintf("<pre>%s</pre>\n\n", html.EscapeString(record.DecisionJSON)))
	}

	// 基础执行结果
	msg.WriteString("⚡ 执行结果: ")
	if len(record.Decisions) > 0 {
		for i, decision := range record.Decisions {
			if i > 0 {
				msg.WriteString(", ")
			}
			msg.WriteString(html.EscapeString(fmt.Sprintf("%s %s", decision.Symbol, decision.Action)))
		}
	} else {
		msg.WriteString("无决策")
	}

	msg.WriteString(fmt.Sprintf("\n\n🤖 由 %s 自动推送", html.EscapeString(at.name)))

	return msg.String()
}

// Run 运行自动交易主循环
func (at *AutoTrader) Run() error {
	at.isRunning = true
	log.Println("🚀 AI驱动自动交易系统启动")

	log.Printf("📥 [InitBalance] trader=%s user=%s initial=%.2f (config/db)", at.name, at.userID, at.initialBalance)

	// 如果初始余额为0，先获取当前余额
	if at.initialBalance <= 0 {
		log.Printf("💰 正在自动获取当前交易所余额...")
		balanceInfo, err := at.trader.GetBalance()
		if err != nil {
			log.Printf("❌ 获取初始余额失败: %v", err)
			return fmt.Errorf("获取初始余额失败: %w", err)
		}

		// 优先使用总资产，而不是可用余额，避免持仓时计算偏小
		var actualBalance float64
		if equity, ok := balanceInfo["totalWalletBalance"].(float64); ok && equity > 0 {
			actualBalance = equity
			log.Printf("📊 [InitBalance] 使用 totalWalletBalance=%.4f", equity)
		} else if equity, ok := balanceInfo["total_equity"].(float64); ok && equity > 0 {
			actualBalance = equity
			log.Printf("📊 [InitBalance] 使用 total_equity=%.4f", equity)
		} else if accountValue, ok := balanceInfo["accountValue"].(float64); ok && accountValue > 0 {
			actualBalance = accountValue
			log.Printf("📊 [InitBalance] 使用 accountValue=%.4f", accountValue)
		} else if totalBalance, ok := balanceInfo["balance"].(float64); ok && totalBalance > 0 {
			actualBalance = totalBalance
			log.Printf("📊 [InitBalance] 使用 balance=%.4f", totalBalance)
		} else if availableBalance, ok := balanceInfo["availableBalance"].(float64); ok && availableBalance > 0 {
			actualBalance = availableBalance
			log.Printf("📊 [InitBalance] 使用 availableBalance=%.4f", availableBalance)
		} else if availableBalance, ok := balanceInfo["available_balance"].(float64); ok && availableBalance > 0 {
			actualBalance = availableBalance
			log.Printf("📊 [InitBalance] 使用 available_balance=%.4f", availableBalance)
		}

		if actualBalance > 0 {
			at.initialBalance = actualBalance
			log.Printf("✅ 自动获取初始余额成功: %.2f USDT", at.initialBalance)

			// 更新数据库中的初始余额
			if db, ok := at.database.(config.DatabaseInterface); ok {
				// 检查是否为TG交易员（userID为数字字符串）
				if isTGTrader(at.userID) {
					// TG交易员使用专门的方法
					if tgUserID, err := strconv.ParseInt(at.userID, 10, 64); err == nil {
						if err := db.UpdateTgTraderInitialBalance(tgUserID, at.id, at.initialBalance); err != nil {
							log.Printf("⚠️ [%s] 更新TG交易员数据库初始余额失败: %v", at.name, err)
						} else {
							log.Printf("✅ [%s] 已更新TG交易员数据库初始余额", at.name)
						}
					} else {
						log.Printf("⚠️ [%s] TG用户ID解析失败: %v", at.name, err)
					}
				} else {
					// 普通交易员使用原方法
					if err := db.UpdateTraderInitialBalance(at.userID, at.id, at.initialBalance); err != nil {
						log.Printf("⚠️ [%s] 更新数据库初始余额失败: %v", at.name, err)
					} else {
						log.Printf("✅ [%s] 已更新数据库初始余额", at.name)
					}
				}
			}
		} else {
			log.Printf("⚠️ 警告: 交易所余额为0或无法解析，继续使用初始余额: %.2f", at.initialBalance)
		}
	} else {
		log.Printf("📊 [InitBalance] 使用数据库初始余额: %.2f USDT", at.initialBalance)
	}

	log.Printf("💰 初始余额: %.2f USDT", at.initialBalance)
	log.Printf("⚙️  扫描间隔: %v", at.config.ScanInterval)
	log.Println("🤖 AI将全权决定杠杆、仓位大小、止损止盈等参数")

	// 启动回撤监控
	at.startDrawdownMonitor()

	ticker := time.NewTicker(at.config.ScanInterval)
	defer ticker.Stop()

	// 首次立即执行
	if err := at.runCycle(); err != nil {
		log.Printf("❌ 执行失败: %v", err)
	}

	for at.isRunning {
		select {
		case <-ticker.C:
			if err := at.runCycle(); err != nil {
				log.Printf("❌ 执行失败: %v", err)
			}
		case <-at.stopMonitorCh:
			// 🔥 关键修复：监听停止信号，立即退出循环
			log.Printf("🛑 收到停止信号，正在退出AI决策循环...")
			return nil
		}
	}

	return nil
}

// Stop 停止自动交易
func (at *AutoTrader) Stop() {
	if !at.isRunning {
		return
	}
	at.isRunning = false

	// 安全地关闭停止通道（防止重复关闭导致panic）
	select {
	case <-at.stopMonitorCh:
		// 通道已关闭，无需再次关闭
	default:
		// 通道未关闭，安全关闭
		close(at.stopMonitorCh)
	}

	at.monitorWg.Wait() // 等待监控goroutine结束
	log.Println("⏹ 自动交易系统停止")
}

// StopWithPositionsClose 停止自动交易并平掉所有仓位
func (at *AutoTrader) StopWithPositionsClose() ([]string, error) {
	if !at.isRunning {
		return nil, fmt.Errorf("交易员未运行")
	}

	var closeResults []string
	var hasError bool

	// 1. 先获取所有仓位
	positions, err := at.trader.GetPositions()
	if err != nil {
		log.Printf("❌ 获取仓位失败: %v", err)
		return nil, fmt.Errorf("获取仓位失败: %w", err)
	}

	// 2. 如果有仓位，逐一平仓
	if len(positions) > 0 {
		log.Printf("🔄 发现 %d 个仓位，开始平仓...", len(positions))

		for _, position := range positions {
			symbol, ok := position["symbol"].(string)
			if !ok {
				continue
			}

			// 提取仓位数量 - 优先使用 Szi 字段（Hyperliquid实际仓位数量）
			var size float64
			if szi, ok := position["Szi"].(string); ok && szi != "" {
				if parsedSize, err := strconv.ParseFloat(szi, 64); err == nil {
					size = parsedSize
					log.Printf("✅ 从 Szi 字段提取 %s 仓位数量: %f", symbol, size)
				} else {
					log.Printf("⚠️ Szi 字段解析失败: %v", err)
				}
			}

			// 如果 Szi 字段无效，尝试其他字段
			if size == 0 {
				size, _ = position["quantity"].(float64)
			}
			if size == 0 {
				size, _ = position["size"].(float64)
			}
			if size == 0 {
				size, _ = position["positionAmt"].(float64)
			}

			positionValue, _ := position["markPrice"].(float64)

			// 跳过仓位为0的
			if size == 0 {
				log.Printf("⚠️ %s 仓位为0，跳过", symbol)
				continue
			}

			log.Printf("🔄 平仓处理: %s (仓位: %.6f, 价值: %.2f)", symbol, size, positionValue)

			// 创建平仓决策记录
			actionRecord := &logger.DecisionAction{
				Action:    "close_position",
				Symbol:    symbol,
				Quantity:  math.Abs(size),
				Timestamp: time.Now(),
			}

			// 根据仓位方向执行平仓
			if size > 0 {
				// 多头仓位，执行平多
				err = at.executeCloseLongWithRecord(nil, actionRecord)
			} else {
				// 空头仓位，执行平空
				err = at.executeCloseShortWithRecord(nil, actionRecord)
			}

			if err != nil {
				errorMsg := fmt.Sprintf("❌ 平仓失败 %s: %v", symbol, err)
				log.Print(errorMsg)
				closeResults = append(closeResults, errorMsg)
				hasError = true
			} else {
				successMsg := fmt.Sprintf("✅ 平仓成功 %s", symbol)
				log.Print(successMsg)
				closeResults = append(closeResults, successMsg)
			}
		}

		// 等待平仓操作完成
		time.Sleep(2 * time.Second)
	} else {
		log.Printf("✅ 无未平仓合约，直接停止交易")
	}

	// 3. 停止交易
	at.Stop()

	// 4. 返回平仓结果
	if hasError {
		return closeResults, fmt.Errorf("部分仓位平仓失败")
	}

	return closeResults, nil
}

// autoSyncBalanceIfNeeded 自动同步余额（每10分钟检查一次，变化>5%才更新）
func (at *AutoTrader) autoSyncBalanceIfNeeded() {
	// 距离上次同步不足10分钟，跳过
	if time.Since(at.lastBalanceSyncTime) < 10*time.Minute {
		return
	}

	log.Printf("🔄 [%s] 开始自动检查余额变化...", at.name)

	// 查询实际余额
	balanceInfo, err := at.trader.GetBalance()
	if err != nil {
		log.Printf("⚠️ [%s] 查询余额失败: %v", at.name, err)
		at.lastBalanceSyncTime = time.Now() // 即使失败也更新时间，避免频繁重试
		return
	}

	// 提取可用余额
	var actualBalance float64
	if availableBalance, ok := balanceInfo["available_balance"].(float64); ok && availableBalance > 0 {
		actualBalance = availableBalance
	} else if availableBalance, ok := balanceInfo["availableBalance"].(float64); ok && availableBalance > 0 {
		actualBalance = availableBalance
	} else if totalBalance, ok := balanceInfo["balance"].(float64); ok && totalBalance > 0 {
		actualBalance = totalBalance
	} else {
		log.Printf("⚠️ [%s] 无法提取可用余额", at.name)
		at.lastBalanceSyncTime = time.Now()
		return
	}

	oldBalance := at.initialBalance

	// 防止除以零：如果初始余额无效，直接更新为实际余额
	if oldBalance <= 0 {
		log.Printf("⚠️ [%s] 初始余额无效 (%.2f)，直接更新为实际余额 %.2f USDT", at.name, oldBalance, actualBalance)
		at.initialBalance = actualBalance
		if at.database != nil {
			type DatabaseUpdater interface {
				UpdateTraderInitialBalance(userID, id string, newBalance float64) error
			}
			if db, ok := at.database.(DatabaseUpdater); ok {
				if err := db.UpdateTraderInitialBalance(at.userID, at.id, actualBalance); err != nil {
					log.Printf("❌ [%s] 更新数据库失败: %v", at.name, err)
				} else {
					log.Printf("✅ [%s] 已自动同步余额到数据库", at.name)
				}
			} else {
				log.Printf("⚠️ [%s] 数据库类型不支持UpdateTraderInitialBalance接口", at.name)
			}
		} else {
			log.Printf("⚠️ [%s] 数据库引用为空，余额仅在内存中更新", at.name)
		}
		at.lastBalanceSyncTime = time.Now()
		return
	}

	changePercent := ((actualBalance - oldBalance) / oldBalance) * 100

	// 变化超过5%才更新
	if math.Abs(changePercent) > 5.0 {
		log.Printf("🔔 [%s] 检测到余额大幅变化: %.2f → %.2f USDT (%.2f%%)",
			at.name, oldBalance, actualBalance, changePercent)

		// 更新内存中的 initialBalance
		at.initialBalance = actualBalance

		// 更新数据库（需要类型断言）
		if at.database != nil {
			// 这里需要根据实际的数据库类型进行类型断言
			// 由于使用了 interface{}，我们需要在 TraderManager 层面处理更新
			// 或者在这里进行类型检查
			type DatabaseUpdater interface {
				UpdateTraderInitialBalance(userID, id string, newBalance float64) error
			}
			if db, ok := at.database.(DatabaseUpdater); ok {
				err := db.UpdateTraderInitialBalance(at.userID, at.id, actualBalance)
				if err != nil {
					log.Printf("❌ [%s] 更新数据库失败: %v", at.name, err)
				} else {
					log.Printf("✅ [%s] 已自动同步余额到数据库", at.name)
				}
			} else {
				log.Printf("⚠️ [%s] 数据库类型不支持UpdateTraderInitialBalance接口", at.name)
			}
		} else {
			log.Printf("⚠️ [%s] 数据库引用为空，余额仅在内存中更新", at.name)
		}
	} else {
		log.Printf("✓ [%s] 余额变化不大 (%.2f%%)，无需更新", at.name, changePercent)
	}

	at.lastBalanceSyncTime = time.Now()
}

// runCycle 运行一个交易周期（使用AI全权决策）
func (at *AutoTrader) runCycle() error {
	at.callCount++

	log.Print("\n" + strings.Repeat("=", 70) + "\n")
	log.Printf("⏰ %s - AI决策周期 #%d", time.Now().Format("2006-01-02 15:04:05"), at.callCount)
	log.Println(strings.Repeat("=", 70))

	// 创建决策记录
	record := &logger.DecisionRecord{
		ExecutionLog: []string{},
		Success:      true,
	}

	// 1. 检查是否需要停止交易
	if time.Now().Before(at.stopUntil) {
		remaining := at.stopUntil.Sub(time.Now())
		log.Printf("⏸ 风险控制：暂停交易中，剩余 %.0f 分钟", remaining.Minutes())
		record.Success = false
		record.ErrorMessage = fmt.Sprintf("风险控制暂停中，剩余 %.0f 分钟", remaining.Minutes())
		at.decisionLogger.LogDecision(record)
		// 推送风险控制暂停信息到Telegram
		at.pushDecisionToTelegram(record)
		return nil
	}

	// 2. 重置日盈亏（每天重置）
	if time.Since(at.lastResetTime) > 24*time.Hour {
		at.dailyPnL = 0
		at.lastResetTime = time.Now()
		log.Println("📅 日盈亏已重置")
	}

	// 3. 自动同步余额（每10分钟检查一次，充值/提现后自动更新）
	at.autoSyncBalanceIfNeeded()

	// 4. 收集交易上下文
	ctx, err := at.buildTradingContext()
	if err != nil {
		record.Success = false
		record.ErrorMessage = fmt.Sprintf("构建交易上下文失败: %v", err)
		at.decisionLogger.LogDecision(record)
		// 推送交易上下文构建失败信息到Telegram
		at.pushDecisionToTelegram(record)
		return fmt.Errorf("构建交易上下文失败: %w", err)
	}

	// 保存账户状态快照
	totalUnrealized := 0.0
	for _, pos := range ctx.Positions {
		totalUnrealized += pos.UnrealizedPnL
	}

	record.AccountState = logger.AccountSnapshot{
		TotalBalance:          ctx.Account.TotalEquity,
		AvailableBalance:      ctx.Account.AvailableBalance,
		TotalUnrealizedProfit: totalUnrealized, // 来自当前持仓的未实现盈亏
		PositionCount:         ctx.Account.PositionCount,
		MarginUsedPct:         ctx.Account.MarginUsedPct,
	}

	// 保存持仓快照
	for _, pos := range ctx.Positions {
		snapshot := logger.PositionSnapshot{
			Symbol:           pos.Symbol,
			Side:             pos.Side,
			PositionAmt:      pos.Quantity,
			EntryPrice:       pos.EntryPrice,
			MarkPrice:        pos.MarkPrice,
			UnrealizedProfit: pos.UnrealizedPnL,
			Leverage:         float64(pos.Leverage),
			LiquidationPrice: pos.LiquidationPrice,
		}

		// 提取止盈止损价格信息
		if pos.BestStopLoss != nil {
			snapshot.StopLossPrice = pos.BestStopLoss.Price
		}
		if pos.BestTakeProfit != nil {
			snapshot.TakeProfitPrice = pos.BestTakeProfit.Price
		}

		record.Positions = append(record.Positions, snapshot)
	}

	log.Print(strings.Repeat("=", 70))
	for _, coin := range ctx.CandidateCoins {
		record.CandidateCoins = append(record.CandidateCoins, coin.Symbol)
	}

	log.Printf("📊 账户净值: %.2f USDT | 可用: %.2f USDT | 持仓: %d",
		ctx.Account.TotalEquity, ctx.Account.AvailableBalance, ctx.Account.PositionCount)

	// 5. 调用AI获取完整决策
	log.Printf("🤖 正在请求AI分析并决策... [模板: %s]", at.systemPromptTemplate)
	decision, err := decision.GetFullDecisionWithCustomPrompt(ctx, at.mcpClient, at.customPrompt, at.overrideBasePrompt, at.systemPromptTemplate)

	// 即使有错误，也保存思维链、决策和输入prompt（用于debug）
	if decision != nil {
		// ⚠️ 关键修复：在存储时就解码Unicode转义序列和HTML实体，避免后续编码问题
		record.SystemPrompt = decodeAllEncodings(decision.SystemPrompt)
		record.InputPrompt = decodeAllEncodings(decision.UserPrompt)
		record.CoTTrace = decodeAllEncodings(decision.CoTTrace)
		record.RawAIResponse = decision.RawResponse
		if len(decision.Decisions) > 0 {
			decisionJSON, _ := json.MarshalIndent(decision.Decisions, "", "  ")
			// 确保JSON在存储时也通过统一解码（Unicode + HTML实体）
			record.DecisionJSON = decodeAllEncodings(string(decisionJSON))
		}
	}

	if err != nil {
		record.Success = false
		record.ErrorMessage = fmt.Sprintf("获取AI决策失败: %v", err)

		// 打印系统提示词和AI思维链（即使有错误，也要输出以便调试）
		if decision != nil {
			log.Print("\n" + strings.Repeat("=", 70) + "\n")
			log.Printf("📋 系统提示词 [模板: %s] (错误情况)", at.systemPromptTemplate)
			log.Println(strings.Repeat("=", 70))
			log.Println(decision.SystemPrompt)
			log.Println(strings.Repeat("=", 70))

			if decision.CoTTrace != "" {
				log.Print("\n" + strings.Repeat("-", 70) + "\n")
				log.Println("💭 AI思维链分析（错误情况）:")
				log.Println(strings.Repeat("-", 70))
				log.Println(decision.CoTTrace)
				log.Println(strings.Repeat("-", 70))
			}
		}

		at.decisionLogger.LogDecision(record)
		// 推送AI决策失败信息到Telegram
		at.pushDecisionToTelegram(record)
		return fmt.Errorf("获取AI决策失败: %w", err)
	}

	// // 5. 打印系统提示词
	// log.Printf("\n" + strings.Repeat("=", 70))
	// log.Printf("📋 系统提示词 [模板: %s]", at.systemPromptTemplate)
	// log.Println(strings.Repeat("=", 70))
	// log.Println(decision.SystemPrompt)
	// log.Printf(strings.Repeat("=", 70) + "\n")

	// 6. 打印AI思维链
	// log.Printf("\n" + strings.Repeat("-", 70))
	// log.Println("💭 AI思维链分析:")
	// log.Println(strings.Repeat("-", 70))
	// log.Println(decision.CoTTrace)
	// log.Printf(strings.Repeat("-", 70) + "\n")

	// 7. 打印AI决策
	// log.Printf("📋 AI决策列表 (%d 个):\n", len(decision.Decisions))
	// for i, d := range decision.Decisions {
	//     log.Printf("  [%d] %s: %s - %s", i+1, d.Symbol, d.Action, d.Reasoning)
	//     if d.Action == "open_long" || d.Action == "open_short" {
	//        log.Printf("      杠杆: %dx | 仓位: %.2f USDT | 止损: %.4f | 止盈: %.4f",
	//           d.Leverage, d.PositionSizeUSD, d.StopLoss, d.TakeProfit)
	//     }
	// }
	log.Println()
	log.Print(strings.Repeat("-", 70))
	// 8. 对决策排序：确保先平仓后开仓（防止仓位叠加超限）
	log.Print(strings.Repeat("-", 70))

	// 8. 对决策排序：确保先平仓后开仓（防止仓位叠加超限）
	sortedDecisions := sortDecisionsByPriority(decision.Decisions)

	log.Println("🔄 执行顺序（已优化）: 先平仓→后开仓")
	for i, d := range sortedDecisions {
		log.Printf("  [%d] %s %s", i+1, d.Symbol, d.Action)
	}
	log.Println()

	// 执行决策并记录结果
	var executionActions []*logger.DecisionAction

	for _, d := range sortedDecisions {
		actionRecord := logger.DecisionAction{
			Action:    d.Action,
			Symbol:    d.Symbol,
			Quantity:  0,
			Leverage:  d.Leverage,
			Price:     0,
			Timestamp: time.Now(),
			Success:   false,
		}

		if err := at.executeDecisionWithRecord(&d, &actionRecord); err != nil {
			log.Printf("❌ 执行决策失败 (%s %s): %v", d.Symbol, d.Action, err)
			actionRecord.Error = err.Error()
			record.ExecutionLog = append(record.ExecutionLog, fmt.Sprintf("❌ %s %s 失败: %v", d.Symbol, d.Action, err))
		} else {
			actionRecord.Success = true
			record.ExecutionLog = append(record.ExecutionLog, fmt.Sprintf("✓ %s %s 成功", d.Symbol, d.Action))
			// 成功执行后短暂延迟
			time.Sleep(1 * time.Second)
		}

		record.Decisions = append(record.Decisions, actionRecord)
		executionActions = append(executionActions, &record.Decisions[len(record.Decisions)-1])
	}

	// 9. 保存决策记录
	if err := at.decisionLogger.LogDecision(record); err != nil {
		log.Printf("⚠ 保存决策记录失败: %v", err)
	}

	// 10. 推送决策到Telegram（仅适用于TG交易员）
	at.pushDecisionToTelegram(record)

	// 11. 推送执行结果到Telegram，确保在AI决策报告之后展示
	for _, action := range executionActions {
		at.pushTradeExecutionToTelegram(action)
	}

	return nil
}

// buildTradingContext 构建交易上下文
func (at *AutoTrader) buildTradingContext() (*decision.Context, error) {
	// 1. 获取账户信息
	balance, err := at.trader.GetBalance()
	if err != nil {
		return nil, fmt.Errorf("获取账户余额失败: %w", err)
	}

	// 获取账户字段
	totalWalletBalance := 0.0
	availableBalance := 0.0

	if wallet, ok := balance["totalWalletBalance"].(float64); ok {
		totalWalletBalance = wallet
	}
	if avail, ok := balance["availableBalance"].(float64); ok {
		availableBalance = avail
	}

	// 修复：totalWalletBalance 已经是正确的总资产值（包含现货+合约净值），不应该再加未实现盈亏
	// 原来的计算逻辑是为了兼容旧的错误数据，现在 totalWalletBalance 已经修复
	totalEquity := totalWalletBalance

	// 2. 获取持仓信息
	positions, err := at.trader.GetPositions()
	if err != nil {
		return nil, fmt.Errorf("获取持仓失败: %w", err)
	}

	var positionInfos []decision.PositionInfo
	totalMarginUsed := 0.0

	// 当前持仓的key集合（用于清理已平仓的记录）
	currentPositionKeys := make(map[string]bool)

	for _, pos := range positions {
		symbol := pos["symbol"].(string)
		side := pos["side"].(string)
		entryPrice := pos["entryPrice"].(float64)
		markPrice := pos["markPrice"].(float64)
		quantity := pos["positionAmt"].(float64)
		if quantity < 0 {
			quantity = -quantity // 空仓数量为负，转为正数
		}

		// 跳过已平仓的持仓（quantity = 0），防止"幽灵持仓"传递给AI
		if quantity == 0 {
			continue
		}

		unrealizedPnl := pos["unRealizedProfit"].(float64)
		liquidationPrice := pos["liquidationPrice"].(float64)

		// 计算盈亏百分比
		pnlPct := 0.0
		if side == "long" {
			pnlPct = ((markPrice - entryPrice) / entryPrice) * 100
		} else {
			pnlPct = ((entryPrice - markPrice) / entryPrice) * 100
		}

		// 计算占用保证金（估算）
		leverage := 10 // 默认值，实际应该从持仓信息获取
		if lev, ok := pos["leverage"].(float64); ok {
			leverage = int(lev)
		}
		marginUsed := (quantity * markPrice) / float64(leverage)
		totalMarginUsed += marginUsed

		// 把当前币种的止盈/止损传递给AI（选择距离当前价格最近的一个）
		var bestSL *decision.TpSlOrderInfo
		var bestTP *decision.TpSlOrderInfo
		if hlTrader, ok := at.trader.(*HyperliquidTrader); ok {
			if triggerOrders, err := hlTrader.ListActiveTpSlOrders(symbol); err != nil {
				log.Printf("⚠️ [%s] 获取触发挂单失败: %v", symbol, err)
			} else if len(triggerOrders) > 0 {
				bestSL, bestTP = pickBestTpSlOrders(triggerOrders, side, markPrice)
			}
		}

		// 跟踪持仓首次出现时间
		posKey := symbol + "_" + side
		currentPositionKeys[posKey] = true
		if _, exists := at.positionFirstSeenTime[posKey]; !exists {
			// 新持仓，记录当前时间
			at.positionFirstSeenTime[posKey] = time.Now().UnixMilli()
		}
		updateTime := at.positionFirstSeenTime[posKey]

		positionInfos = append(positionInfos, decision.PositionInfo{
			Symbol:           symbol,
			Side:             side,
			EntryPrice:       entryPrice,
			MarkPrice:        markPrice,
			Quantity:         quantity,
			Leverage:         leverage,
			UnrealizedPnL:    unrealizedPnl,
			UnrealizedPnLPct: pnlPct,
			LiquidationPrice: liquidationPrice,
			MarginUsed:       marginUsed,
			UpdateTime:       updateTime,
			BestStopLoss:     bestSL,
			BestTakeProfit:   bestTP,
		})
	}

	// 清理已平仓的持仓记录
	for key := range at.positionFirstSeenTime {
		if !currentPositionKeys[key] {
			delete(at.positionFirstSeenTime, key)
		}
	}

	// 3. 获取交易员的候选币种池
	candidateCoins, err := at.getCandidateCoins()
	if err != nil {
		return nil, fmt.Errorf("获取候选币种失败: %w", err)
	}

	// 4. 计算总盈亏
	totalPnL := totalEquity - at.userInitialBalance
	totalPnLPct := 0.0

	// 添加边界检查和验证
	if at.userInitialBalance <= 0 {
		// 用户初始余额无效，记录警告但避免除零错误
		log.Printf("⚠️ [AccountInfo] 用户初始余额无效 (trader=%s user_initial=%.2f)", at.name, at.userInitialBalance)
		totalPnL = totalEquity
		totalPnLPct = 0.0
	} else {
		// 正常计算收益率
		totalPnLPct = (totalPnL / at.userInitialBalance) * 100

		// 边界检查：异常高的收益率可能表示数据错误
		if math.Abs(totalPnLPct) > 10000 { // 10000% = 100倍收益
			log.Printf("🚨 [AccountInfo] 异常高收益率检测 (trader=%s pnl_pct=%.2f%% user_initial=%.2f pnl=%.2f)",
				at.name, totalPnLPct, at.userInitialBalance, totalPnL)
			// 可以选择限制最大收益率，但暂时记录日志不修改数值
		}
	}

	marginUsedPct := 0.0
	if totalEquity > 0 {
		marginUsedPct = (totalMarginUsed / totalEquity) * 100
	}

	// 5. 分析历史表现（最近100个周期，避免长期持仓的交易记录丢失）
	// 假设每3分钟一个周期，100个周期 = 5小时，足够覆盖大部分交易
	performance, err := at.decisionLogger.AnalyzePerformance(100)
	if err != nil {
		log.Printf("⚠️  分析历史表现失败: %v", err)
		// 不影响主流程，继续执行（但设置performance为nil以避免传递错误数据）
		performance = nil
	}

	// 6. 构建上下文
	ctx := &decision.Context{
		CurrentTime:     time.Now().Format("2006-01-02 15:04:05"),
		RuntimeMinutes:  int(time.Since(at.startTime).Minutes()),
		CallCount:       at.callCount,
		BTCETHLeverage:  at.config.BTCETHLeverage,  // 使用配置的杠杆倍数
		AltcoinLeverage: at.config.AltcoinLeverage, // 使用配置的杠杆倍数
		Account: decision.AccountInfo{
			TotalEquity:      totalEquity,
			AvailableBalance: availableBalance,
			TotalPnL:         totalPnL,
			TotalPnLPct:      totalPnLPct,
			MarginUsed:       totalMarginUsed,
			MarginUsedPct:    marginUsedPct,
			PositionCount:    len(positionInfos),
		},
		Positions:      positionInfos,
		CandidateCoins: candidateCoins,
		Performance:    performance, // 添加历史表现分析
	}

	return ctx, nil
}

// pickBestTpSlOrders 从触发挂单中选出距离当前价格最近的止盈/止损单
func pickBestTpSlOrders(orders []hyperliquid.FrontendOpenOrder, positionSide string, markPrice float64) (*decision.TpSlOrderInfo, *decision.TpSlOrderInfo) {
	var bestSL *decision.TpSlOrderInfo
	var bestTP *decision.TpSlOrderInfo

	for _, ord := range orders {
		kind := classifyTpSl(ord, positionSide)
		if kind != "tp" && kind != "sl" {
			continue
		}

		price := ord.TriggerPx
		if price == 0 && ord.LimitPx > 0 {
			price = ord.LimitPx
		}
		if price == 0 {
			continue
		}

		info := &decision.TpSlOrderInfo{
			OrderID:          ord.Oid,
			Kind:             kind,
			Price:            price,
			TriggerCondition: ord.TriggerCondition,
			ReduceOnly:       ord.ReduceOnly,
		}

		dist := math.Abs(price - markPrice)
		if kind == "tp" {
			if bestTP == nil || math.Abs(bestTP.Price-markPrice) > dist {
				bestTP = info
			}
		} else {
			if bestSL == nil || math.Abs(bestSL.Price-markPrice) > dist {
				bestSL = info
			}
		}
	}

	return bestSL, bestTP
}

// executeDecisionWithRecord 执行AI决策并记录详细信息
func (at *AutoTrader) executeDecisionWithRecord(decision *decision.Decision, actionRecord *logger.DecisionAction) error {
	// 反向交易逻辑：配置启用ReverseTrading时执行反向交易
	if at.reverseTrading {
		switch decision.Action {
		case "open_long":
			log.Printf("[REVERSE TRADING] %s: AI建议开多，但配置启用反向交易，执行开空", at.name)
			// 反向交易时需要反转止损止盈价格
			originalStopLoss := decision.StopLoss
			originalTakeProfit := decision.TakeProfit
			decision.StopLoss = originalTakeProfit // 止损变为止盈
			decision.TakeProfit = originalStopLoss // 止盈变为止损
			return at.executeOpenShortWithRecord(decision, actionRecord)
		case "open_short":
			log.Printf("[REVERSE TRADING] %s: AI建议开空，但配置启用反向交易，执行开多", at.name)
			// 反向交易时需要反转止损止盈价格
			originalStopLoss := decision.StopLoss
			originalTakeProfit := decision.TakeProfit
			decision.StopLoss = originalTakeProfit // 止损变为止盈
			decision.TakeProfit = originalStopLoss // 止盈变为止损
			return at.executeOpenLongWithRecord(decision, actionRecord)
		// close操作保持不变，不进行反向
		case "close_long":
			return at.executeCloseLongWithRecord(decision, actionRecord)
		case "close_short":
			return at.executeCloseShortWithRecord(decision, actionRecord)
		case "update_stop_loss":
			return at.executeUpdateStopLossWithRecord(decision, actionRecord)
		case "update_take_profit":
			return at.executeUpdateTakeProfitWithRecord(decision, actionRecord)
		case "partial_close":
			return at.executePartialCloseWithRecord(decision, actionRecord)
		case "hold", "wait":
			// 无需执行，仅记录
			return nil
		default:
			return fmt.Errorf("未知的决策类型: %s", decision.Action)
		}
	}

	// 正常交易逻辑
	switch decision.Action {
	case "open_long":
		return at.executeOpenLongWithRecord(decision, actionRecord)
	case "open_short":
		return at.executeOpenShortWithRecord(decision, actionRecord)
	case "close_long":
		return at.executeCloseLongWithRecord(decision, actionRecord)
	case "close_short":
		return at.executeCloseShortWithRecord(decision, actionRecord)
	case "update_stop_loss":
		return at.executeUpdateStopLossWithRecord(decision, actionRecord)
	case "update_take_profit":
		return at.executeUpdateTakeProfitWithRecord(decision, actionRecord)
	case "partial_close":
		return at.executePartialCloseWithRecord(decision, actionRecord)
	case "hold", "wait":
		// 无需执行，仅记录
		return nil
	default:
		return fmt.Errorf("未知的action: %s", decision.Action)
	}
}

// executeOpenLongWithRecord 执行开多仓并记录详细信息
func (at *AutoTrader) executeOpenLongWithRecord(decision *decision.Decision, actionRecord *logger.DecisionAction) error {
	log.Printf("  📈 开多仓: %s", decision.Symbol)

	// ⚠️ 关键：检查是否已有同币种同方向持仓，如果有则拒绝开仓（防止仓位叠加超限）
	positions, err := at.trader.GetPositions()
	if err == nil {
		for _, pos := range positions {
			if pos["symbol"] == decision.Symbol && pos["side"] == "long" {
				return fmt.Errorf("❌ %s 已有多仓，拒绝开仓以防止仓位叠加超限。如需换仓，请先给出 close_long 决策", decision.Symbol)
			}
		}
	}

	// 获取当前价格
	marketData, err := market.Get(decision.Symbol)
	if err != nil {
		return err
	}

	// 计算数量
	quantity := decision.PositionSizeUSD / marketData.CurrentPrice
	actionRecord.Quantity = quantity
	actionRecord.Price = marketData.CurrentPrice
	if decision.StopLoss > 0 {
		sl := decision.StopLoss
		actionRecord.StopLoss = &sl
	}
	if decision.TakeProfit > 0 {
		tp := decision.TakeProfit
		actionRecord.TakeProfit = &tp
	}

	// ⚠️ 保证金验证：防止保证金不足错误（code=-2019）
	requiredMargin := decision.PositionSizeUSD / float64(decision.Leverage)

	balance, err := at.trader.GetBalance()
	if err != nil {
		return fmt.Errorf("获取账户余额失败: %w", err)
	}
	availableBalance := 0.0
	if avail, ok := balance["availableBalance"].(float64); ok {
		availableBalance = avail
	}

	// 手续费估算（Taker费率 0.04%）
	estimatedFee := decision.PositionSizeUSD * 0.0004
	totalRequired := requiredMargin + estimatedFee

	if totalRequired > availableBalance {
		return fmt.Errorf("❌ 保证金不足: 需要 %.2f USDT（保证金 %.2f + 手续费 %.2f），可用 %.2f USDT",
			totalRequired, requiredMargin, estimatedFee, availableBalance)
	}

	// 设置仓位模式
	formattedSymbol := at.formatSymbolForExchange(decision.Symbol)
	if err := at.trader.SetMarginMode(formattedSymbol, at.config.IsCrossMargin); err != nil {
		log.Printf("  ⚠️ 设置仓位模式失败: %v", err)
		// 继续执行，不影响交易
	}

	// 开仓
	order, err := at.trader.OpenLong(formattedSymbol, quantity, decision.Leverage)
	if err != nil {
		return err
	}

	// 记录订单ID
	if orderID, ok := order["orderId"].(int64); ok {
		actionRecord.OrderID = orderID
	}

	log.Printf("  ✓ 开仓成功，订单ID: %v, 数量: %.4f", order["orderId"], quantity)

	// 记录开仓时间
	posKey := decision.Symbol + "_long"
	at.positionFirstSeenTime[posKey] = time.Now().UnixMilli()

	// 设置止损止盈
	if err := at.trader.SetStopLoss(formattedSymbol, "LONG", quantity, decision.StopLoss); err != nil {
		log.Printf("  ⚠ 设置止损失败: %v", err)
	}
	if err := at.trader.SetTakeProfit(formattedSymbol, "LONG", quantity, decision.TakeProfit); err != nil {
		log.Printf("  ⚠ 设置止盈失败: %v", err)
	}

	return nil
}

// executeOpenShortWithRecord 执行开空仓并记录详细信息
func (at *AutoTrader) executeOpenShortWithRecord(decision *decision.Decision, actionRecord *logger.DecisionAction) error {
	log.Printf("  📉 开空仓: %s", decision.Symbol)

	// ⚠️ 关键：检查是否已有同币种同方向持仓，如果有则拒绝开仓（防止仓位叠加超限）
	positions, err := at.trader.GetPositions()
	if err == nil {
		for _, pos := range positions {
			if pos["symbol"] == decision.Symbol && pos["side"] == "short" {
				return fmt.Errorf("❌ %s 已有空仓，拒绝开仓以防止仓位叠加超限。如需换仓，请先给出 close_short 决策", decision.Symbol)
			}
		}
	}

	// 获取当前价格
	marketData, err := market.Get(decision.Symbol)
	if err != nil {
		return err
	}

	// 计算数量
	quantity := decision.PositionSizeUSD / marketData.CurrentPrice
	actionRecord.Quantity = quantity
	actionRecord.Price = marketData.CurrentPrice
	if decision.StopLoss > 0 {
		sl := decision.StopLoss
		actionRecord.StopLoss = &sl
	}
	if decision.TakeProfit > 0 {
		tp := decision.TakeProfit
		actionRecord.TakeProfit = &tp
	}

	// ⚠️ 保证金验证：防止保证金不足错误（code=-2019）
	requiredMargin := decision.PositionSizeUSD / float64(decision.Leverage)

	balance, err := at.trader.GetBalance()
	if err != nil {
		return fmt.Errorf("获取账户余额失败: %w", err)
	}
	availableBalance := 0.0
	if avail, ok := balance["availableBalance"].(float64); ok {
		availableBalance = avail
	}

	// 手续费估算（Taker费率 0.04%）
	estimatedFee := decision.PositionSizeUSD * 0.0004
	totalRequired := requiredMargin + estimatedFee

	if totalRequired > availableBalance {
		return fmt.Errorf("❌ 保证金不足: 需要 %.2f USDT（保证金 %.2f + 手续费 %.2f），可用 %.2f USDT",
			totalRequired, requiredMargin, estimatedFee, availableBalance)
	}

	// 设置仓位模式
	formattedSymbol := at.formatSymbolForExchange(decision.Symbol)
	if err := at.trader.SetMarginMode(formattedSymbol, at.config.IsCrossMargin); err != nil {
		log.Printf("  ⚠️ 设置仓位模式失败: %v", err)
		// 继续执行，不影响交易
	}

	// 开仓
	order, err := at.trader.OpenShort(formattedSymbol, quantity, decision.Leverage)
	if err != nil {
		return err
	}

	// 记录订单ID
	if orderID, ok := order["orderId"].(int64); ok {
		actionRecord.OrderID = orderID
	}

	log.Printf("  ✓ 开仓成功，订单ID: %v, 数量: %.4f", order["orderId"], quantity)

	// 记录开仓时间
	posKey := decision.Symbol + "_short"
	at.positionFirstSeenTime[posKey] = time.Now().UnixMilli()

	// 设置止损止盈
	if err := at.trader.SetStopLoss(formattedSymbol, "SHORT", quantity, decision.StopLoss); err != nil {
		log.Printf("  ⚠ 设置止损失败: %v", err)
	}
	if err := at.trader.SetTakeProfit(formattedSymbol, "SHORT", quantity, decision.TakeProfit); err != nil {
		log.Printf("  ⚠ 设置止盈失败: %v", err)
	}

	return nil
}

// executeCloseLongWithRecord 执行平多仓并记录详细信息
func (at *AutoTrader) executeCloseLongWithRecord(decision *decision.Decision, actionRecord *logger.DecisionAction) error {
	// 处理 decision 为 nil 的情况，使用 actionRecord.Symbol
	var symbol string
	if decision != nil {
		symbol = decision.Symbol
	} else {
		symbol = actionRecord.Symbol
	}

	log.Printf("  🔄 平多仓: %s", symbol)

	// 获取当前价格
	marketData, err := market.Get(symbol)
	if err != nil {
		return err
	}
	actionRecord.Price = marketData.CurrentPrice

	// 记录持仓均价与数量（用于计算平仓盈亏）
	var entryPrice float64
	var positionAmt float64
	positionsForPnl, err := at.trader.GetPositions()
	if err == nil {
		for _, pos := range positionsForPnl {
			if pos["symbol"] == symbol && pos["side"] == "long" {
				entryPrice = pos["entryPrice"].(float64)
				positionAmt = pos["positionAmt"].(float64)
				if positionAmt < 0 {
					positionAmt = -positionAmt
				}
				break
			}
		}
	}

	// 平仓
	formattedSymbol := at.formatSymbolForExchange(symbol)
	order, err := at.trader.CloseLong(formattedSymbol, 0) // 0 = 全部平仓
	if err != nil {
		return err
	}

	// 尝试计算本次平仓盈亏（基于持仓均价和当前价格）
	if marketData.CurrentPrice > 0 && entryPrice > 0 && positionAmt > 0 {
		actionRecord.Quantity = positionAmt
		actionRecord.Profit = (marketData.CurrentPrice - entryPrice) * positionAmt
	}

	// 记录订单ID
	if orderID, ok := order["orderId"].(int64); ok {
		actionRecord.OrderID = orderID
	}

	log.Printf("  ✓ 平仓成功")
	return nil
}

// executeCloseShortWithRecord 执行平空仓并记录详细信息
func (at *AutoTrader) executeCloseShortWithRecord(decision *decision.Decision, actionRecord *logger.DecisionAction) error {
	// 处理 decision 为 nil 的情况，使用 actionRecord.Symbol
	var symbol string
	if decision != nil {
		symbol = decision.Symbol
	} else {
		symbol = actionRecord.Symbol
	}

	log.Printf("  🔄 平空仓: %s", symbol)

	// 获取当前价格
	marketData, err := market.Get(symbol)
	if err != nil {
		return err
	}
	actionRecord.Price = marketData.CurrentPrice

	// 记录持仓均价与数量（用于计算平仓盈亏）
	var entryPrice float64
	var positionAmt float64
	positionsForPnl, err := at.trader.GetPositions()
	if err == nil {
		for _, pos := range positionsForPnl {
			if pos["symbol"] == symbol && pos["side"] == "short" {
				entryPrice = pos["entryPrice"].(float64)
				positionAmt = pos["positionAmt"].(float64)
				if positionAmt < 0 {
					positionAmt = -positionAmt
				}
				break
			}
		}
	}

	// 平仓
	formattedSymbol := at.formatSymbolForExchange(symbol)
	order, err := at.trader.CloseShort(formattedSymbol, 0) // 0 = 全部平仓
	if err != nil {
		return err
	}

	// 尝试计算本次平仓盈亏
	if marketData.CurrentPrice > 0 && entryPrice > 0 && positionAmt > 0 {
		actionRecord.Quantity = positionAmt
		actionRecord.Profit = (entryPrice - marketData.CurrentPrice) * positionAmt
	}

	// 记录订单ID
	if orderID, ok := order["orderId"].(int64); ok {
		actionRecord.OrderID = orderID
	}

	log.Printf("  ✓ 平仓成功")
	return nil
}

// executeUpdateStopLossWithRecord 执行调整止损并记录详细信息
func (at *AutoTrader) executeUpdateStopLossWithRecord(decision *decision.Decision, actionRecord *logger.DecisionAction) error {
	log.Printf("  🎯 调整止损: %s → %.2f", decision.Symbol, decision.NewStopLoss)
	actionRecord.StopLoss = &decision.NewStopLoss

	// 获取当前价格
	marketData, err := market.Get(decision.Symbol)
	if err != nil {
		return err
	}
	actionRecord.Price = marketData.CurrentPrice

	// 获取当前持仓
	positions, err := at.trader.GetPositions()
	if err != nil {
		return fmt.Errorf("获取持仓失败: %w", err)
	}

	// 查找目标持仓
	var targetPosition map[string]interface{}
	for _, pos := range positions {
		symbol, _ := pos["symbol"].(string)
		posAmt, _ := pos["positionAmt"].(float64)
		if symbol == decision.Symbol && posAmt != 0 {
			targetPosition = pos
			break
		}
	}

	if targetPosition == nil {
		return fmt.Errorf("持仓不存在: %s", decision.Symbol)
	}

	// 获取持仓方向和数量
	side, _ := targetPosition["side"].(string)
	positionSide := strings.ToUpper(side)
	positionAmt, _ := targetPosition["positionAmt"].(float64)

	// 验证新止损价格合理性
	if positionSide == "LONG" && decision.NewStopLoss >= marketData.CurrentPrice {
		return fmt.Errorf("多单止损必须低于当前价格 (当前: %.2f, 新止损: %.2f)", marketData.CurrentPrice, decision.NewStopLoss)
	}
	if positionSide == "SHORT" && decision.NewStopLoss <= marketData.CurrentPrice {
		return fmt.Errorf("空单止损必须高于当前价格 (当前: %.2f, 新止损: %.2f)", marketData.CurrentPrice, decision.NewStopLoss)
	}

	// ⚠️ 防御性检查：检测是否存在双向持仓（不应该出现，但提供保护）
	var hasOppositePosition bool
	oppositeSide := ""
	for _, pos := range positions {
		symbol, _ := pos["symbol"].(string)
		posSide, _ := pos["side"].(string)
		posAmt, _ := pos["positionAmt"].(float64)
		if symbol == decision.Symbol && posAmt != 0 && strings.ToUpper(posSide) != positionSide {
			hasOppositePosition = true
			oppositeSide = strings.ToUpper(posSide)
			break
		}
	}

	if hasOppositePosition {
		log.Printf("  🚨 警告：检测到 %s 存在双向持仓（%s + %s），这违反了策略规则",
			decision.Symbol, positionSide, oppositeSide)
		log.Printf("  🚨 取消止损单将影响两个方向的订单，请检查是否为用户手动操作导致")
		log.Printf("  🚨 建议：手动平掉其中一个方向的持仓，或检查系统是否有BUG")
	}

	// 格式化符号
	formattedSymbol := at.formatSymbolForExchange(decision.Symbol)

	// 取消旧的止损单（只删除止损单，不影响止盈单）
	// 注意：如果存在双向持仓，这会删除两个方向的止损单
	if err := at.trader.CancelStopLossOrders(formattedSymbol); err != nil {
		log.Printf("  ⚠ 取消旧止损单失败: %v", err)
		// 不中断执行，继续设置新止损
	}

	// 调用交易所 API 修改止损
	quantity := math.Abs(positionAmt)
	err = at.trader.SetStopLoss(formattedSymbol, positionSide, quantity, decision.NewStopLoss)
	if err != nil {
		return fmt.Errorf("修改止损失败: %w", err)
	}

	log.Printf("  ✓ 止损已调整: %.2f (当前价格: %.2f)", decision.NewStopLoss, marketData.CurrentPrice)
	return nil
}

// executeUpdateTakeProfitWithRecord 执行调整止盈并记录详细信息
func (at *AutoTrader) executeUpdateTakeProfitWithRecord(decision *decision.Decision, actionRecord *logger.DecisionAction) error {
	log.Printf("  🎯 调整止盈: %s → %.2f", decision.Symbol, decision.NewTakeProfit)
	actionRecord.TakeProfit = &decision.NewTakeProfit

	// 获取当前价格
	marketData, err := market.Get(decision.Symbol)
	if err != nil {
		return err
	}
	actionRecord.Price = marketData.CurrentPrice

	// 获取当前持仓
	positions, err := at.trader.GetPositions()
	if err != nil {
		return fmt.Errorf("获取持仓失败: %w", err)
	}

	// 查找目标持仓
	var targetPosition map[string]interface{}
	for _, pos := range positions {
		symbol, _ := pos["symbol"].(string)
		posAmt, _ := pos["positionAmt"].(float64)
		if symbol == decision.Symbol && posAmt != 0 {
			targetPosition = pos
			break
		}
	}

	if targetPosition == nil {
		return fmt.Errorf("持仓不存在: %s", decision.Symbol)
	}

	// 获取持仓方向和数量
	side, _ := targetPosition["side"].(string)
	positionSide := strings.ToUpper(side)
	positionAmt, _ := targetPosition["positionAmt"].(float64)

	// 验证新止盈价格合理性
	if positionSide == "LONG" && decision.NewTakeProfit <= marketData.CurrentPrice {
		return fmt.Errorf("多单止盈必须高于当前价格 (当前: %.2f, 新止盈: %.2f)", marketData.CurrentPrice, decision.NewTakeProfit)
	}
	if positionSide == "SHORT" && decision.NewTakeProfit >= marketData.CurrentPrice {
		return fmt.Errorf("空单止盈必须低于当前价格 (当前: %.2f, 新止盈: %.2f)", marketData.CurrentPrice, decision.NewTakeProfit)
	}

	// ⚠️ 防御性检查：检测是否存在双向持仓（不应该出现，但提供保护）
	var hasOppositePosition bool
	oppositeSide := ""
	for _, pos := range positions {
		symbol, _ := pos["symbol"].(string)
		posSide, _ := pos["side"].(string)
		posAmt, _ := pos["positionAmt"].(float64)
		if symbol == decision.Symbol && posAmt != 0 && strings.ToUpper(posSide) != positionSide {
			hasOppositePosition = true
			oppositeSide = strings.ToUpper(posSide)
			break
		}
	}

	if hasOppositePosition {
		log.Printf("  🚨 警告：检测到 %s 存在双向持仓（%s + %s），这违反了策略规则",
			decision.Symbol, positionSide, oppositeSide)
		log.Printf("  🚨 取消止盈单将影响两个方向的订单，请检查是否为用户手动操作导致")
		log.Printf("  🚨 建议：手动平掉其中一个方向的持仓，或检查系统是否有BUG")
	}

	// 格式化符号
	formattedSymbol := at.formatSymbolForExchange(decision.Symbol)

	// 取消旧的止盈单（只删除止盈单，不影响止损单）
	// 注意：如果存在双向持仓，这会删除两个方向的止盈单
	if err := at.trader.CancelTakeProfitOrders(formattedSymbol); err != nil {
		log.Printf("  ⚠ 取消旧止盈单失败: %v", err)
		// 不中断执行，继续设置新止盈
	}

	// 调用交易所 API 修改止盈
	quantity := math.Abs(positionAmt)
	err = at.trader.SetTakeProfit(formattedSymbol, positionSide, quantity, decision.NewTakeProfit)
	if err != nil {
		return fmt.Errorf("修改止盈失败: %w", err)
	}

	log.Printf("  ✓ 止盈已调整: %.2f (当前价格: %.2f)", decision.NewTakeProfit, marketData.CurrentPrice)
	return nil
}

// executePartialCloseWithRecord 执行部分平仓并记录详细信息
func (at *AutoTrader) executePartialCloseWithRecord(decision *decision.Decision, actionRecord *logger.DecisionAction) error {
	log.Printf("  📊 部分平仓: %s %.1f%%", decision.Symbol, decision.ClosePercentage)

	// 验证百分比范围
	if decision.ClosePercentage <= 0 || decision.ClosePercentage > 100 {
		return fmt.Errorf("平仓百分比必须在 0-100 之间，当前: %.1f", decision.ClosePercentage)
	}

	// 获取当前价格
	marketData, err := market.Get(decision.Symbol)
	if err != nil {
		return err
	}
	actionRecord.Price = marketData.CurrentPrice

	// 获取当前持仓
	positions, err := at.trader.GetPositions()
	if err != nil {
		return fmt.Errorf("获取持仓失败: %w", err)
	}

	// 查找目标持仓
	var targetPosition map[string]interface{}
	for _, pos := range positions {
		symbol, _ := pos["symbol"].(string)
		posAmt, _ := pos["positionAmt"].(float64)
		if symbol == decision.Symbol && posAmt != 0 {
			targetPosition = pos
			break
		}
	}

	if targetPosition == nil {
		return fmt.Errorf("持仓不存在: %s", decision.Symbol)
	}

	// 获取持仓方向和数量
	side, _ := targetPosition["side"].(string)
	positionSide := strings.ToUpper(side)
	positionAmt, _ := targetPosition["positionAmt"].(float64)

	// 计算平仓数量
	totalQuantity := math.Abs(positionAmt)
	closeQuantity := totalQuantity * (decision.ClosePercentage / 100.0)
	actionRecord.Quantity = closeQuantity

	// 执行平仓
	var order map[string]interface{}
	formattedSymbol := at.formatSymbolForExchange(decision.Symbol)
	if positionSide == "LONG" {
		order, err = at.trader.CloseLong(formattedSymbol, closeQuantity)
	} else {
		order, err = at.trader.CloseShort(formattedSymbol, closeQuantity)
	}

	if err != nil {
		return fmt.Errorf("部分平仓失败: %w", err)
	}

	// 记录订单ID
	if orderID, ok := order["orderId"].(int64); ok {
		actionRecord.OrderID = orderID
	}

	remainingQuantity := totalQuantity - closeQuantity
	log.Printf("  ✓ 部分平仓成功: 平仓 %.4f (%.1f%%), 剩余 %.4f",
		closeQuantity, decision.ClosePercentage, remainingQuantity)

	return nil
}

// GetID 获取trader ID
func (at *AutoTrader) GetID() string {
	return at.id
}

// GetName 获取trader名称
func (at *AutoTrader) GetName() string {
	return at.name
}

// GetAIModel 获取AI模型
func (at *AutoTrader) GetAIModel() string {
	return at.aiModel
}

// GetExchange 获取交易所
func (at *AutoTrader) GetExchange() string {
	return at.exchange
}

// UpdateAIConfig 动态更新AI提供商、模型与密钥
func (at *AutoTrader) UpdateAIConfig(provider string, apiKey string, modelName string) {
	normalized := strings.ToLower(strings.TrimSpace(provider))
	if normalized == "" {
		normalized = "deepseek"
	}

	modelName = strings.TrimSpace(modelName)
	at.config.CustomModelName = modelName

	if normalized == "qwen" {
		at.config.UseQwen = true
		at.config.AIModel = "qwen"
		at.config.QwenKey = apiKey
		at.config.DeepSeekKey = ""
		at.aiModel = "qwen"
		if at.mcpClient != nil {
			at.mcpClient.SetQwenAPIKey(apiKey, at.config.CustomAPIURL, at.config.CustomModelName)
		}
		log.Printf("🔁 [%s] AI 配置已更新为 Qwen", at.name)
		return
	}

	at.config.UseQwen = false
	at.config.AIModel = "deepseek"
	at.config.DeepSeekKey = apiKey
	at.config.QwenKey = ""
	at.aiModel = "deepseek"
	if at.mcpClient != nil {
		at.mcpClient.SetDeepSeekAPIKey(apiKey, at.config.CustomAPIURL, at.config.CustomModelName)
	}
	log.Printf("🔁 [%s] AI 配置已更新为 DeepSeek", at.name)
}

// SetCustomPrompt 设置自定义交易策略prompt
func (at *AutoTrader) SetCustomPrompt(prompt string) {
	at.customPrompt = prompt
}

// SetOverrideBasePrompt 设置是否覆盖基础prompt
func (at *AutoTrader) SetOverrideBasePrompt(override bool) {
	at.overrideBasePrompt = override
}

// IsRunning 检查交易员是否正在运行
func (at *AutoTrader) IsRunning() bool {
	return at.isRunning
}

// GetStartTime 获取交易员启动时间
func (at *AutoTrader) GetStartTime() time.Time {
	return at.startTime
}

// GetCurrentBalance 获取当前余额（简化实现，返回0表示运行中）
func (at *AutoTrader) GetCurrentBalance() float64 {
	// 检查trader是否可用
	if at.trader == nil {
		return 0
	}

	// 获取余额信息
	balanceInfo, err := at.trader.GetBalance()
	if err != nil {
		return 0
	}

	// 提取可用余额，使用与AutoTrader启动时相同的解析逻辑
	if availableBalance, ok := balanceInfo["available_balance"].(float64); ok && availableBalance > 0 {
		return availableBalance
	} else if availableBalance, ok := balanceInfo["availableBalance"].(float64); ok && availableBalance > 0 {
		return availableBalance
	} else if totalBalance, ok := balanceInfo["balance"].(float64); ok && totalBalance > 0 {
		return totalBalance
	} else if totalEquity, ok := balanceInfo["total_equity"].(float64); ok && totalEquity > 0 {
		return totalEquity
	} else if accountValue, ok := balanceInfo["accountValue"].(float64); ok && accountValue > 0 {
		return accountValue
	}

	return 0
}

// GetPositionsCount 获取持仓数量（简化实现，返回-1表示不可用）
func (at *AutoTrader) GetPositionsCount() int {
	// 简化实现，实际项目中可以通过交易器接口获取真实持仓数量
	return -1
}

// SetSystemPromptTemplate 设置系统提示词模板
func (at *AutoTrader) SetSystemPromptTemplate(templateName string) {
	at.systemPromptTemplate = templateName
}

// GetSystemPromptTemplate 获取当前系统提示词模板名称
func (at *AutoTrader) GetSystemPromptTemplate() string {
	return at.systemPromptTemplate
}

// GetDecisionLogger 获取决策日志记录器
func (at *AutoTrader) GetDecisionLogger() *logger.DecisionLogger {
	return at.decisionLogger
}

// GetStatus 获取系统状态（用于API）
func (at *AutoTrader) GetStatus() map[string]interface{} {
	aiProvider := "DeepSeek"
	if at.config.UseQwen {
		aiProvider = "Qwen"
	}

	return map[string]interface{}{
		"trader_id":       at.id,
		"trader_name":     at.name,
		"ai_model":        at.aiModel,
		"exchange":        at.exchange,
		"is_running":      at.isRunning,
		"start_time":      at.startTime.Format(time.RFC3339),
		"runtime_minutes": int(time.Since(at.startTime).Minutes()),
		"call_count":      at.callCount,
		"initial_balance": at.initialBalance,
		"scan_interval":   at.config.ScanInterval.String(),
		"stop_until":      at.stopUntil.Format(time.RFC3339),
		"last_reset_time": at.lastResetTime.Format(time.RFC3339),
		"ai_provider":     aiProvider,
	}
}

// GetAccountInfo 获取账户信息（用于API）
func (at *AutoTrader) GetAccountInfo() (map[string]interface{}, error) {
	balance, err := at.trader.GetBalance()
	if err != nil {
		return nil, fmt.Errorf("获取余额失败: %w", err)
	}

	// 获取账户字段
	totalWalletBalance := 0.0
	totalUnrealizedProfit := 0.0
	availableBalance := 0.0

	if wallet, ok := balance["totalWalletBalance"].(float64); ok {
		totalWalletBalance = wallet
	}
	if unrealized, ok := balance["totalUnrealizedProfit"].(float64); ok {
		totalUnrealizedProfit = unrealized
	}
	if avail, ok := balance["availableBalance"].(float64); ok {
		availableBalance = avail
	}

	// 修复：totalWalletBalance 已经是正确的总资产值，不需要再加未实现盈亏
	totalEquity := totalWalletBalance

	// 获取持仓计算总保证金
	positions, err := at.trader.GetPositions()
	if err != nil {
		return nil, fmt.Errorf("获取持仓失败: %w", err)
	}

	totalMarginUsed := 0.0
	totalUnrealizedPnL := 0.0
	for _, pos := range positions {
		markPrice := pos["markPrice"].(float64)
		quantity := pos["positionAmt"].(float64)
		if quantity < 0 {
			quantity = -quantity
		}
		unrealizedPnl := pos["unRealizedProfit"].(float64)
		totalUnrealizedPnL += unrealizedPnl

		leverage := 10
		if lev, ok := pos["leverage"].(float64); ok {
			leverage = int(lev)
		}
		marginUsed := (quantity * markPrice) / float64(leverage)
		totalMarginUsed += marginUsed
	}

	totalPnL := totalEquity - at.userInitialBalance
	totalPnLPct := 0.0

	// 添加边界检查和验证
	if at.userInitialBalance <= 0 {
		// 用户初始余额无效，记录警告但避免除零错误
		log.Printf("⚠️ [AccountInfo] 用户初始余额无效 (trader=%s user_initial=%.2f)", at.name, at.userInitialBalance)
		totalPnL = totalEquity
		totalPnLPct = 0.0
	} else {
		// 正常计算收益率
		totalPnLPct = (totalPnL / at.userInitialBalance) * 100

		// 边界检查：异常高的收益率可能表示数据错误
		if math.Abs(totalPnLPct) > 10000 { // 10000% = 100倍收益
			log.Printf("🚨 [AccountInfo] 异常高收益率检测 (trader=%s pnl_pct=%.2f%% user_initial=%.2f pnl=%.2f)",
				at.name, totalPnLPct, at.userInitialBalance, totalPnL)
			// 可以选择限制最大收益率，但暂时记录日志不修改数值
		}
	}

	marginUsedPct := 0.0
	if totalEquity > 0 {
		marginUsedPct = (totalMarginUsed / totalEquity) * 100
	}

	// 诊断日志：输出盈亏相关数据，便于排查排行榜异常
	log.Printf("📈 [AccountInfo] trader=%s equity=%.2f user_initial=%.2f synced_initial=%.2f pnl=%.2f pnl_pct=%.2f unrealized=%.2f margin_used=%.2f",
		at.name, totalEquity, at.userInitialBalance, at.initialBalance, totalPnL, totalPnLPct, totalUnrealizedPnL, totalMarginUsed)

	return map[string]interface{}{
		// 核心字段
		"total_equity":      totalEquity,           // 账户净值 = wallet + unrealized
		"wallet_balance":    totalWalletBalance,    // 钱包余额（不含未实现盈亏）
		"unrealized_profit": totalUnrealizedProfit, // 未实现盈亏（从API）
		"available_balance": availableBalance,      // 可用余额

		// 盈亏统计
		"total_pnl":              totalPnL,              // 总盈亏 = equity - initial
		"total_pnl_pct":          totalPnLPct,           // 总盈亏百分比
		"total_unrealized_pnl":   totalUnrealizedPnL,    // 未实现盈亏（从持仓计算）
		"initial_balance":        at.userInitialBalance, // 用户初始投资余额
		"synced_initial_balance": at.initialBalance,     // 自动同步的余额
		"daily_pnl":              at.dailyPnL,           // 日盈亏

		// 持仓信息
		"position_count":  len(positions),  // 持仓数量
		"margin_used":     totalMarginUsed, // 保证金占用
		"margin_used_pct": marginUsedPct,   // 保证金使用率
	}, nil
}

// GetPositions 获取持仓列表（用于API）
func (at *AutoTrader) GetPositions() ([]map[string]interface{}, error) {
	positions, err := at.trader.GetPositions()
	if err != nil {
		return nil, fmt.Errorf("获取持仓失败: %w", err)
	}

	var result []map[string]interface{}
	for _, pos := range positions {
		symbol := pos["symbol"].(string)
		side := pos["side"].(string)
		entryPrice := pos["entryPrice"].(float64)
		markPrice := pos["markPrice"].(float64)
		quantity := pos["positionAmt"].(float64)
		if quantity < 0 {
			quantity = -quantity
		}
		unrealizedPnl := pos["unRealizedProfit"].(float64)
		liquidationPrice := pos["liquidationPrice"].(float64)

		leverage := 10
		if lev, ok := pos["leverage"].(float64); ok {
			leverage = int(lev)
		}

		// 计算占用保证金
		marginUsed := (quantity * markPrice) / float64(leverage)

		// 计算盈亏百分比（基于保证金）
		// 收益率 = 未实现盈亏 / 保证金 × 100%
		pnlPct := 0.0
		if marginUsed > 0 {
			pnlPct = (unrealizedPnl / marginUsed) * 100
		}

		result = append(result, map[string]interface{}{
			"symbol":             symbol,
			"side":               side,
			"entry_price":        entryPrice,
			"mark_price":         markPrice,
			"quantity":           quantity,
			"leverage":           leverage,
			"unrealized_pnl":     unrealizedPnl,
			"unrealized_pnl_pct": pnlPct,
			"liquidation_price":  liquidationPrice,
			"margin_used":        marginUsed,
		})
	}

	return result, nil
}

// sortDecisionsByPriority 对决策排序：先平仓，再开仓，最后hold/wait
// 这样可以避免换仓时仓位叠加超限
func sortDecisionsByPriority(decisions []decision.Decision) []decision.Decision {
	if len(decisions) <= 1 {
		return decisions
	}

	// 定义优先级
	getActionPriority := func(action string) int {
		switch action {
		case "close_long", "close_short", "partial_close":
			return 1 // 最高优先级：先平仓（包括部分平仓）
		case "update_stop_loss", "update_take_profit":
			return 2 // 调整持仓止盈止损
		case "open_long", "open_short":
			return 3 // 次优先级：后开仓
		case "hold", "wait":
			return 4 // 最低优先级：观望
		default:
			return 999 // 未知动作放最后
		}
	}

	// 复制决策列表
	sorted := make([]decision.Decision, len(decisions))
	copy(sorted, decisions)

	// 按优先级排序
	for i := 0; i < len(sorted)-1; i++ {
		for j := i + 1; j < len(sorted); j++ {
			if getActionPriority(sorted[i].Action) > getActionPriority(sorted[j].Action) {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	return sorted
}

// getCandidateCoins 获取交易员的候选币种列表
func (at *AutoTrader) getCandidateCoins() ([]decision.CandidateCoin, error) {
	if len(at.tradingCoins) == 0 {
		// 使用数据库配置的默认币种列表
		var candidateCoins []decision.CandidateCoin

		if len(at.defaultCoins) > 0 {
			// 使用数据库中配置的默认币种
			for _, coin := range at.defaultCoins {
				symbol := normalizeSymbol(coin)
				candidateCoins = append(candidateCoins, decision.CandidateCoin{
					Symbol:  symbol,
					Sources: []string{"default"}, // 标记为数据库默认币种
				})
			}
			log.Printf("📋 [%s] 使用数据库默认币种: %d个币种 %v",
				at.name, len(candidateCoins), at.defaultCoins)
			return candidateCoins, nil
		} else {
			// 如果数据库中没有配置默认币种，则使用AI500+OI Top作为fallback
			const ai500Limit = 20 // AI500取前20个评分最高的币种

			mergedPool, err := pool.GetMergedCoinPool(ai500Limit)
			if err != nil {
				return nil, fmt.Errorf("获取合并币种池失败: %w", err)
			}

			// 构建候选币种列表（包含来源信息）
			for _, symbol := range mergedPool.AllSymbols {
				sources := mergedPool.SymbolSources[symbol]
				candidateCoins = append(candidateCoins, decision.CandidateCoin{
					Symbol:  symbol,
					Sources: sources, // "ai500" 和/或 "oi_top"
				})
			}

			log.Printf("📋 [%s] 数据库无默认币种配置，使用AI500+OI Top: AI500前%d + OI_Top20 = 总计%d个候选币种",
				at.name, ai500Limit, len(candidateCoins))
			return candidateCoins, nil
		}
	} else {
		// 使用自定义币种列表
		var candidateCoins []decision.CandidateCoin
		for _, coin := range at.tradingCoins {
			// 确保币种格式正确（转为大写USDT交易对）
			symbol := normalizeSymbol(coin)
			candidateCoins = append(candidateCoins, decision.CandidateCoin{
				Symbol:  symbol,
				Sources: []string{"custom"}, // 标记为自定义来源
			})
		}

		log.Printf("📋 [%s] 使用自定义币种: %d个币种 %v",
			at.name, len(candidateCoins), at.tradingCoins)
		return candidateCoins, nil
	}
}

// normalizeSymbol 基础符号标准化（仅大小写转换）
func normalizeSymbol(symbol string) string {
	return strings.ToUpper(strings.TrimSpace(symbol))
}

// formatSymbolForExchange 根据交易所格式化符号
func (at *AutoTrader) formatSymbolForExchange(symbol string) string {
	// Hyperliquid: 保持 HIP-3 前缀原样（前缀小写 + 冒号）
	if at.exchange == "hyperliquid" && strings.Contains(symbol, ":") {
		return strings.TrimSpace(normalizeHip3Symbol(symbol))
	}

	symbol = normalizeSymbol(symbol)

	// Binance需要USDT后缀
	if at.exchange == "binance" && !strings.HasSuffix(symbol, "USDT") {
		return symbol + "USDT"
	}

	// 其他交易所保持原样（Hyperliquid等）
	return symbol
}

// 启动回撤监控
func (at *AutoTrader) startDrawdownMonitor() {
	at.monitorWg.Add(1)
	go func() {
		defer at.monitorWg.Done()

		ticker := time.NewTicker(1 * time.Minute) // 每分钟检查一次
		defer ticker.Stop()

		log.Println("📊 启动持仓回撤监控（每分钟检查一次）")

		for {
			select {
			case <-ticker.C:
				at.checkPositionDrawdown()
			case <-at.stopMonitorCh:
				log.Println("⏹ 停止持仓回撤监控")
				return
			}
		}
	}()
}

// 检查持仓回撤情况
func (at *AutoTrader) checkPositionDrawdown() {
	// 获取当前持仓
	positions, err := at.trader.GetPositions()
	if err != nil {
		log.Printf("❌ 回撤监控：获取持仓失败: %v", err)
		return
	}

	for _, pos := range positions {
		symbol := pos["symbol"].(string)
		side := pos["side"].(string)
		entryPrice := pos["entryPrice"].(float64)
		markPrice := pos["markPrice"].(float64)
		quantity := pos["positionAmt"].(float64)
		if quantity < 0 {
			quantity = -quantity // 空仓数量为负，转为正数
		}

		// 计算当前盈亏百分比
		leverage := 10 // 默认值
		if lev, ok := pos["leverage"].(float64); ok {
			leverage = int(lev)
		}

		var currentPnLPct float64
		if side == "long" {
			currentPnLPct = ((markPrice - entryPrice) / entryPrice) * float64(leverage) * 100
		} else {
			currentPnLPct = ((entryPrice - markPrice) / entryPrice) * float64(leverage) * 100
		}

		// 构造持仓唯一标识（区分多空）
		posKey := symbol + "_" + side

		// 获取该持仓的历史最高收益
		at.peakPnLCacheMutex.RLock()
		peakPnLPct, exists := at.peakPnLCache[posKey]
		at.peakPnLCacheMutex.RUnlock()

		if !exists {
			// 如果没有历史最高记录，使用当前盈亏作为初始值
			peakPnLPct = currentPnLPct
			at.UpdatePeakPnL(symbol, side, currentPnLPct)
		} else {
			// 更新峰值缓存
			at.UpdatePeakPnL(symbol, side, currentPnLPct)
		}

		// 计算回撤（从最高点下跌的幅度）
		var drawdownPct float64
		if peakPnLPct > 0 && currentPnLPct < peakPnLPct {
			drawdownPct = ((peakPnLPct - currentPnLPct) / peakPnLPct) * 100
		}

		// 检查平仓条件：收益大于5%且回撤超过40%
		if currentPnLPct > 5.0 && drawdownPct >= 40.0 {
			log.Printf("🚨 触发回撤平仓条件: %s %s | 当前收益: %.2f%% | 最高收益: %.2f%% | 回撤: %.2f%%",
				symbol, side, currentPnLPct, peakPnLPct, drawdownPct)

			// 执行平仓
			if err := at.emergencyClosePosition(symbol, side); err != nil {
				log.Printf("❌ 回撤平仓失败 (%s %s): %v", symbol, side, err)
			} else {
				log.Printf("✅ 回撤平仓成功: %s %s", symbol, side)
				// 平仓后清理该持仓的缓存
				at.ClearPeakPnLCache(symbol, side)
			}
		} else if currentPnLPct > 5.0 {
			// 记录接近平仓条件的情况（用于调试）
			log.Printf("📊 回撤监控: %s %s | 收益: %.2f%% | 最高: %.2f%% | 回撤: %.2f%%",
				symbol, side, currentPnLPct, peakPnLPct, drawdownPct)
		}
	}
}

// 紧急平仓函数
func (at *AutoTrader) emergencyClosePosition(symbol, side string) error {
	formattedSymbol := at.formatSymbolForExchange(symbol)
	switch side {
	case "long":
		order, err := at.trader.CloseLong(formattedSymbol, 0) // 0 = 全部平仓
		if err != nil {
			return err
		}
		log.Printf("✅ 紧急平多仓成功，订单ID: %v", order["orderId"])
	case "short":
		order, err := at.trader.CloseShort(formattedSymbol, 0) // 0 = 全部平仓
		if err != nil {
			return err
		}
		log.Printf("✅ 紧急平空仓成功，订单ID: %v", order["orderId"])
	default:
		return fmt.Errorf("未知的持仓方向: %s", side)
	}

	return nil
}

// GetPeakPnLCache 获取最高收益缓存
func (at *AutoTrader) GetPeakPnLCache() map[string]float64 {
	at.peakPnLCacheMutex.RLock()
	defer at.peakPnLCacheMutex.RUnlock()

	// 返回缓存的副本
	cache := make(map[string]float64)
	for k, v := range at.peakPnLCache {
		cache[k] = v
	}
	return cache
}

// UpdatePeakPnL 更新最高收益缓存
func (at *AutoTrader) UpdatePeakPnL(symbol, side string, currentPnLPct float64) {
	at.peakPnLCacheMutex.Lock()
	defer at.peakPnLCacheMutex.Unlock()

	posKey := symbol + "_" + side
	if peak, exists := at.peakPnLCache[posKey]; exists {
		// 更新峰值（如果是多头，取较大值；如果是空头，currentPnLPct为负，也要比较）
		if currentPnLPct > peak {
			at.peakPnLCache[posKey] = currentPnLPct
		}
	} else {
		// 首次记录
		at.peakPnLCache[posKey] = currentPnLPct
	}
}

// ClearPeakPnLCache 清除指定持仓的峰值缓存
func (at *AutoTrader) ClearPeakPnLCache(symbol, side string) {
	at.peakPnLCacheMutex.Lock()
	defer at.peakPnLCacheMutex.Unlock()

	posKey := symbol + "_" + side
	delete(at.peakPnLCache, posKey)
}

func (at *AutoTrader) GetMCPClient() *mcp.Client {
	return at.mcpClient
}

// ExecuteNaturalLanguageTrade 执行自然语言交易命令
// amount: 开仓时表示美元名义；平仓表示数量；止盈止损时忽略
// price: 仅用于止盈/止损，开仓/平仓可为0
func (at *AutoTrader) ExecuteNaturalLanguageTrade(action, symbol string, amount float64, price float64, leverage int, assetType string) (map[string]interface{}, error) {
	if at.trader == nil {
		return nil, fmt.Errorf("交易实例未初始化")
	}

	// 统一符号大小写，避免 "eth"/"Eth" 与仓位 "ETHUSDT" 不匹配
	symbol = strings.ToUpper(strings.TrimSpace(symbol))

	// 基于 asset_type 控制符号映射/解析策略
	resolvedSymbol := symbol
	if at.config.Exchange == "hyperliquid" {
		if ht, ok := at.trader.(*HyperliquidTrader); ok {
			if strings.ToLower(strings.TrimSpace(assetType)) == "crypto" || assetType == "" {
				// crypto：跳过 HIP-3 映射，直接标准化
				resolvedSymbol = convertSymbolToHyperliquid(symbol)
				log.Printf("✓ [HL] crypto 资产直通: %s -> %s", symbol, resolvedSymbol)
			} else {
				// 非 crypto：走 HIP-3 映射
				mapped, err := ht.ResolveNonCryptoSymbol(symbol, true)
				if err != nil {
					return nil, fmt.Errorf("非加密资产符号映射失败: %w", err)
				}
				resolvedSymbol = mapped
				log.Printf("🔄 [HL] 非加密资产映射: %s -> %s (asset_type=%s)", symbol, resolvedSymbol, assetType)
			}
		}
	}

	// 将金额转换为下单数量（amount 视为 USD 名义价值，仅用于开仓）
	var qty float64
	var err error
	if action == "long" || action == "short" {
		if amount <= 0 {
			return nil, fmt.Errorf("开仓金额必须大于0")
		}
		var price float64
		// 非加密资产在 Hyperliquid 上使用 recentTrades 获取价格
		if assetType != "" && assetType != "crypto" && at.config.Exchange == "hyperliquid" {
			if ht, ok := at.trader.(*HyperliquidTrader); ok {
				price, err = ht.GetRecentTradePrice(resolvedSymbol)
			} else {
				price, err = at.trader.GetMarketPrice(resolvedSymbol)
			}
		} else {
			price, err = at.trader.GetMarketPrice(resolvedSymbol)
		}

		if err != nil {
			return nil, fmt.Errorf("获取价格失败: %w", err)
		}
		if price <= 0 {
			return nil, fmt.Errorf("无效价格: %.4f", price)
		}
		qty = amount / price
	}

	switch action {
	case "long":
		formattedSymbol := at.formatSymbolForExchange(resolvedSymbol)
		return at.trader.OpenLong(formattedSymbol, qty, leverage)
	case "short":
		formattedSymbol := at.formatSymbolForExchange(resolvedSymbol)
		return at.trader.OpenShort(formattedSymbol, qty, leverage)
	case "close":
		// 对于平仓，我们需要先确定持仓方向，然后调用相应的方法
		positions, err := at.trader.GetPositions()
		if err != nil {
			return nil, fmt.Errorf("获取持仓失败: %w", err)
		}

		// 查找对应交易对的持仓
		for _, pos := range positions {
			if pos["symbol"] == resolvedSymbol {
				formattedSymbol := at.formatSymbolForExchange(resolvedSymbol)
				if pos["side"] == "long" {
					return at.trader.CloseLong(formattedSymbol, amount)
				} else if pos["side"] == "short" {
					return at.trader.CloseShort(formattedSymbol, amount)
				}
			}
		}

		return nil, fmt.Errorf("未找到 %s 的持仓", symbol)
	case "close_all":
		// 关闭所有持仓
		positions, err := at.trader.GetPositions()
		if err != nil {
			return nil, fmt.Errorf("获取持仓失败: %w", err)
		}

		results := make(map[string]interface{})
		closeCount := 0
		for _, pos := range positions {
			sym := pos["symbol"].(string)
			formattedSymbol := at.formatSymbolForExchange(sym)
			side := pos["side"].(string)
			if side == "long" {
				_, err := at.trader.CloseLong(formattedSymbol, 0)
				if err == nil {
					closeCount++
				}
			} else if side == "short" {
				_, err := at.trader.CloseShort(formattedSymbol, 0)
				if err == nil {
					closeCount++
				}
			}
		}
		results["closed_positions"] = closeCount
		return results, nil
	case "stop_loss", "take_profit":
		if leverage != 0 {
			// leverage 在止盈止损设置中无效
			_ = leverage
		}
		// 使用 price（模型应填），回退到 amount 作为价格以兼容旧逻辑
		tpSlPrice := price
		if tpSlPrice <= 0 {
			tpSlPrice = amount
		}
		if tpSlPrice <= 0 {
			// 如果模型未提供价格，无法设置
			return nil, fmt.Errorf("止盈/止损价格必须大于0")
		}

		positions, err := at.trader.GetPositions()
		if err != nil {
			return nil, fmt.Errorf("获取持仓失败: %w", err)
		}

		for _, pos := range positions {
			sym, _ := pos["symbol"].(string)
			if strings.ToUpper(sym) != strings.ToUpper(symbol) {
				continue
			}
			side, _ := pos["side"].(string)
			q, _ := pos["positionAmt"].(float64)
			if q < 0 {
				q = -q
			}
			positionSide := "LONG"
			if strings.ToLower(side) == "short" {
				positionSide = "SHORT"
			}

			if action == "stop_loss" {
				size := q
				if amount > 0 {
					size = amount // 用户可指定部分数量
				}
				formattedSymbol := at.formatSymbolForExchange(symbol)
				if err := at.trader.SetStopLoss(formattedSymbol, positionSide, size, tpSlPrice); err != nil {
					return nil, fmt.Errorf("设置止损失败: %w", err)
				}
				return map[string]interface{}{"status": "OK", "symbol": symbol, "action": "stop_loss", "price": tpSlPrice, "size": size}, nil
			}
			if action == "take_profit" {
				size := q
				if amount > 0 {
					size = amount // 用户可指定部分数量
				}
				formattedSymbol := at.formatSymbolForExchange(symbol)
				if err := at.trader.SetTakeProfit(formattedSymbol, positionSide, size, tpSlPrice); err != nil {
					return nil, fmt.Errorf("设置止盈失败: %w", err)
				}
				return map[string]interface{}{"status": "OK", "symbol": symbol, "action": "take_profit", "price": tpSlPrice, "size": size}, nil
			}
		}
		return nil, fmt.Errorf("未找到 %s 的持仓，无法设置止盈/止损", symbol)
	default:
		return nil, fmt.Errorf("不支持的操作类型: %s", action)
	}
}

// GetTraderPositions 获取当前持仓
func (at *AutoTrader) GetTraderPositions() ([]map[string]interface{}, error) {
	if at.trader == nil {
		return nil, fmt.Errorf("交易实例未初始化")
	}
	return at.trader.GetPositions()
}

// isTGTrader 检查是否为TG交易员（通过检查userID是否为纯数字）
func isTGTrader(userID string) bool {
	// TG交易员的userID是纯数字字符串（Telegram用户ID）
	// 普通交易员的userID是UUID格式（包含连字符）
	_, err := strconv.ParseInt(userID, 10, 64)
	return err == nil
}
