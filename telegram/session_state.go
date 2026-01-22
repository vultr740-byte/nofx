package telegram

import (
	"log"
	"sync"
	"time"
)

// SessionState 会话状态类型
type SessionState string

const (
	StateIdle                SessionState = "idle"                  // 空闲状态
	StateCreatingTrader      SessionState = "creating_trader"       // 创建交易员中
	StateChoosingPrompt      SessionState = "choosing_prompt"       // 选择提示词模板
	StateChoosingAIModel     SessionState = "choosing_ai_model"     // 选择AI模型提供商
	StateSettingAPIKey       SessionState = "setting_api_key"       // 设置 API KEY
	StateUpdatingAIProvider  SessionState = "updating_ai_provider"  // 更新 AI 提供商
	StateUpdatingAPIKey      SessionState = "updating_api_key"      // 更新 API KEY
	StateSettingBalance      SessionState = "setting_balance"       // 设置初始资金
	StateSettingRisk         SessionState = "setting_risk"          // 设置风险级别
	StateSettingLeverage     SessionState = "setting_leverage"      // 设置杠杆倍数
	StateSettingInterval     SessionState = "setting_interval"      // 设置扫描间隔
	StateConfirm             SessionState = "confirm"               // 确认配置
	StateQuickSetup          SessionState = "quick_setup"           // 快速配置（选择风险级别）
	StateEditingCustomPrompt SessionState = "editing_custom_prompt" // 编辑自定义 Prompt
)

// PromptTemplate 提示词模板配置
type PromptTemplate struct {
	Name                string
	DisplayName         string
	PlainName           string
	Description         string
	RiskLevel           string
	BTCETHLeverage      int
	AltcoinLeverage     int
	ScanIntervalMinutes int
}

// TraderConfig 交易员配置
type TraderConfig struct {
	Step                SessionState
	PromptTemplate      string
	InitialBalance      float64
	RiskLevel           string
	BTCETHLeverage      int
	AltcoinLeverage     int
	ScanIntervalMinutes int
	AIProvider          string // deepseek / qwen
	AIModelName         string // 自定义模型名称
	AIModelAPIKey       string // AI 模型 API KEY (对应数据库中的 ai_model_api_key 字段)
	AIModelAPIURL       string // AI 模型 API URL (对应数据库中的 ai_model_api_url 字段)
	// 预留扩展字段
	CustomParams map[string]interface{}
}

// UserSession 用户会话
type UserSession struct {
	TelegramID    int64
	State         SessionState
	TraderConfig  *TraderConfig
	LastActivity  time.Time
	ExpiresAt     time.Time
	LastMessageID int // 存储最后一条消息的ID，用于删除
}

// SessionManager 会话管理器
type SessionManager struct {
	sessions map[int64]*UserSession
	mutex    sync.RWMutex
}

// NewSessionManager 创建会话管理器
func NewSessionManager() *SessionManager {
	return &SessionManager{
		sessions: make(map[int64]*UserSession),
	}
}

// GetOrCreateSession 获取或创建用户会话
func (sm *SessionManager) GetOrCreateSession(telegramID int64) *UserSession {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()

	session, exists := sm.sessions[telegramID]
	if !exists || session.isExpired() {
		log.Printf("🔍 SessionManager: Creating new session for telegramID=%d (exists=%t, expired=%t)", telegramID, exists, !exists && session != nil && session.isExpired())
		session = &UserSession{
			TelegramID: telegramID,
			State:      StateIdle,
			TraderConfig: &TraderConfig{
				AIProvider: "deepseek",
			},
			LastActivity: time.Now(),
			ExpiresAt:    time.Now().Add(30 * time.Minute), // 30分钟过期
		}
		sm.sessions[telegramID] = session
	} else {
		log.Printf("🔍 SessionManager: Retrieved existing session for telegramID=%d, state=%s", telegramID, session.State)
	}

	session.LastActivity = time.Now()
	return session
}

// UpdateSessionState 更新会话状态
func (sm *SessionManager) UpdateSessionState(telegramID int64, state SessionState) {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()

	if session, exists := sm.sessions[telegramID]; exists {
		log.Printf("🔄 SessionManager: Updating session state for telegramID=%d from %s to %s", telegramID, session.State, state)
		session.State = state
		session.LastActivity = time.Now()
		session.ExpiresAt = time.Now().Add(30 * time.Minute)
	} else {
		log.Printf("⚠️ SessionManager: Tried to update state for non-existent session telegramID=%d", telegramID)
	}
}

// UpdateTraderConfig 更新交易员配置
func (sm *SessionManager) UpdateTraderConfig(telegramID int64, config *TraderConfig) {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()

	if session, exists := sm.sessions[telegramID]; exists {
		session.TraderConfig = config
		session.LastActivity = time.Now()
		session.ExpiresAt = time.Now().Add(30 * time.Minute)
	}
}

// UpdateLastMessageID 更新最后一条消息的ID
func (sm *SessionManager) UpdateLastMessageID(telegramID int64, messageID int) {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()

	if session, exists := sm.sessions[telegramID]; exists {
		session.LastMessageID = messageID
		session.LastActivity = time.Now()
	}
}

// ClearSession 清除会话
func (sm *SessionManager) ClearSession(telegramID int64) {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()

	delete(sm.sessions, telegramID)
}

// CleanupExpiredSessions 清理过期会话
func (sm *SessionManager) CleanupExpiredSessions() {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()

	for telegramID, session := range sm.sessions {
		if session.isExpired() {
			delete(sm.sessions, telegramID)
		}
	}
}

// GetSessionStateOnly 获取当前会话状态（不过期返回StateIdle）
func (sm *SessionManager) GetSessionStateOnly(telegramID int64) SessionState {
	sm.mutex.RLock()
	defer sm.mutex.RUnlock()

	if session, exists := sm.sessions[telegramID]; exists {
		if session.isExpired() {
			return StateIdle
		}
		return session.State
	}
	return StateIdle
}

// isExpired 检查会话是否过期
func (us *UserSession) isExpired() bool {
	return time.Now().After(us.ExpiresAt)
}

// GetAvailablePromptTemplates 获取可用的提示词模板
func GetAvailablePromptTemplates() []PromptTemplate {
	return []PromptTemplate{
		{
			Name:                "default",
			DisplayName:         "🛡️ 结构趋势策略",
			PlainName:           "结构趋势策略",
			Description:         "结构驱动多周期趋势跟随，强调趋势共振与关键位入场；ATR风控、保本移动止损、低频高纪律",
			RiskLevel:           "保守",
			BTCETHLeverage:      3,
			AltcoinLeverage:     2,
			ScanIntervalMinutes: 15,
		},
		{
			Name:                "structure_trend",
			DisplayName:         "🧭 结构趋势（RSI过滤）",
			PlainName:           "结构趋势（RSI过滤）",
			Description:         "结构趋势共振 + RSI过滤极端/背离，15m关键位入场，ATR风控与移动止损",
			RiskLevel:           "标准",
			BTCETHLeverage:      5,
			AltcoinLeverage:     3,
			ScanIntervalMinutes: 15,
		},
	}
}

// GetRiskLevels 获取风险级别选项
func GetRiskLevels() []string {
	return []string{"保守", "标准", "激进"}
}

// GetBalanceOptions 获取初始资金选项
func GetBalanceOptions() []float64 {
	return []float64{500, 1000, 2000, 5000}
}

// GetLeverageOptions 获取杠杆倍数选项
func GetLeverageOptions() map[string][]int {
	return map[string][]int{
		"BTC/ETH": {1, 2, 3, 5, 7, 10},
		"山寨币":     {1, 2, 3, 4, 5},
	}
}

// GetIntervalOptions 获取扫描间隔选项
func GetIntervalOptions() []int {
	return []int{1, 3, 5, 10, 15, 30, 60} // 分钟
}
