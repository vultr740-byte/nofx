package telegram

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"time"
)

// TGUser Telegram 用户结构体
type TGUser struct {
	ID                string          `json:"id"`
	TelegramID        int64           `json:"telegram_id"`
	TelegramUsername  string          `json:"telegram_username"`
	TelegramFirstName string          `json:"telegram_first_name"`
	TelegramChatID    int64           `json:"telegram_chat_id"`
	LanguageCode      string          `json:"language_code"`
	CurrentAction     string          `json:"current_action"`
	SessionData       json.RawMessage `json:"session_data"`
	NotificationEnabled bool          `json:"notification_enabled"`
	LastInteractionAt *time.Time      `json:"last_interaction_at"`
	IsActive          bool            `json:"is_active"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
}

// TGUserDatabaseInterface Telegram 用户数据库接口
type TGUserDatabaseInterface interface {
	CreateTGUser(user *TGUser) error
	GetTGUserByTelegramID(telegramID int64) (*TGUser, error)
	GetTGUserByChatID(chatID int64) (*TGUser, error)
	UpdateTGUserSession(telegramID int64, sessionData interface{}) error
	UpdateTGUserAction(telegramID int64, action string) error
	UpdateTGUserLastInteraction(telegramID int64) error
}

// TGUserService Telegram 用户服务
type TGUserService struct {
	db *sql.DB
	usePostgreSQL bool
}

// NewTGUserService 创建 Telegram 用户服务
func NewTGUserService(db *sql.DB, usePostgreSQL bool) *TGUserService {
	return &TGUserService{
		db: db,
		usePostgreSQL: usePostgreSQL,
	}
}

// CreateTGUserTable 创建 tg_users 表
func (s *TGUserService) CreateTGUserTable() error {
	var query string
	if s.usePostgreSQL {
		query = `
		CREATE TABLE IF NOT EXISTS tg_users (
			id TEXT PRIMARY KEY DEFAULT uuid_generate_v4(),
			telegram_id BIGINT UNIQUE NOT NULL,
			telegram_username VARCHAR(255),
			telegram_first_name VARCHAR(255),
			telegram_chat_id BIGINT NOT NULL,
			language_code VARCHAR(10) DEFAULT 'en',
			current_action VARCHAR(100) DEFAULT 'idle',
			session_data JSONB DEFAULT '{}',
			session_expires_at TIMESTAMPTZ,
			notification_enabled BOOLEAN DEFAULT TRUE,
			last_interaction_at TIMESTAMPTZ,
			is_active BOOLEAN DEFAULT TRUE,
			created_at TIMESTAMPTZ DEFAULT NOW(),
			updated_at TIMESTAMPTZ DEFAULT NOW()
		);

		-- 创建索引
		CREATE INDEX IF NOT EXISTS idx_tg_users_telegram_id ON tg_users(telegram_id);
		CREATE INDEX IF NOT EXISTS idx_tg_users_chat_id ON tg_users(telegram_chat_id);
		CREATE INDEX IF NOT EXISTS idx_tg_users_current_action ON tg_users(current_action);
		CREATE INDEX IF NOT EXISTS idx_tg_users_is_active ON tg_users(is_active);

		-- 创建更新时间触发器
		CREATE OR REPLACE FUNCTION update_tg_users_updated_at()
		RETURNS TRIGGER AS $$
		BEGIN
			NEW.updated_at = NOW();
			RETURN NEW;
		END;
		$$ language 'plpgsql';

		DROP TRIGGER IF EXISTS update_tg_users_updated_at ON tg_users;
		CREATE TRIGGER update_tg_users_updated_at
		BEFORE UPDATE ON tg_users
		FOR EACH ROW EXECUTE FUNCTION update_tg_users_updated_at();
		`
	} else {
		query = `
		CREATE TABLE IF NOT EXISTS tg_users (
			id TEXT PRIMARY KEY,
			telegram_id INTEGER UNIQUE NOT NULL,
			telegram_username TEXT,
			telegram_first_name TEXT,
			telegram_chat_id INTEGER NOT NULL,
			language_code TEXT DEFAULT 'en',
			current_action TEXT DEFAULT 'idle',
			session_data TEXT DEFAULT '{}',
			session_expires_at DATETIME,
			notification_enabled BOOLEAN DEFAULT 1,
			last_interaction_at DATETIME,
			is_active BOOLEAN DEFAULT 1,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);

		-- 创建索引
		CREATE INDEX IF NOT EXISTS idx_tg_users_telegram_id ON tg_users(telegram_id);
		CREATE INDEX IF NOT EXISTS idx_tg_users_chat_id ON tg_users(telegram_chat_id);
		CREATE INDEX IF NOT EXISTS idx_tg_users_current_action ON tg_users(current_action);
		CREATE INDEX IF NOT EXISTS idx_tg_users_is_active ON tg_users(is_active);

		-- 创建更新触发器
		CREATE TRIGGER IF NOT EXISTS update_tg_users_updated_at
			AFTER UPDATE ON tg_users
			BEGIN
				UPDATE tg_users SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
			END;
		`
	}

	_, err := s.db.Exec(query)
	if err != nil {
		return fmt.Errorf("创建 tg_users 表失败: %w", err)
	}

	log.Printf("✅ tg_users 表创建成功")
	return nil
}

// CreateTGUser 创建 Telegram 用户
func (s *TGUserService) CreateTGUser(user *TGUser) error {
	sessionDataJSON, err := json.Marshal(user.SessionData)
	if err != nil {
		return fmt.Errorf("序列化 session_data 失败: %w", err)
	}

	var query string
	if s.usePostgreSQL {
		query = `
			INSERT INTO tg_users (telegram_id, telegram_username, telegram_first_name, telegram_chat_id,
			                      language_code, current_action, session_data, notification_enabled, is_active)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		`
		_, err = s.db.Exec(query, user.TelegramID, user.TelegramUsername, user.TelegramFirstName,
			user.TelegramChatID, user.LanguageCode, user.CurrentAction, sessionDataJSON,
			user.NotificationEnabled, user.IsActive)
	} else {
		query = `
			INSERT INTO tg_users (telegram_id, telegram_username, telegram_first_name, telegram_chat_id,
			                      language_code, current_action, session_data, notification_enabled, is_active)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		`
		_, err = s.db.Exec(query, user.TelegramID, user.TelegramUsername, user.TelegramFirstName,
			user.TelegramChatID, user.LanguageCode, user.CurrentAction, sessionDataJSON,
			user.NotificationEnabled, user.IsActive)
	}

	if err != nil {
		return fmt.Errorf("创建 TGUser 失败: %w", err)
	}

	return nil
}

// GetTGUserByTelegramID 通过 Telegram ID 获取用户
func (s *TGUserService) GetTGUserByTelegramID(telegramID int64) (*TGUser, error) {
	var user TGUser
	var query string

	if s.usePostgreSQL {
		query = `
			SELECT id, telegram_id, telegram_username, telegram_first_name, telegram_chat_id,
			       language_code, current_action, session_data, notification_enabled,
			       last_interaction_at, is_active, created_at, updated_at
			FROM tg_users WHERE telegram_id = $1
		`
		err := s.db.QueryRow(query, telegramID).Scan(
			&user.ID, &user.TelegramID, &user.TelegramUsername, &user.TelegramFirstName,
			&user.TelegramChatID, &user.LanguageCode, &user.CurrentAction, &user.SessionData,
			&user.NotificationEnabled, &user.LastInteractionAt, &user.IsActive,
			&user.CreatedAt, &user.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
	} else {
		query = `
			SELECT id, telegram_id, telegram_username, telegram_first_name, telegram_chat_id,
			       language_code, current_action, session_data, notification_enabled,
			       last_interaction_at, is_active, created_at, updated_at
			FROM tg_users WHERE telegram_id = ?
		`
		err := s.db.QueryRow(query, telegramID).Scan(
			&user.ID, &user.TelegramID, &user.TelegramUsername, &user.TelegramFirstName,
			&user.TelegramChatID, &user.LanguageCode, &user.CurrentAction, &user.SessionData,
			&user.NotificationEnabled, &user.LastInteractionAt, &user.IsActive,
			&user.CreatedAt, &user.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
	}

	return &user, nil
}

// GetTGUserByChatID 通过 Chat ID 获取用户
func (s *TGUserService) GetTGUserByChatID(chatID int64) (*TGUser, error) {
	var user TGUser
	var query string

	if s.usePostgreSQL {
		query = `
			SELECT id, telegram_id, telegram_username, telegram_first_name, telegram_chat_id,
			       language_code, current_action, session_data, notification_enabled,
			       last_interaction_at, is_active, created_at, updated_at
			FROM tg_users WHERE telegram_chat_id = $1
		`
		err := s.db.QueryRow(query, chatID).Scan(
			&user.ID, &user.TelegramID, &user.TelegramUsername, &user.TelegramFirstName,
			&user.TelegramChatID, &user.LanguageCode, &user.CurrentAction, &user.SessionData,
			&user.NotificationEnabled, &user.LastInteractionAt, &user.IsActive,
			&user.CreatedAt, &user.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
	} else {
		query = `
			SELECT id, telegram_id, telegram_username, telegram_first_name, telegram_chat_id,
			       language_code, current_action, session_data, notification_enabled,
			       last_interaction_at, is_active, created_at, updated_at
			FROM tg_users WHERE telegram_chat_id = ?
		`
		err := s.db.QueryRow(query, chatID).Scan(
			&user.ID, &user.TelegramID, &user.TelegramUsername, &user.TelegramFirstName,
			&user.TelegramChatID, &user.LanguageCode, &user.CurrentAction, &user.SessionData,
			&user.NotificationEnabled, &user.LastInteractionAt, &user.IsActive,
			&user.CreatedAt, &user.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
	}

	return &user, nil
}

// UpdateTGUserSession 更新用户会话数据
func (s *TGUserService) UpdateTGUserSession(telegramID int64, sessionData interface{}) error {
	sessionDataJSON, err := json.Marshal(sessionData)
	if err != nil {
		return fmt.Errorf("序列化 session_data 失败: %w", err)
	}

	var query string
	if s.usePostgreSQL {
		query = `
			UPDATE tg_users
			SET session_data = $1, last_interaction_at = NOW()
			WHERE telegram_id = $2
		`
		_, err = s.db.Exec(query, sessionDataJSON, telegramID)
	} else {
		query = `
			UPDATE tg_users
			SET session_data = ?, last_interaction_at = CURRENT_TIMESTAMP
			WHERE telegram_id = ?
		`
		_, err = s.db.Exec(query, sessionDataJSON, telegramID)
	}

	return err
}

// UpdateTGUserAction 更新用户当前操作
func (s *TGUserService) UpdateTGUserAction(telegramID int64, action string) error {
	var query string
	if s.usePostgreSQL {
		query = `
			UPDATE tg_users
			SET current_action = $1, last_interaction_at = NOW()
			WHERE telegram_id = $2
		`
		_, err := s.db.Exec(query, action, telegramID)
		return err
	} else {
		query = `
			UPDATE tg_users
			SET current_action = ?, last_interaction_at = CURRENT_TIMESTAMP
			WHERE telegram_id = ?
		`
		_, err := s.db.Exec(query, action, telegramID)
		return err
	}
}

// UpdateTGUserLastInteraction 更新用户最后交互时间
func (s *TGUserService) UpdateTGUserLastInteraction(telegramID int64) error {
	var query string
	if s.usePostgreSQL {
		query = `UPDATE tg_users SET last_interaction_at = NOW() WHERE telegram_id = $1`
		_, err := s.db.Exec(query, telegramID)
		return err
	} else {
		query = `UPDATE tg_users SET last_interaction_at = CURRENT_TIMESTAMP WHERE telegram_id = ?`
		_, err := s.db.Exec(query, telegramID)
		return err
	}
}

// HasHyperliquidAccount 检查用户是否有 Hyperliquid 账号
func (s *TGUserService) HasHyperliquidAccount(sessionData json.RawMessage) bool {
	if sessionData == nil {
		return false
	}

	var data map[string]interface{}
	if err := json.Unmarshal(sessionData, &data); err != nil {
		return false
	}

	_, hasAgentKey := data["agent_key"]
	_, hasWalletAddr := data["wallet_address"]

	return hasAgentKey && hasWalletAddr
}

// ExtractHyperliquidConfig 从 session_data 中提取 Hyperliquid 配置
func (s *TGUserService) ExtractHyperliquidConfig(sessionData json.RawMessage) (map[string]interface{}, error) {
	if sessionData == nil {
		return nil, fmt.Errorf("session_data 为空")
	}

	var data map[string]interface{}
	if err := json.Unmarshal(sessionData, &data); err != nil {
		return nil, fmt.Errorf("解析 session_data 失败: %w", err)
	}

	config := make(map[string]interface{})

	if agentKey, ok := data["agent_key"].(string); ok {
		config["agent_key"] = agentKey
	} else {
		return nil, fmt.Errorf("未找到 agent_key")
	}

	if walletAddr, ok := data["wallet_address"].(string); ok {
		config["wallet_address"] = walletAddr
	} else {
		return nil, fmt.Errorf("未找到 wallet_address")
	}

	// 默认主网
	config["testnet"] = false

	return config, nil
}