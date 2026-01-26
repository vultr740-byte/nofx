package telegram

import (
	"fmt"
	"log"
	"math/rand"
	"strconv"
	"strings"
	"time"

	"nofx/config"
	"nofx/manager"
	"nofx/trader"

	"github.com/google/uuid"
)

// TelegramTraderManager Telegram 交易员管理器
type TelegramTraderManager struct {
	db           config.DatabaseInterface
	traderMgr    *manager.TraderManager
	sessionMgr   *SessionManager
	tgBotMgr     interface{} // TelegramBotManager引用，用于推送决策
	testnet      bool
	referralCode string
}

// NewTelegramTraderManager 创建 Telegram 交易员管理器
func NewTelegramTraderManager(db config.DatabaseInterface, traderMgr *manager.TraderManager, testnet bool, referralCode string) *TelegramTraderManager {
	return &TelegramTraderManager{
		db:           db,
		traderMgr:    traderMgr,
		sessionMgr:   NewSessionManager(),
		testnet:      testnet,
		referralCode: strings.TrimSpace(referralCode),
	}
}

// GetSessionManager 获取会话管理器
func (ttm *TelegramTraderManager) GetSessionManager() *SessionManager {
	return ttm.sessionMgr
}

// SetTelegramBotManager 设置TelegramBotManager引用
func (ttm *TelegramTraderManager) SetTelegramBotManager(tgBotMgr interface{}) {
	ttm.tgBotMgr = tgBotMgr
}

// setupTelegramBotManagerForTrader 为交易员设置Telegram推送功能
func (ttm *TelegramTraderManager) setupTelegramBotManagerForTrader(traderObj *trader.AutoTrader) {
	if ttm.tgBotMgr == nil {
		log.Printf("⚠️ TelegramBotManager未设置，无法为交易员启用决策推送")
		return
	}

	// 设置TelegramBotManager到AutoTrader
	traderObj.SetTelegramBotManager(ttm.tgBotMgr)
	log.Printf("✅ 已为交易员启用Telegram决策推送功能")
}

// traderNicknames AI交易员昵称列表
var traderNicknames = []string{
	// 动物/自然风格
	"🦅 猎鹰交易员",
	"🐺 币圈孤狼",
	"🦈 市场鲨鱼",
	"🐉 交易神龙",
	"🦁 币圈雄狮",
	"🐼 熊猫交易员",
	"🦉 夜猫子交易员",
	"🐻 熊市猎手",
	// 神话/传说风格
	"🐱 招财猫",
	"💰 聚宝盆",
	"🐸 金蟾交易员",
	"🦄 独角兽",
	"⚡ 雷神交易员",
	"🔥 火凤凰",
	"❄️ 冰霜交易员",
	// 科技/算法风格
	"🤖 代码猎人",
	"🧙 算法巫师",
	"🔮 数据魔法师",
	"⚙️ 二进制战士",
	"💻 代码刺客",
	"🎯 精准算法",
	"🔬 量化科学家",
	// 趣味/网络风格
	"💎 钻石手",
	"🚀 梭哈大师",
	"🛡️ HODL信徒",
	"🎲 币圈赌神",
	"🎨 K线艺术家",
	"💪 币圈硬汉",
	"🔥 币圈老炮",
	// 武侠/江湖风格
	"⚔️ 交易剑客",
	"🗡️ 币圈侠客",
	"🏹 市场游侠",
	"🥷 暗夜交易员",
	"👤 币圈扫地僧",
	"🗡️ 币圈刀客",
	"⚔️ 交易宗师",
	// 时间/节奏风格
	"🌙 午夜操盘手",
	"🌅 黎明猎手",
	"☀️ 日不落交易员",
	"⏰ 时间猎人",
	"⚡ 闪电交易员",
	"🌊 波动捕手",
	// 其他创意
	"🎯 图表魔法师",
	"📊 数据炼金师",
	"🔍 市场侦探",
	"🎭 币圈戏精",
	"🌟 交易新星",
	"⚔️ 币圈战神",
	"🎪 收益收割机",
}

// generateRandomTraderName 随机生成交易员昵称
func generateRandomTraderName() string {
	if len(traderNicknames) == 0 {
		return "AI交易员"
	}
	rand.Seed(time.Now().UnixNano())
	return traderNicknames[rand.Intn(len(traderNicknames))]
}

// EnsureTraderAccount 确保用户已经分配了Hyperliquid账号（仅生成钱包，不配置策略）
func (ttm *TelegramTraderManager) EnsureTraderAccount(telegramID int64) (*config.TgTraderRecord, error) {
	traders, err := ttm.db.GetTgTraders(telegramID)
	if err != nil {
		return nil, fmt.Errorf("获取交易员失败: %w", err)
	}
	if len(traders) > 0 {
		return &traders[0], nil
	}

	return ttm.createTraderSkeleton(telegramID, len(traders)+1)
}

// createTraderSkeleton 只创建钱包账户，等待 /create_trader 完成配置
func (ttm *TelegramTraderManager) createTraderSkeleton(telegramID int64, index int) (*config.TgTraderRecord, error) {
	traderID := uuid.New().String()

	agentKey, walletAddr, err := generateHyperliquidAccount()
	if err != nil {
		return nil, fmt.Errorf("生成 Hyperliquid 账号失败: %w", err)
	}

	traderName := generateRandomTraderName()
	skeleton := &config.TgTraderRecord{
		ID:                   traderID,
		TgUserID:             telegramID,
		Name:                 traderName,
		AIModelID:            "deepseek",
		AIModelName:          "",
		ExchangeID:           "hyperliquid",
		InitialBalance:       0,
		ScanIntervalMinutes:  0,
		IsRunning:            false,
		IsConfigured:         false,
		BTCETHLeverage:       0,
		AltcoinLeverage:      0,
		TradingSymbols:       "",
		UseCoinPool:          true,
		UseOITop:             false,
		CustomPrompt:         "",
		OverrideBasePrompt:   false,
		IsCrossMargin:        true,
		UseDefaultCoins:      true,
		CustomCoins:          "",
		ReferralCode:         "",
		SystemPromptTemplate: "",
		AIModelAPIKey:        "",
		AIModelAPIURL:        "",
		PrivateKey:           agentKey,
		WalletAddress:        walletAddr,
	}

	if err := ttm.db.CreateTgTrader(telegramID, skeleton); err != nil {
		return nil, fmt.Errorf("创建TG交易员基础账户失败: %w", err)
	}

	ttm.ensureReferralBound(skeleton)
	log.Printf("✅ 已为用户 %d 生成 Hyperliquid 钱包: %s", telegramID, walletAddr)
	return skeleton, nil
}

func (ttm *TelegramTraderManager) findPendingTrader(traders []config.TgTraderRecord) *config.TgTraderRecord {
	for i := range traders {
		if !traders[i].IsConfigured {
			return &traders[i]
		}
	}
	return nil
}

func (ttm *TelegramTraderManager) determineTraderName(defaultName string, traderConfig *TraderConfig) string {
	if traderConfig != nil && traderConfig.CustomParams != nil {
		if nameVal, ok := traderConfig.CustomParams["name"].(string); ok && strings.TrimSpace(nameVal) != "" {
			return strings.TrimSpace(nameVal)
		}
	}
	if strings.TrimSpace(defaultName) != "" {
		return strings.TrimSpace(defaultName)
	}
	return "AI交易员"
}

func (ttm *TelegramTraderManager) buildConfiguredTraderRecord(telegramID int64, defaultName string, traderConfig *TraderConfig) *config.TgTraderRecord {
	provider := normalizeAIProvider(traderConfig.AIProvider)
	traderName := ttm.determineTraderName(defaultName, traderConfig)
	templateName := traderConfig.PromptTemplate
	if templateName == "" {
		templateName = "default"
	}

	return &config.TgTraderRecord{
		TgUserID:             telegramID,
		Name:                 traderName,
		AIModelID:            provider,
		AIModelName:          traderConfig.AIModelName,
		ExchangeID:           "hyperliquid",
		InitialBalance:       traderConfig.InitialBalance,
		ScanIntervalMinutes:  traderConfig.ScanIntervalMinutes,
		IsRunning:            false,
		IsConfigured:         true,
		BTCETHLeverage:       traderConfig.BTCETHLeverage,
		AltcoinLeverage:      traderConfig.AltcoinLeverage,
		TradingSymbols:       "",
		UseCoinPool:          true,
		UseOITop:             false,
		CustomPrompt:         "",
		OverrideBasePrompt:   false,
		IsCrossMargin:        true,
		UseDefaultCoins:      true,
		CustomCoins:          "",
		ReferralCode:         "",
		SystemPromptTemplate: templateName,
		AIModelAPIKey:        traderConfig.AIModelAPIKey,
		AIModelAPIURL:        traderConfig.AIModelAPIURL,
	}
}

// CreateTrader 创建交易员
func (ttm *TelegramTraderManager) CreateTrader(telegramID int64, traderConfig *TraderConfig) (*config.TgTraderRecord, error) {
	existingTraders, err := ttm.db.GetTgTraders(telegramID)
	if err != nil {
		log.Printf("获取现有交易员失败: %v", err)
	}

	if pending := ttm.findPendingTrader(existingTraders); pending != nil {
		configuredTrader := ttm.buildConfiguredTraderRecord(telegramID, pending.Name, traderConfig)
		configuredTrader.ID = pending.ID
		configuredTrader.PrivateKey = pending.PrivateKey
		configuredTrader.WalletAddress = pending.WalletAddress
		configuredTrader.ReferralCode = pending.ReferralCode
		configuredTrader.IsRunning = pending.IsRunning
		configuredTrader.CreatedAt = pending.CreatedAt
		configuredTrader.IsConfigured = true

		if err := ttm.db.UpdateTgTraderConfig(telegramID, pending.ID, configuredTrader); err != nil {
			return nil, err
		}

		ttm.ensureReferralBound(configuredTrader)
		log.Printf("✅ 已更新未配置的交易员: %s", configuredTrader.Name)
		return configuredTrader, nil
	}

	traderID := uuid.New().String()
	agentKey, walletAddr, err := generateHyperliquidAccount()
	if err != nil {
		return nil, fmt.Errorf("生成 Hyperliquid 账号失败: %w", err)
	}

	defaultName := generateRandomTraderName()
	newTrader := ttm.buildConfiguredTraderRecord(telegramID, defaultName, traderConfig)
	newTrader.ID = traderID
	newTrader.PrivateKey = agentKey
	newTrader.WalletAddress = walletAddr

	if err := ttm.db.CreateTgTrader(telegramID, newTrader); err != nil {
		return nil, fmt.Errorf("创建TG交易员失败: %w", err)
	}

	ttm.ensureReferralBound(newTrader)
	log.Printf("✅ 成功创建TG交易员: %s", newTrader.Name)
	return newTrader, nil
}

// UpdateTraderAPIConfig 更新交易员的AI提供商与API KEY
func (ttm *TelegramTraderManager) UpdateTraderAPIConfig(telegramID int64, provider string, apiKey string, modelName string) (*config.TgTraderRecord, error) {
	provider = normalizeAIProvider(provider)
	modelName = strings.TrimSpace(modelName)

	traders, err := ttm.db.GetTgTraders(telegramID)
	if err != nil {
		return nil, fmt.Errorf("获取交易员失败: %w", err)
	}
	if len(traders) == 0 {
		return nil, fmt.Errorf("您还没有创建交易员，请先使用 /start 初始化账号")
	}

	traderRecord := traders[0]
	if err := ttm.db.UpdateTgTraderAPIConfig(telegramID, traderRecord.ID, provider, apiKey, modelName); err != nil {
		return nil, err
	}

	traderRecord.AIModelID = provider
	traderRecord.AIModelAPIKey = apiKey
	if modelName != "" {
		traderRecord.AIModelName = modelName
	}

	if ttm.traderMgr != nil {
		if traderObj, err := ttm.traderMgr.GetTrader(traderRecord.ID); err == nil {
			traderObj.UpdateAIConfig(provider, apiKey, traderRecord.AIModelName)
			log.Printf("🔁 已同步内存中的交易员 %s 的 AI 配置", traderRecord.Name)
		}
	}

	return &traderRecord, nil
}

// UpdateTraderPromptTemplate 更新交易员的策略提示词模板
func (ttm *TelegramTraderManager) UpdateTraderPromptTemplate(telegramID int64, templateName string) (*config.TgTraderRecord, error) {
	templateName = strings.TrimSpace(templateName)
	if templateName == "" {
		return nil, fmt.Errorf("无效的交易策略")
	}

	valid := false
	for _, template := range GetAvailablePromptTemplates() {
		if template.Name == templateName {
			valid = true
			break
		}
	}
	if !valid {
		return nil, fmt.Errorf("未找到该交易策略")
	}

	traders, err := ttm.db.GetTgTraders(telegramID)
	if err != nil {
		return nil, fmt.Errorf("获取交易员失败: %w", err)
	}
	if len(traders) == 0 {
		return nil, fmt.Errorf("您还没有创建交易员，请先使用 /start 初始化账号")
	}

	traderRecord := traders[0]
	if !traderRecord.IsConfigured {
		return nil, fmt.Errorf("交易员尚未配置，请先使用 /create_trader 完成设置")
	}

	traderRecord.SystemPromptTemplate = templateName
	if err := ttm.db.UpdateTgTraderConfig(telegramID, traderRecord.ID, &traderRecord); err != nil {
		return nil, err
	}

	if ttm.traderMgr != nil {
		if traderObj, err := ttm.traderMgr.GetTrader(traderRecord.ID); err == nil {
			traderObj.SetSystemPromptTemplate(templateName)
			log.Printf("🔁 已同步内存中的交易员 %s 的策略模板: %s", traderRecord.Name, templateName)
		}
	}

	return &traderRecord, nil
}

func (ttm *TelegramTraderManager) ensureReferralBound(traderRecord *config.TgTraderRecord) {
	const referralAlreadySetMarker = "already_set"

	if traderRecord == nil {
		log.Printf("⚠️ referral 绑定跳过：交易员记录为空")
		return
	}
	if ttm.testnet {
		log.Printf("ℹ️ referral 绑定跳过：当前为测试网（trader_id=%s）", traderRecord.ID)
		return
	}

	code := strings.TrimSpace(ttm.referralCode)
	if code == "" {
		log.Printf("ℹ️ referral 绑定跳过：referral code 为空（trader_id=%s）", traderRecord.ID)
		return
	}
	if strings.EqualFold(traderRecord.ReferralCode, code) {
		log.Printf("ℹ️ referral 绑定跳过：数据库已是相同 code（trader_id=%s）", traderRecord.ID)
		return
	}
	if strings.EqualFold(traderRecord.ReferralCode, referralAlreadySetMarker) {
		log.Printf("ℹ️ referral 绑定跳过：referrer 已绑定（trader_id=%s）", traderRecord.ID)
		return
	}
	if traderRecord.PrivateKey == "" || traderRecord.WalletAddress == "" {
		log.Printf("⚠️ referral 绑定跳过：缺少私钥或地址（trader_id=%s）", traderRecord.ID)
		return
	}

	traderObj, err := trader.NewHyperliquidTrader(traderRecord.PrivateKey, traderRecord.WalletAddress, ttm.testnet)
	if err != nil {
		log.Printf("⚠️ 绑定 referral code 失败（初始化交易器失败）: %v", err)
		return
	}

	state, err := traderObj.QueryReferralState()
	if err != nil {
		log.Printf("⚠️ 查询 referral 状态失败: %v", err)
	} else {
		log.Printf("ℹ️ referral 状态: %+v", state)
	}

	if state != nil && state.Referrer != "" {
		log.Printf("ℹ️ 已存在 referrer (%s)，尝试覆盖为 %s", state.Referrer, code)
	} else {
		log.Printf("📝 尝试绑定 referral code: %s", code)
	}

	resp, err := traderObj.SetReferrerCode(code)
	if err != nil {
		log.Printf("⚠️ 设置 referral code 失败: %v", err)
		return
	}
	if resp != nil {
		log.Printf("ℹ️ 设置 referral code 响应: %+v", resp)
	}
	if resp != nil && resp.Status != "" && strings.ToLower(resp.Status) != "ok" {
		if strings.Contains(strings.ToLower(resp.Response), "referrer already set") {
			log.Printf("ℹ️ referrer 已绑定，跳过设置并标记数据库（trader_id=%s）", traderRecord.ID)
			traderRecord.ReferralCode = referralAlreadySetMarker
			if err := ttm.db.UpdateTgTraderConfig(traderRecord.TgUserID, traderRecord.ID, traderRecord); err != nil {
				log.Printf("⚠️ 更新 referral_code 失败: %v", err)
			}
			return
		}
		log.Printf("⚠️ 设置 referral code 返回异常: status=%s error=%s response=%s", resp.Status, resp.Error, resp.Response)
		return
	}

	traderRecord.ReferralCode = code
	if err := ttm.db.UpdateTgTraderConfig(traderRecord.TgUserID, traderRecord.ID, traderRecord); err != nil {
		log.Printf("⚠️ 更新 referral_code 失败: %v", err)
		return
	}

	log.Printf("✅ referral code 绑定成功: %s", code)
}

// StartTrader 启动交易员 - 重构版本，确保状态同步
func (ttm *TelegramTraderManager) StartTrader(telegramID int64) error {
	// 1. 获取用户的TG交易员
	traders, err := ttm.db.GetTgTraders(telegramID)
	if err != nil {
		return fmt.Errorf("获取交易员失败: %w", err)
	}

	if len(traders) == 0 {
		return fmt.Errorf("您还没有创建交易员，请先使用 /start 初始化账号")
	}

	trader := traders[0]
	ttm.ensureReferralBound(&trader)

	// 2. 检查是否已经在运行中（双重检查）
	if trader.IsRunning {
		// 进一步检查实际的运行状态
		if traderObj, err := ttm.traderMgr.GetTrader(trader.ID); err == nil {
			if traderObj.IsRunning() {
				return fmt.Errorf("交易员已经在运行中")
			} else {
				// 数据库显示运行中，但实际已停止，同步状态
				log.Printf("⚠️ 检测到状态不一致，正在同步数据库状态: %s", trader.Name)
				ttm.db.UpdateTgTraderStatus(telegramID, trader.ID, false)
			}
		}
	}

	// 3. 强制从数据库重新加载交易员配置，确保获取最新的API密钥
	log.Printf("🔄 重新加载TG交易员配置以确保最新API密钥: %s", trader.ID)
	database, ok := ttm.db.(*config.Database)
	if !ok {
		return fmt.Errorf("数据库类型不支持")
	}
	if err := ttm.traderMgr.LoadTGTradersFromDatabase(database, ttm.testnet); err != nil {
		log.Printf("⚠️ 加载TG交易员配置失败: %v", err)
		// 继续执行，可能是首次加载
	}

	// 4. 获取或创建TG交易员实例
	traderObj, err := ttm.GetTgTrader(trader.ID)
	if err != nil {
		return fmt.Errorf("获取TG交易员实例失败: %w", err)
	}

	// 5. 确保TelegramBotManager已设置
	ttm.setupTelegramBotManagerForTrader(traderObj)

	// 6. 启动交易员（同步等待启动成功）
	startCh := make(chan error, 1)
	go func() {
		if err := traderObj.Run(); err != nil {
			log.Printf("❌ TG交易员运行失败: %s, 错误: %v", trader.Name, err)
			startCh <- fmt.Errorf("交易员运行失败: %w", err)
		} else {
			startCh <- nil
		}
	}()

	// 6. 等待一小段时间确保启动成功
	select {
	case err := <-startCh:
		if err != nil {
			return fmt.Errorf("启动TG交易员失败: %w", err)
		}
	case <-time.After(3 * time.Second):
		// 3秒内没有错误，认为启动成功
		log.Printf("✅ TG交易员启动成功: %s", trader.Name)
	}

	// 7. 确认启动成功后更新数据库状态
	if !traderObj.IsRunning() {
		return fmt.Errorf("交易员启动失败或立即停止")
	}

	if err := ttm.db.UpdateTgTraderStatus(telegramID, trader.ID, true); err != nil {
		log.Printf("⚠️ 更新TG交易员运行状态失败: %v", err)
		// 不返回错误，因为交易员已经实际启动
	}

	log.Printf("✅ 成功启动TG交易员: %s (数据库和内存状态已同步)", trader.Name)
	return nil
}

// CheckPositionsBeforeStop 停止前检查仓位 - 复用 /positions 查询逻辑
func (ttm *TelegramTraderManager) CheckPositionsBeforeStop(telegramID int64) (bool, int, error) {
	// 检查 TelegramBotManager 是否已设置
	if ttm.tgBotMgr == nil {
		return false, 0, fmt.Errorf("TelegramBotManager未设置")
	}

	// 类型断言获取 TelegramBotManager
	tgBotMgr, ok := ttm.tgBotMgr.(*TelegramBotManager)
	if !ok {
		return false, 0, fmt.Errorf("TelegramBotManager类型断言失败")
	}

	// 检查是否有 Hyperliquid 账号 (复用 /positions 的逻辑)
	if !tgBotMgr.hasHyperliquidAccount(telegramID) {
		return false, 0, fmt.Errorf("未找到 Hyperliquid 账号")
	}

	// 提取 Agent Key 和 Wallet Address (复用 /positions 的逻辑)
	agentKey, walletAddr, err := tgBotMgr.extractAgentKeyAndWallet(telegramID)
	if err != nil {
		return false, 0, fmt.Errorf("提取账号信息失败: %w", err)
	}

	log.Printf("🔍 复用 /positions 查询逻辑检查仓位 (AgentKey: %s, Wallet: %s)",
		maskPrivateKey(agentKey), walletAddr)

	// 复用 /positions 相同的查询逻辑
	positionsMsg, err := tgBotMgr.hlService.GetPositions(agentKey, walletAddr, tgBotMgr.testnet)
	if err != nil {
		return false, 0, fmt.Errorf("查询持仓失败: %w", err)
	}

	log.Printf("📋 /positions 查询成功，返回消息长度: %d", len(positionsMsg))

	// 通过解析返回的消息来判断是否有仓位
	// 如果消息包含 "持仓数量: 0 个" 或类似内容，说明没有仓位
	hasPositions := true
	positionCount := 0

	// 检查消息中是否有仓位信息
	if strings.Contains(positionsMsg, "持仓数量: 0 个") ||
		strings.Contains(positionsMsg, "当前持仓\n\n") && len(positionsMsg) < 50 {
		hasPositions = false
		positionCount = 0
		log.Printf("📊 解析 /positions 结果: 无未平仓合约")
	} else if strings.Contains(positionsMsg, "持仓数量:") {
		// 尝试从消息中提取持仓数量
		if idx := strings.Index(positionsMsg, "持仓数量:"); idx != -1 {
			remaining := positionsMsg[idx+len("持仓数量: "):]
			if spaceIdx := strings.Index(remaining, " "); spaceIdx != -1 {
				countStr := remaining[:spaceIdx]
				if count, err := strconv.Atoi(strings.TrimSpace(countStr)); err == nil {
					positionCount = count
					hasPositions = count > 0
					log.Printf("📊 解析 /positions 结果: 发现 %d 个未平仓合约", positionCount)
				}
			}
		}
	}

	// 如果解析失败，通过检查消息内容来判断
	if positionCount == 0 && hasPositions {
		// 如果包含具体的仓位信息，说明有仓位
		if strings.Contains(positionsMsg, "→ LONG") ||
			strings.Contains(positionsMsg, "→ SHORT") ||
			strings.Contains(positionsMsg, "数量:") {
			hasPositions = true
			positionCount = 1 // 至少有1个仓位
			log.Printf("📊 通过内容分析判断: 有未平仓合约")
		} else {
			hasPositions = false
			log.Printf("📊 通过内容分析判断: 无未平仓合约")
		}
	}

	log.Printf("✅ 仓位检查完成: 有仓位=%v, 数量=%d", hasPositions, positionCount)

	return hasPositions, positionCount, nil
}

// StopTrader 停止交易员 - 重构版本，确保进程真正停止
func (ttm *TelegramTraderManager) StopTrader(telegramID int64) error {
	// 1. 获取用户的TG交易员
	traders, err := ttm.db.GetTgTraders(telegramID)
	if err != nil {
		return fmt.Errorf("获取交易员失败: %w", err)
	}

	if len(traders) == 0 {
		return fmt.Errorf("您还没有创建交易员")
	}

	trader := traders[0]

	// 2. 双重检查运行状态
	if !trader.IsRunning {
		// 进一步检查实际的运行状态
		if traderObj, err := ttm.traderMgr.GetTrader(trader.ID); err == nil {
			if traderObj.IsRunning() {
				// 数据库显示已停止，但实际还在运行，先同步状态
				log.Printf("⚠️ 检测到状态不一致，正在同步数据库状态: %s", trader.Name)
				ttm.db.UpdateTgTraderStatus(telegramID, trader.ID, true)
			} else {
				return fmt.Errorf("交易员未在运行")
			}
		} else {
			return fmt.Errorf("交易员未在运行")
		}
	}

	// 3. 获取交易员实例并停止
	traderObj, err := ttm.traderMgr.GetTrader(trader.ID)
	if err != nil {
		// 交易员实例不存在，但数据库显示运行中，直接同步状态
		log.Printf("⚠️ 交易员实例不存在，正在同步数据库状态: %s", trader.Name)
		if syncErr := ttm.db.UpdateTgTraderStatus(telegramID, trader.ID, false); syncErr != nil {
			log.Printf("❌ 同步数据库状态失败: %v", syncErr)
		}
		return fmt.Errorf("交易员实例不存在，已同步状态")
	}

	// 4. 调用停止方法
	log.Printf("⏹️ 正在停止交易员: %s", trader.Name)
	traderObj.Stop()

	// 5. 等待交易员真正停止（最多等待5秒）
	stopTimeout := time.After(5 * time.Second)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	stopped := false
	for {
		select {
		case <-ticker.C:
			if !traderObj.IsRunning() {
				stopped = true
				log.Printf("✅ 确认交易员已停止: %s", trader.Name)
				goto STOPPED
			}
		case <-stopTimeout:
			log.Printf("⚠️ 停止交易员超时: %s", trader.Name)
			goto STOPPED
		}
	}

STOPPED:
	// 6. 更新数据库状态（确保数据库与实际状态同步）
	currentRunningState := traderObj.IsRunning()
	if err := ttm.db.UpdateTgTraderStatus(telegramID, trader.ID, currentRunningState); err != nil {
		log.Printf("⚠️ 更新TG交易员运行状态失败: %v", err)
		// 如果数据库更新失败，记录但不影响返回结果
	}

	if stopped && !currentRunningState {
		log.Printf("✅ 成功停止TG交易员: %s (进程和数据库状态已同步)", trader.Name)
		return nil
	} else {
		log.Printf("⚠️ TG交易员停止可能未完成: %s (当前状态: %v)", trader.Name, currentRunningState)
		return fmt.Errorf("交易员停止可能未完全成功，请检查状态")
	}
}

// StopTraderWithPositions 停止交易员并平掉所有仓位
func (ttm *TelegramTraderManager) StopTraderWithPositions(telegramID int64) ([]string, error) {
	// 1. 获取用户的TG交易员
	traders, err := ttm.db.GetTgTraders(telegramID)
	if err != nil {
		return nil, fmt.Errorf("获取交易员失败: %w", err)
	}

	if len(traders) == 0 {
		return nil, fmt.Errorf("您还没有创建交易员")
	}

	trader := traders[0]

	// 2. 获取交易员实例
	traderObj, err := ttm.traderMgr.GetTrader(trader.ID)
	if err != nil {
		return nil, fmt.Errorf("获取交易员实例失败: %w", err)
	}

	// 直接调用带平仓的停止方法
	closeResults, err := traderObj.StopWithPositionsClose()
	if err != nil {
		return closeResults, err
	}

	// 更新数据库状态
	if dbErr := ttm.db.UpdateTgTraderStatus(telegramID, trader.ID, false); dbErr != nil {
		log.Printf("⚠️ 更新TG交易员运行状态失败: %v", dbErr)
	}

	return closeResults, nil
}

// GetTraderStatus 获取交易员状态 - 重构版本，确保状态实时准确
func (ttm *TelegramTraderManager) GetTraderStatus(telegramID int64) (map[string]interface{}, error) {
	// 1. 获取用户的TG交易员
	traders, err := ttm.db.GetTgTraders(telegramID)
	if err != nil {
		return map[string]interface{}{
			"has_trader": false,
		}, fmt.Errorf("获取交易员失败: %w", err)
	}

	// 2. 返回无交易员状态
	if len(traders) == 0 {
		return map[string]interface{}{
			"has_trader": false,
		}, nil
	}

	trader := traders[0]

	// 3. 获取实际的运行状态（双重验证）
	actualRunningState := false
	if traderObj, err := ttm.traderMgr.GetTrader(trader.ID); err == nil {
		actualRunningState = traderObj.IsRunning()

		// 检测到状态不一致时自动同步
		if trader.IsRunning != actualRunningState {
			log.Printf("⚠️ 检测到状态不一致，数据库: %v, 实际: %v, 正在同步: %s",
				trader.IsRunning, actualRunningState, trader.Name)

			// 同步数据库状态
			if syncErr := ttm.db.UpdateTgTraderStatus(telegramID, trader.ID, actualRunningState); syncErr != nil {
				log.Printf("❌ 同步状态失败: %v", syncErr)
			} else {
				log.Printf("✅ 状态同步成功: %s", trader.Name)
				// 更新本地变量
				trader.IsRunning = actualRunningState
			}
		}
	} else {
		// 交易员实例不存在，说明已停止
		if trader.IsRunning {
			log.Printf("⚠️ 交易员实例不存在但数据库显示运行中，正在同步: %s", trader.Name)
			if syncErr := ttm.db.UpdateTgTraderStatus(telegramID, trader.ID, false); syncErr != nil {
				log.Printf("❌ 同步状态失败: %v", syncErr)
			} else {
				trader.IsRunning = false
			}
		}
		actualRunningState = false
	}

	// 4. 构建状态返回
	status := map[string]interface{}{
		"has_trader":            true,
		"id":                    trader.ID,
		"name":                  trader.Name,
		"is_running":            actualRunningState, // 使用实际运行状态
		"is_configured":         trader.IsConfigured,
		"wallet_address":        normalizeEVMAddressLower(trader.WalletAddress),
		"initial_balance":       trader.InitialBalance,
		"scan_interval_minutes": trader.ScanIntervalMinutes,
		"btc_eth_leverage":      trader.BTCETHLeverage,
		"altcoin_leverage":      trader.AltcoinLeverage,
		"created_at":            trader.CreatedAt,
		"updated_at":            trader.UpdatedAt,
		"status":                "停止",
		"prompt_template":       trader.SystemPromptTemplate,
	}

	if actualRunningState {
		if trader.IsConfigured {
			status["status"] = "运行中"
		}

		// 5. 如果确实在运行中，获取更多实时信息
		if traderObj, err := ttm.traderMgr.GetTrader(trader.ID); err == nil {
			// 获取实时余额和持仓信息（如果交易员支持）
			if balance := traderObj.GetCurrentBalance(); balance > 0 {
				status["current_balance"] = fmt.Sprintf("%.2f", balance)
			} else {
				status["current_balance"] = "运行中"
			}

			if positions := traderObj.GetPositionsCount(); positions >= 0 {
				status["positions_count"] = positions
			} else {
				status["positions_count"] = "N/A"
			}

			// 添加运行时长信息
			if startTime := traderObj.GetStartTime(); !startTime.IsZero() {
				status["running_duration"] = time.Since(startTime).String()
			}
		}
	} else {
		if trader.IsConfigured {
			status["current_balance"] = "已停止"
			status["positions_count"] = "0"
		}
	}

	if !trader.IsConfigured {
		status["status"] = "未配置"
	}

	return status, nil
}

// GetTgTrader 获取或创建TG交易员实例
func (ttm *TelegramTraderManager) GetTgTrader(traderID string) (*trader.AutoTrader, error) {
	// 首先尝试从现有的TraderManager获取
	if traderObj, err := ttm.traderMgr.GetTrader(traderID); err == nil {
		// 设置TelegramBotManager以便推送决策（如果还没设置的话）
		ttm.setupTelegramBotManagerForTrader(traderObj)
		return traderObj, nil
	}

	// 如果不存在，尝试从数据库动态加载TG交易员
	log.Printf("🔍 TG交易员 %s 不在内存中，尝试从数据库加载...", traderID)

	// 获取数据库实例
	database, ok := ttm.db.(*config.Database)
	if !ok {
		return nil, fmt.Errorf("数据库类型不支持，无法动态加载TG交易员")
	}

	// 从数据库获取TG交易员记录
	var tgTrader *config.TgTraderRecord

	// 遍历所有TG用户查找该交易员
	tgUserIDs, err := database.GetAllTGUsers()
	if err != nil {
		return nil, fmt.Errorf("获取TG用户列表失败: %w", err)
	}

	for _, tgUserID := range tgUserIDs {
		tgTraders, err := database.GetTgTraders(tgUserID)
		if err != nil {
			continue
		}

		for _, t := range tgTraders {
			if t.ID == traderID {
				tgTrader = &t
				break
			}
		}

		if tgTrader != nil {
			break
		}
	}

	if tgTrader == nil {
		return nil, fmt.Errorf("TG交易员不存在: %s", traderID)
	}

	// 使用TraderManager的loadTGTraderFromDB方法动态加载
	log.Printf("🔄 动态创建TG交易员实例: %s", traderID)
	if err := ttm.traderMgr.LoadTGTradersFromDatabase(database, ttm.testnet); err != nil {
		return nil, fmt.Errorf("动态加载TG交易员失败: %w", err)
	}

	// 再次尝试获取
	if traderObj, err := ttm.traderMgr.GetTrader(traderID); err == nil {
		log.Printf("✅ 成功动态创建TG交易员: %s", traderID)

		// 设置TelegramBotManager以便推送决策
		ttm.setupTelegramBotManagerForTrader(traderObj)

		return traderObj, nil
	}

	return nil, fmt.Errorf("动态创建TG交易员失败: %s", traderID)
}

// 默认AI模型
func (ttm *TelegramTraderManager) getDefaultAIModel(userID string) (*config.AIModelConfig, error) {
	models, err := ttm.db.GetAIModels(userID)
	if err != nil {
		return nil, fmt.Errorf("获取AI模型失败: %w", err)
	}

	// 优先使用 DeepSeek
	for _, model := range models {
		if model.Provider == "deepseek" && model.Enabled {
			return model, nil
		}
	}

	// 其次使用 Qwen
	for _, model := range models {
		if model.Provider == "qwen" && model.Enabled {
			return model, nil
		}
	}

	// 如果没有启用的模型，返回第一个
	if len(models) > 0 {
		return models[0], nil
	}

	return nil, fmt.Errorf("没有找到可用的AI模型")
}

// 默认交易所配置
func (ttm *TelegramTraderManager) getDefaultExchange(userID string) (*config.ExchangeConfig, error) {
	exchanges, err := ttm.db.GetExchanges(userID)
	if err != nil {
		return nil, fmt.Errorf("获取交易所配置失败: %w", err)
	}

	// 优先使用 Hyperliquid
	for _, exchange := range exchanges {
		if exchange.Type == "hyperliquid" && exchange.Enabled {
			return exchange, nil
		}
	}

	// 其次使用 Binance
	for _, exchange := range exchanges {
		if exchange.Type == "binance" && exchange.Enabled {
			return exchange, nil
		}
	}

	// 如果没有启用的交易所，返回第一个
	if len(exchanges) > 0 {
		return exchanges[0], nil
	}

	return nil, fmt.Errorf("没有找到可用的交易所配置")
}

// 根据风险等级获取杠杆
func (ttm *TelegramTraderManager) getLeverageByRiskLevel(riskLevel string) (int, int) {
	switch strings.ToLower(riskLevel) {
	case "保守", "低风险", "low":
		return 2, 2 // BTC/ETH 2倍，山寨币 2倍
	case "激进", "高风险", "high":
		return 10, 5 // BTC/ETH 10倍，山寨币 5倍
	default:
		return 5, 3 // BTC/ETH 5倍，山寨币 3倍
	}
}

// 解析数字输入
func (ttm *TelegramTraderManager) ParseNumberInput(input string) (float64, error) {
	input = strings.TrimSpace(input)
	input = strings.ReplaceAll(input, ",", "")
	input = strings.ReplaceAll(input, " ", "")

	value, err := strconv.ParseFloat(input, 64)
	if err != nil {
		return 0, fmt.Errorf("无效的数字格式: %s", input)
	}

	if value <= 0 {
		return 0, fmt.Errorf("数字必须大于0")
	}

	return value, nil
}

// 解析整数输入
func (ttm *TelegramTraderManager) ParseIntInput(input string) (int, error) {
	input = strings.TrimSpace(input)
	input = strings.ReplaceAll(input, ",", "")
	input = strings.ReplaceAll(input, " ", "")

	value, err := strconv.Atoi(input)
	if err != nil {
		return 0, fmt.Errorf("无效的整数格式: %s", input)
	}

	if value <= 0 {
		return 0, fmt.Errorf("数字必须大于0")
	}

	return value, nil
}
