package telegram

import (
	"fmt"
	"log"
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
	db         config.DatabaseInterface
	traderMgr  *manager.TraderManager
	sessionMgr *SessionManager
	tgBotMgr   interface{} // TelegramBotManager引用，用于推送决策
}

// NewTelegramTraderManager 创建 Telegram 交易员管理器
func NewTelegramTraderManager(db config.DatabaseInterface, traderMgr *manager.TraderManager) *TelegramTraderManager {
	return &TelegramTraderManager{
		db:         db,
		traderMgr:  traderMgr,
		sessionMgr: NewSessionManager(),
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

// CreateTrader 创建交易员
func (ttm *TelegramTraderManager) CreateTrader(telegramID int64, traderConfig *TraderConfig) (string, error) {
	// 检查用户是否已有交易员
	existingTraders, err := ttm.db.GetTgTraders(telegramID)
	if err != nil {
		log.Printf("获取现有交易员失败: %v", err)
	} else if len(existingTraders) > 0 {
		return "", fmt.Errorf("您已经创建过交易员了，每个用户只能创建一个交易员")
	}

	// 生成交易员配置
	traderID := uuid.New().String()
	userID := fmt.Sprintf("%d", telegramID)

	// 获取默认AI模型和交易所配置
	aiModel, err := ttm.getDefaultAIModel(userID)
	if err != nil {
		return "", fmt.Errorf("获取默认AI模型失败: %w", err)
	}

	exchange, err := ttm.getDefaultExchange(userID)
	if err != nil {
		return "", fmt.Errorf("获取默认交易所配置失败: %w", err)
	}

	// 创建TG交易员记录
	tgTraderRecord := &config.TgTraderRecord{
		ID:                traderID,
		TgUserID:         telegramID,
		Name:             fmt.Sprintf("AI交易员-%s", traderID[:8]),
		AIModelID:        aiModel.ID,
		ExchangeID:       exchange.ID,
		InitialBalance:   traderConfig.InitialBalance,
		ScanIntervalMinutes: traderConfig.ScanIntervalMinutes,
		IsRunning:        false,
		BTCETHLeverage:   traderConfig.BTCETHLeverage,
		AltcoinLeverage:  traderConfig.AltcoinLeverage,
		TradingSymbols:   "",
		UseCoinPool:      true,
		UseOITop:         false,
		CustomPrompt:     "",
		OverrideBasePrompt: false,
		IsCrossMargin:    true,
		UseDefaultCoins:  true,
		CustomCoins:      "",
		SystemPromptTemplate: traderConfig.PromptTemplate,
	}

	// 保存到数据库
	if err := ttm.db.CreateTgTrader(telegramID, tgTraderRecord); err != nil {
		return "", fmt.Errorf("创建TG交易员失败: %w", err)
	}

	log.Printf("✅ 成功创建TG交易员: %s", traderID)
	return traderID, nil
}

// StartTrader 启动交易员 - 重构版本，确保状态同步
func (ttm *TelegramTraderManager) StartTrader(telegramID int64) error {
	// 1. 获取用户的TG交易员
	traders, err := ttm.db.GetTgTraders(telegramID)
	if err != nil {
		return fmt.Errorf("获取交易员失败: %w", err)
	}

	if len(traders) == 0 {
		return fmt.Errorf("您还没有创建交易员，请先使用 /create_trader 创建交易员")
	}

	trader := traders[0]

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

	// 3. 获取或创建TG交易员实例
	traderObj, err := ttm.GetTgTrader(trader.ID)
	if err != nil {
		return fmt.Errorf("获取TG交易员实例失败: %w", err)
	}

	// 4. 确保TelegramBotManager已设置
	ttm.setupTelegramBotManagerForTrader(traderObj)

	// 5. 启动交易员（同步等待启动成功）
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
		"has_trader":             true,
		"id":                    trader.ID,
		"name":                  trader.Name,
		"is_running":            actualRunningState, // 使用实际运行状态
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
		status["status"] = "运行中"

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
		status["current_balance"] = "已停止"
		status["positions_count"] = "0"
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
	if err := ttm.traderMgr.LoadTGTradersFromDatabase(database); err != nil {
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