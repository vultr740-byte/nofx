package config

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"nofx/crypto"
	"nofx/market"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

// DatabaseInterface 定义了数据库实现需要提供的方法集合
type DatabaseInterface interface {
	SetCryptoService(cs *crypto.CryptoService)
	CreateUser(user *User) error
	GetUserByEmail(email string) (*User, error)
	GetUserByID(userID string) (*User, error)
	GetAllUsers() ([]string, error)
	UpdateUserOTPVerified(userID string, verified bool) error
	GetAIModels(userID string) ([]*AIModelConfig, error)
	UpdateAIModel(userID, id string, enabled bool, apiKey, customAPIURL, customModelName string) error
	GetExchanges(userID string) ([]*ExchangeConfig, error)
	UpdateExchange(userID, id string, enabled bool, apiKey, secretKey string, testnet bool, hyperliquidWalletAddr, asterUser, asterSigner, asterPrivateKey, customExchangeName string) error
	CreateAIModel(userID, id, name, provider string, enabled bool, apiKey, customAPIURL string) error
	CreateExchange(userID, id, name, typ string, enabled bool, apiKey, secretKey string, testnet bool, hyperliquidWalletAddr, asterUser, asterSigner, asterPrivateKey string) error
	CreateTrader(trader *TraderRecord) error
	GetTraders(userID string) ([]*TraderRecord, error)
	UpdateTraderStatus(userID, id string, isRunning bool) error
	UpdateTrader(trader *TraderRecord) error
	UpdateTraderInitialBalance(userID, id string, newBalance float64) error
	UpdateTraderCustomPrompt(userID, id string, customPrompt string, overrideBase bool) error
	DeleteTrader(userID, id string) error
	GetTraderConfig(userID, traderID string) (*TraderRecord, *AIModelConfig, *ExchangeConfig, error)
	GetSystemConfig(key string) (string, error)
	SetSystemConfig(key, value string) error
	CreateUserSignalSource(userID, coinPoolURL, oiTopURL string) error
	GetUserSignalSource(userID string) (*UserSignalSource, error)
	UpdateUserSignalSource(userID, coinPoolURL, oiTopURL string) error
	GetCustomCoins() []string
	LoadBetaCodesFromFile(filePath string) error
	EnsureUserInUsersTable(userID string) error
	ValidateBetaCode(code string) (bool, error)
	UseBetaCode(code, userEmail string) error
	GetBetaCodeStats() (total, used int, err error)
	// Telegram 用户相关方法
	CreateTGUserTable() error
	CreateTGUser(telegramID int64, username, firstName string, chatID int64, languageCode string) error
	GetTGUsers() ([]string, error)
	GetAllTGUsers() ([]int64, error)
	GetTGUserByTelegramID(telegramID int64) (interface{}, error)
	GetTGUserByChatID(chatID int64) (interface{}, error)
	UpdateTGUserAction(telegramID int64, action string) error
	UpdateTGUserLastInteraction(telegramID int64) error
	// TG交易员相关方法
	CreateTgTrader(tgUserID int64, traderRecord *TgTraderRecord) error
	GetTgTraders(tgUserID int64) ([]TgTraderRecord, error)
	UpdateTgTraderStatus(tgUserID int64, traderID string, isRunning bool) error
	UpdateTgTraderInitialBalance(tgUserID int64, traderID string, newBalance float64) error
	UpdateTgTraderAPIConfig(tgUserID int64, traderID string, aiModelID string, apiKey string, aiModel string) error
	CreateTgGasSponsorship(record *TgGasSponsorshipRecord) (int64, error)
	UpdateTgGasSponsorshipProgress(id int64, status string, gasTxHash string, bridgeTxHash string, usdcAmount string) error
	GetActiveGasSponsorship(walletAddr string) (*TgGasSponsorshipRecord, error)
	HasRecentGasSponsorship(walletAddr string, withinHours int) (bool, error)
	UpdateTgTraderConfig(tgUserID int64, traderID string, traderRecord *TgTraderRecord) error
	DeleteTgTrader(tgUserID int64, traderID string) error
	GetTgTraderConfig(tgUserID int64, traderID string) (*TgTraderRecord, error)
	Close() error
}

// Database 配置数据库
type Database struct {
	db            *sql.DB
	cryptoService *crypto.CryptoService
	usePostgreSQL bool // 是否使用 PostgreSQL (Supabase)
}

// NewDatabase 创建配置数据库
func NewDatabase(dbPath string) (*Database, error) {
	// 检查是否使用 Supabase
	supabaseURL := os.Getenv("SUPABASE_URL")
	if supabaseURL != "" {
		// 使用 Supabase
		log.Printf("🔄 使用 Supabase 数据库")
		db, err := sql.Open("pgx", supabaseURL)
		if err != nil {
			return nil, fmt.Errorf("连接 Supabase 失败: %w", err)
		}

		// 测试连接
		if err := db.Ping(); err != nil {
			return nil, fmt.Errorf("Supabase 连接测试失败: %w", err)
		}

		database := &Database{db: db, usePostgreSQL: true}
		if err := database.createTables(); err != nil {
			return nil, fmt.Errorf("创建表失败: %w", err)
		}

		if err := database.initDefaultData(); err != nil {
			return nil, fmt.Errorf("初始化默认数据失败: %w", err)
		}

		log.Printf("✓ Supabase 数据库连接成功")
		return database, nil
	}

	// 使用 SQLite（原有逻辑）
	log.Printf("🔄 使用 SQLite 数据库: %s", dbPath)
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}

	database := &Database{db: db, usePostgreSQL: false}
	if err := database.createTables(); err != nil {
		return nil, fmt.Errorf("创建表失败: %w", err)
	}

	if err := database.initDefaultData(); err != nil {
		return nil, fmt.Errorf("初始化默认数据失败: %w", err)
	}

	log.Printf("✓ SQLite 数据库连接成功")
	return database, nil
}

// createTables 创建数据库表
func (d *Database) createTables() error {
	// 选择合适的 SQL 语法
	if d.usePostgreSQL {
		return d.createPostgreSQLTables()
	}
	return d.createSQLiteTables()
}

// createPostgreSQLTables 创建 PostgreSQL 表
func (d *Database) createPostgreSQLTables() error {
	queries := []string{
		// 启用 UUID 扩展
		`CREATE EXTENSION IF NOT EXISTS "uuid-ossp"`,

		// 用户表
		`CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY DEFAULT uuid_generate_v4(),
			email TEXT UNIQUE NOT NULL,
			password_hash TEXT NOT NULL,
			otp_secret TEXT,
			otp_verified BOOLEAN DEFAULT FALSE,
			created_at TIMESTAMPTZ DEFAULT NOW(),
			updated_at TIMESTAMPTZ DEFAULT NOW()
		)`,

		// AI模型配置表
		`CREATE TABLE IF NOT EXISTS ai_models (
			id TEXT PRIMARY KEY DEFAULT uuid_generate_v4(),
			user_id TEXT NOT NULL DEFAULT 'default',
			name TEXT NOT NULL,
			provider TEXT NOT NULL,
			enabled BOOLEAN DEFAULT FALSE,
			api_key TEXT DEFAULT '',
			custom_api_url TEXT DEFAULT '',
			custom_model_name TEXT DEFAULT '',
			created_at TIMESTAMPTZ DEFAULT NOW(),
			updated_at TIMESTAMPTZ DEFAULT NOW(),
			FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
		)`,

		// 交易所配置表
		`CREATE TABLE IF NOT EXISTS exchanges (
			id TEXT PRIMARY KEY DEFAULT uuid_generate_v4(),
			user_id TEXT NOT NULL DEFAULT 'default',
			name TEXT NOT NULL,
			type TEXT NOT NULL,
			enabled BOOLEAN DEFAULT FALSE,
			api_key TEXT DEFAULT '',
			secret_key TEXT DEFAULT '',
			testnet BOOLEAN DEFAULT FALSE,
			hyperliquid_wallet_addr TEXT DEFAULT '',
			aster_user TEXT DEFAULT '',
			aster_signer TEXT DEFAULT '',
			aster_private_key TEXT DEFAULT '',
			custom_exchange_name TEXT DEFAULT '',
			created_at TIMESTAMPTZ DEFAULT NOW(),
			updated_at TIMESTAMPTZ DEFAULT NOW(),
			FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
		)`,

		// 用户信号源配置表
		`CREATE TABLE IF NOT EXISTS user_signal_sources (
			id TEXT PRIMARY KEY DEFAULT uuid_generate_v4(),
			user_id TEXT NOT NULL,
			coin_pool_url TEXT DEFAULT '',
			oi_top_url TEXT DEFAULT '',
			created_at TIMESTAMPTZ DEFAULT NOW(),
			updated_at TIMESTAMPTZ DEFAULT NOW(),
			FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
			UNIQUE(user_id)
		)`,

		// 交易员配置表
		`CREATE TABLE IF NOT EXISTS traders (
			id TEXT PRIMARY KEY DEFAULT uuid_generate_v4(),
			user_id TEXT NOT NULL DEFAULT 'default',
			name TEXT NOT NULL,
			ai_model_id TEXT NOT NULL,
			exchange_id TEXT NOT NULL,
			initial_balance DECIMAL(20,8) NOT NULL,
			scan_interval_minutes INTEGER DEFAULT 3,
			is_running BOOLEAN DEFAULT FALSE,
			btc_eth_leverage INTEGER DEFAULT 5,
			altcoin_leverage INTEGER DEFAULT 5,
			trading_symbols TEXT DEFAULT '',
			use_coin_pool BOOLEAN DEFAULT FALSE,
			use_oi_top BOOLEAN DEFAULT FALSE,
			custom_prompt TEXT DEFAULT '',
			override_base_prompt BOOLEAN DEFAULT FALSE,
			is_cross_margin BOOLEAN DEFAULT TRUE,
			use_default_coins BOOLEAN DEFAULT TRUE,
			custom_coins TEXT DEFAULT '',
			system_prompt_template TEXT DEFAULT 'default',
			created_at TIMESTAMPTZ DEFAULT NOW(),
			updated_at TIMESTAMPTZ DEFAULT NOW(),
			FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
			FOREIGN KEY (ai_model_id) REFERENCES ai_models(id),
			FOREIGN KEY (exchange_id) REFERENCES exchanges(id)
		)`,

		// 系统配置表
		`CREATE TABLE IF NOT EXISTS system_config (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at TIMESTAMPTZ DEFAULT NOW()
		)`,

		// 内测码表
		`CREATE TABLE IF NOT EXISTS beta_codes (
			code TEXT PRIMARY KEY,
			used BOOLEAN DEFAULT FALSE,
			used_by TEXT DEFAULT '',
			used_at TIMESTAMPTZ,
			created_at TIMESTAMPTZ DEFAULT NOW()
		)`,

		// 审计日志表
		`CREATE TABLE IF NOT EXISTS audit_logs (
			id TEXT PRIMARY KEY DEFAULT uuid_generate_v4(),
			user_id TEXT NOT NULL,
			action TEXT NOT NULL,
			resource TEXT NOT NULL,
			details TEXT,
			ip_address TEXT,
			user_agent TEXT,
			timestamp TIMESTAMPTZ DEFAULT NOW()
		)`,

		// 创建更新时间触发器函数
		`CREATE OR REPLACE FUNCTION update_updated_at_column()
		RETURNS TRIGGER AS $$
		BEGIN
			NEW.updated_at = NOW();
			RETURN NEW;
		END;
		$$ language 'plpgsql'`,

		// 为需要的表创建触发器（忽略已存在的错误）
		`DROP TRIGGER IF EXISTS update_users_updated_at ON users`,
		`CREATE TRIGGER update_users_updated_at
		BEFORE UPDATE ON users
		FOR EACH ROW EXECUTE FUNCTION update_updated_at_column()`,

		`DROP TRIGGER IF EXISTS update_ai_models_updated_at ON ai_models`,
		`CREATE TRIGGER update_ai_models_updated_at
		BEFORE UPDATE ON ai_models
		FOR EACH ROW EXECUTE FUNCTION update_updated_at_column()`,

		`DROP TRIGGER IF EXISTS update_exchanges_updated_at ON exchanges`,
		`CREATE TRIGGER update_exchanges_updated_at
		BEFORE UPDATE ON exchanges
		FOR EACH ROW EXECUTE FUNCTION update_updated_at_column()`,

		`DROP TRIGGER IF EXISTS update_traders_updated_at ON traders`,
		`CREATE TRIGGER update_traders_updated_at
		BEFORE UPDATE ON traders
		FOR EACH ROW EXECUTE FUNCTION update_updated_at_column()`,

		`DROP TRIGGER IF EXISTS update_user_signal_sources_updated_at ON user_signal_sources`,
		`CREATE TRIGGER update_user_signal_sources_updated_at
		BEFORE UPDATE ON user_signal_sources
		FOR EACH ROW EXECUTE FUNCTION update_updated_at_column()`,

		`DROP TRIGGER IF EXISTS update_system_config_updated_at ON system_config`,
		`CREATE TRIGGER update_system_config_updated_at
		BEFORE UPDATE ON system_config
		FOR EACH ROW EXECUTE FUNCTION update_updated_at_column()`,

		// Telegram 用户表
		`CREATE TABLE IF NOT EXISTS tg_users (
			id TEXT PRIMARY KEY DEFAULT uuid_generate_v4(),
			telegram_id BIGINT UNIQUE NOT NULL,
			telegram_username VARCHAR(255),
			telegram_first_name VARCHAR(255),
			telegram_chat_id BIGINT NOT NULL,
			language_code VARCHAR(10) DEFAULT 'en',
			current_action VARCHAR(100) DEFAULT 'idle',
			session_expires_at TIMESTAMPTZ,
			notification_enabled BOOLEAN DEFAULT TRUE,
			last_interaction_at TIMESTAMPTZ,
			is_active BOOLEAN DEFAULT TRUE,
			created_at TIMESTAMPTZ DEFAULT NOW(),
			updated_at TIMESTAMPTZ DEFAULT NOW()
		)`,

		// Telegram 用户表索引
		`CREATE INDEX IF NOT EXISTS idx_tg_users_telegram_id ON tg_users(telegram_id)`,
		`CREATE INDEX IF NOT EXISTS idx_tg_users_chat_id ON tg_users(telegram_chat_id)`,
		`CREATE INDEX IF NOT EXISTS idx_tg_users_current_action ON tg_users(current_action)`,
		`CREATE INDEX IF NOT EXISTS idx_tg_users_is_active ON tg_users(is_active)`,

		// Gas sponsorship records
		`CREATE TABLE IF NOT EXISTS tg_gas_sponsorships (
			id BIGSERIAL PRIMARY KEY,
			tg_user_id BIGINT NOT NULL,
			wallet_address TEXT NOT NULL,
			amount_wei NUMERIC(78,0) NOT NULL,
			usdc_amount NUMERIC(78,0) NOT NULL DEFAULT 0,
			gas_tx_hash TEXT,
			bridge_tx_hash TEXT,
			status TEXT NOT NULL,
			created_at TIMESTAMPTZ DEFAULT NOW(),
			updated_at TIMESTAMPTZ DEFAULT NOW()
		)`,

		`CREATE INDEX IF NOT EXISTS idx_tg_gas_wallet ON tg_gas_sponsorships(wallet_address)`,
		`CREATE INDEX IF NOT EXISTS idx_tg_gas_user ON tg_gas_sponsorships(tg_user_id)`,

		// Telegram 用户表触发器
		`DROP TRIGGER IF EXISTS update_tg_users_updated_at ON tg_users`,
		`CREATE TRIGGER update_tg_users_updated_at
		BEFORE UPDATE ON tg_users
		FOR EACH ROW EXECUTE FUNCTION update_updated_at_column()`,
	}

	for _, query := range queries {
		if _, err := d.db.Exec(query); err != nil {
			return fmt.Errorf("执行PostgreSQL SQL失败 [%s]: %w", query, err)
		}
	}

	alterQueries := []string{
		`ALTER TABLE exchanges ADD COLUMN IF NOT EXISTS hyperliquid_wallet_addr TEXT DEFAULT ''`,
		`ALTER TABLE exchanges ADD COLUMN IF NOT EXISTS aster_user TEXT DEFAULT ''`,
		`ALTER TABLE exchanges ADD COLUMN IF NOT EXISTS aster_signer TEXT DEFAULT ''`,
		`ALTER TABLE exchanges ADD COLUMN IF NOT EXISTS aster_private_key TEXT DEFAULT ''`,
		`ALTER TABLE exchanges ADD COLUMN IF NOT EXISTS custom_exchange_name TEXT DEFAULT ''`,
		`ALTER TABLE traders ADD COLUMN IF NOT EXISTS custom_prompt TEXT DEFAULT ''`,
		`ALTER TABLE traders ADD COLUMN IF NOT EXISTS override_base_prompt BOOLEAN DEFAULT FALSE`,
		`ALTER TABLE traders ADD COLUMN IF NOT EXISTS is_cross_margin BOOLEAN DEFAULT TRUE`,
		`ALTER TABLE traders ADD COLUMN IF NOT EXISTS use_default_coins BOOLEAN DEFAULT TRUE`,
		`ALTER TABLE traders ADD COLUMN IF NOT EXISTS custom_coins TEXT DEFAULT ''`,
		`ALTER TABLE traders ADD COLUMN IF NOT EXISTS btc_eth_leverage INTEGER DEFAULT 5`,
		`ALTER TABLE traders ADD COLUMN IF NOT EXISTS altcoin_leverage INTEGER DEFAULT 5`,
		`ALTER TABLE traders ADD COLUMN IF NOT EXISTS trading_symbols TEXT DEFAULT ''`,
		`ALTER TABLE traders ADD COLUMN IF NOT EXISTS use_coin_pool BOOLEAN DEFAULT FALSE`,
		`ALTER TABLE traders ADD COLUMN IF NOT EXISTS use_oi_top BOOLEAN DEFAULT FALSE`,
		`ALTER TABLE traders ADD COLUMN IF NOT EXISTS system_prompt_template TEXT DEFAULT 'default'`,
		`ALTER TABLE ai_models ADD COLUMN IF NOT EXISTS custom_api_url TEXT DEFAULT ''`,
		`ALTER TABLE ai_models ADD COLUMN IF NOT EXISTS custom_model_name TEXT DEFAULT ''`,
		`ALTER TABLE tg_traders ADD COLUMN IF NOT EXISTS ai_model_api_url TEXT DEFAULT ''`,
		`ALTER TABLE tg_traders ADD COLUMN IF NOT EXISTS private_key TEXT DEFAULT ''`,
		`ALTER TABLE tg_traders ADD COLUMN IF NOT EXISTS wallet_address TEXT DEFAULT ''`,
		`ALTER TABLE tg_traders ADD COLUMN IF NOT EXISTS is_configured BOOLEAN DEFAULT FALSE`,
	}

	for _, query := range alterQueries {
		if _, err := d.db.Exec(query); err != nil {
			log.Printf("⚠️ 执行PostgreSQL ALTER失败: %v", err)
		}
	}

	d.migrateLegacyTGSessionData()
	d.dropTGUserSessionDataColumn()

	return nil
}

// createSQLiteTables 创建 SQLite 表（原有逻辑）
func (d *Database) createSQLiteTables() error {
	queries := []string{
		// AI模型配置表
		`CREATE TABLE IF NOT EXISTS ai_models (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL DEFAULT 'default',
			name TEXT NOT NULL,
			provider TEXT NOT NULL,
			enabled BOOLEAN DEFAULT 0,
			api_key TEXT DEFAULT '',
			custom_api_url TEXT DEFAULT '',
			custom_model_name TEXT DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
		)`,

		// 交易所配置表
		`CREATE TABLE IF NOT EXISTS exchanges (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL DEFAULT 'default',
			name TEXT NOT NULL,
			type TEXT NOT NULL,
			enabled BOOLEAN DEFAULT 0,
			api_key TEXT DEFAULT '',
			secret_key TEXT DEFAULT '',
			testnet BOOLEAN DEFAULT 0,
			hyperliquid_wallet_addr TEXT DEFAULT '',
			aster_user TEXT DEFAULT '',
			aster_signer TEXT DEFAULT '',
			aster_private_key TEXT DEFAULT '',
			custom_exchange_name TEXT DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
		)`,

		// 用户信号源配置表
		`CREATE TABLE IF NOT EXISTS user_signal_sources (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id TEXT NOT NULL,
			coin_pool_url TEXT DEFAULT '',
			oi_top_url TEXT DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
			UNIQUE(user_id)
		)`,

		// 交易员配置表
		`CREATE TABLE IF NOT EXISTS traders (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL DEFAULT 'default',
			name TEXT NOT NULL,
			ai_model_id TEXT NOT NULL,
			exchange_id TEXT NOT NULL,
			initial_balance REAL NOT NULL,
			scan_interval_minutes INTEGER DEFAULT 3,
			is_running BOOLEAN DEFAULT 0,
			btc_eth_leverage INTEGER DEFAULT 5,
			altcoin_leverage INTEGER DEFAULT 5,
			trading_symbols TEXT DEFAULT '',
			use_coin_pool BOOLEAN DEFAULT 0,
			use_oi_top BOOLEAN DEFAULT 0,
			custom_prompt TEXT DEFAULT '',
			override_base_prompt BOOLEAN DEFAULT 0,
			is_cross_margin BOOLEAN DEFAULT 1,
			use_default_coins BOOLEAN DEFAULT 1,
			custom_coins TEXT DEFAULT '',
			system_prompt_template TEXT DEFAULT 'default',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
			FOREIGN KEY (ai_model_id) REFERENCES ai_models(id),
			FOREIGN KEY (exchange_id) REFERENCES exchanges(id)
		)`,

		// 用户表
		`CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			email TEXT UNIQUE NOT NULL,
			password_hash TEXT NOT NULL,
			otp_secret TEXT,
			otp_verified BOOLEAN DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,

		// 系统配置表
		`CREATE TABLE IF NOT EXISTS system_config (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,

		// 内测码表
		`CREATE TABLE IF NOT EXISTS beta_codes (
			code TEXT PRIMARY KEY,
			used BOOLEAN DEFAULT 0,
			used_by TEXT DEFAULT '',
			used_at DATETIME DEFAULT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,

		// 触发器：自动更新 updated_at
		`CREATE TRIGGER IF NOT EXISTS update_users_updated_at
			AFTER UPDATE ON users
			BEGIN
				UPDATE users SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
			END`,

		`CREATE TRIGGER IF NOT EXISTS update_ai_models_updated_at
			AFTER UPDATE ON ai_models
			BEGIN
				UPDATE ai_models SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
			END`,

		`CREATE TRIGGER IF NOT EXISTS update_exchanges_updated_at
			AFTER UPDATE ON exchanges
			BEGIN
				UPDATE exchanges SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
			END`,

		`CREATE TRIGGER IF NOT EXISTS update_traders_updated_at
			AFTER UPDATE ON traders
			BEGIN
				UPDATE traders SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
			END`,

		`CREATE TRIGGER IF NOT EXISTS update_user_signal_sources_updated_at
			AFTER UPDATE ON user_signal_sources
			BEGIN
				UPDATE user_signal_sources SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
			END`,

		`CREATE TRIGGER IF NOT EXISTS update_system_config_updated_at
			AFTER UPDATE ON system_config
			BEGIN
				UPDATE system_config SET updated_at = CURRENT_TIMESTAMP WHERE key = NEW.key;
			END`,

		// Telegram 用户表
		`CREATE TABLE IF NOT EXISTS tg_users (
			id TEXT PRIMARY KEY,
			telegram_id INTEGER UNIQUE NOT NULL,
			telegram_username TEXT,
			telegram_first_name TEXT,
			telegram_chat_id INTEGER NOT NULL,
			language_code TEXT DEFAULT 'en',
			current_action TEXT DEFAULT 'idle',
			session_expires_at DATETIME,
			notification_enabled BOOLEAN DEFAULT 1,
			last_interaction_at DATETIME,
			is_active BOOLEAN DEFAULT 1,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,

		// Telegram 用户表索引
		`CREATE INDEX IF NOT EXISTS idx_tg_users_telegram_id ON tg_users(telegram_id)`,
		`CREATE INDEX IF NOT EXISTS idx_tg_users_chat_id ON tg_users(telegram_chat_id)`,
		`CREATE INDEX IF NOT EXISTS idx_tg_users_current_action ON tg_users(current_action)`,
		`CREATE INDEX IF NOT EXISTS idx_tg_users_is_active ON tg_users(is_active)`,

		// Telegram 用户表触发器
		`CREATE TRIGGER IF NOT EXISTS update_tg_users_updated_at
			AFTER UPDATE ON tg_users
			BEGIN
				UPDATE tg_users SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
			END`,

		// Gas sponsorship records
		`CREATE TABLE IF NOT EXISTS tg_gas_sponsorships (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tg_user_id INTEGER NOT NULL,
			wallet_address TEXT NOT NULL,
			amount_wei TEXT NOT NULL,
			usdc_amount TEXT NOT NULL DEFAULT '0',
			gas_tx_hash TEXT,
			bridge_tx_hash TEXT,
			status TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_tg_gas_wallet ON tg_gas_sponsorships(wallet_address)`,
		`CREATE INDEX IF NOT EXISTS idx_tg_gas_user ON tg_gas_sponsorships(tg_user_id)`,
		`CREATE TRIGGER IF NOT EXISTS update_tg_gas_sponsorships_updated_at
			AFTER UPDATE ON tg_gas_sponsorships
			BEGIN
				UPDATE tg_gas_sponsorships SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
			END`,
	}

	for _, query := range queries {
		if _, err := d.db.Exec(query); err != nil {
			return fmt.Errorf("执行SQLite SQL失败 [%s]: %w", query, err)
		}
	}

	// 为现有数据库添加新字段（向后兼容）
	alterQueries := []string{
		`ALTER TABLE exchanges ADD COLUMN hyperliquid_wallet_addr TEXT DEFAULT ''`,
		`ALTER TABLE exchanges ADD COLUMN aster_user TEXT DEFAULT ''`,
		`ALTER TABLE exchanges ADD COLUMN aster_signer TEXT DEFAULT ''`,
		`ALTER TABLE exchanges ADD COLUMN aster_private_key TEXT DEFAULT ''`,
		`ALTER TABLE exchanges ADD COLUMN custom_exchange_name TEXT DEFAULT ''`, // 自定义交易所名称
		`ALTER TABLE traders ADD COLUMN custom_prompt TEXT DEFAULT ''`,
		`ALTER TABLE traders ADD COLUMN override_base_prompt BOOLEAN DEFAULT 0`,
		`ALTER TABLE traders ADD COLUMN is_cross_margin BOOLEAN DEFAULT 1`,             // 默认为全仓模式
		`ALTER TABLE traders ADD COLUMN use_default_coins BOOLEAN DEFAULT 1`,           // 默认使用默认币种
		`ALTER TABLE traders ADD COLUMN custom_coins TEXT DEFAULT ''`,                  // 自定义币种列表（JSON格式）
		`ALTER TABLE traders ADD COLUMN btc_eth_leverage INTEGER DEFAULT 5`,            // BTC/ETH杠杆倍数
		`ALTER TABLE traders ADD COLUMN altcoin_leverage INTEGER DEFAULT 5`,            // 山寨币杠杆倍数
		`ALTER TABLE traders ADD COLUMN trading_symbols TEXT DEFAULT ''`,               // 交易币种，逗号分隔
		`ALTER TABLE traders ADD COLUMN use_coin_pool BOOLEAN DEFAULT 0`,               // 是否使用COIN POOL信号源
		`ALTER TABLE traders ADD COLUMN use_oi_top BOOLEAN DEFAULT 0`,                  // 是否使用OI TOP信号源
		`ALTER TABLE traders ADD COLUMN system_prompt_template TEXT DEFAULT 'default'`, // 系统提示词模板名称
		`ALTER TABLE ai_models ADD COLUMN custom_api_url TEXT DEFAULT ''`,              // 自定义API地址
		`ALTER TABLE ai_models ADD COLUMN custom_model_name TEXT DEFAULT ''`,           // 自定义模型名称
		`ALTER TABLE tg_traders ADD COLUMN ai_model_api_url TEXT DEFAULT ''`,
		`ALTER TABLE tg_traders ADD COLUMN private_key TEXT DEFAULT ''`,
		`ALTER TABLE tg_traders ADD COLUMN wallet_address TEXT DEFAULT ''`,
		`ALTER TABLE tg_traders ADD COLUMN is_configured BOOLEAN DEFAULT 0`,
	}

	for _, query := range alterQueries {
		// 忽略已存在字段的错误
		d.db.Exec(query)
	}

	d.migrateLegacyTGSessionData()
	d.dropTGUserSessionDataColumn()

	// 检查是否需要迁移exchanges表的主键结构
	err := d.migrateExchangesTable()
	if err != nil {
		log.Printf("⚠️ 迁移exchanges表失败: %v", err)
	}

	return nil
}

// initDefaultData 初始化默认数据
func (d *Database) initDefaultData() error {
	// 先创建默认用户（PostgreSQL 需要）
	if d.usePostgreSQL {
		_, err := d.db.Exec(`
			INSERT INTO users (id, email, password_hash, otp_verified)
			VALUES ('default', 'default@example.com', 'default_hash', FALSE)
			ON CONFLICT (id) DO NOTHING
		`)
		if err != nil {
			return fmt.Errorf("创建默认用户失败: %w", err)
		}
	}

	// 初始化AI模型（使用default用户）
	aiModels := []struct {
		id, name, provider string
	}{
		{"deepseek", "DeepSeek", "deepseek"},
		{"qwen", "Qwen", "qwen"},
	}

	for _, model := range aiModels {
		var err error
		if d.usePostgreSQL {
			_, err = d.db.Exec(`
				INSERT INTO ai_models (id, user_id, name, provider, enabled)
				VALUES ($1, 'default', $2, $3, FALSE)
				ON CONFLICT (id) DO NOTHING
			`, model.id, model.name, model.provider)
		} else {
			_, err = d.db.Exec(`
				INSERT OR IGNORE INTO ai_models (id, user_id, name, provider, enabled)
				VALUES (?, 'default', ?, ?, 0)
			`, model.id, model.name, model.provider)
		}
		if err != nil {
			return fmt.Errorf("初始化AI模型失败: %w", err)
		}
	}

	// 初始化交易所（使用default用户）
	exchanges := []struct {
		id, name, typ string
	}{
		{"binance", "Binance Futures", "binance"},
		{"hyperliquid", "Hyperliquid", "hyperliquid"},
		{"aster", "Aster DEX", "aster"},
	}

	for _, exchange := range exchanges {
		var err error
		if d.usePostgreSQL {
			_, err = d.db.Exec(`
				INSERT INTO exchanges (id, user_id, name, type, enabled)
				VALUES ($1, 'default', $2, $3, FALSE)
				ON CONFLICT (id) DO NOTHING
			`, exchange.id, exchange.name, exchange.typ)
		} else {
			_, err = d.db.Exec(`
				INSERT OR IGNORE INTO exchanges (id, user_id, name, type, enabled)
				VALUES (?, 'default', ?, ?, 0)
			`, exchange.id, exchange.name, exchange.typ)
		}
		if err != nil {
			return fmt.Errorf("初始化交易所失败: %w", err)
		}
	}

	// 初始化系统配置 - 创建所有字段，设置默认值，后续由config.json同步更新
	systemConfigs := map[string]string{
		"beta_mode":            "false",                                                          // 默认关闭内测模式
		"api_server_port":      "8080",                                                           // 默认API端口
		"use_default_coins":    "true",                                                           // 默认使用内置币种列表
		"default_coins":        `["BTCUSDT","ETHUSDT","SOLUSDT","BNBUSDT","HYPEUSDT","SUIUSDT"]`, // 默认币种列表（JSON格式）
		"max_daily_loss":       "10.0",                                                           // 最大日损失百分比
		"max_drawdown":         "20.0",                                                           // 最大回撤百分比
		"stop_trading_minutes": "60",                                                             // 停止交易时间（分钟）
		"btc_eth_leverage":     "5",                                                              // BTC/ETH杠杆倍数
		"altcoin_leverage":     "5",                                                              // 山寨币杠杆倍数
		"jwt_secret":           "",                                                               // JWT密钥，默认为空，由config.json或系统生成
	}

	for key, value := range systemConfigs {
		var err error
		if d.usePostgreSQL {
			_, err = d.db.Exec(`
				INSERT INTO system_config (key, value)
				VALUES ($1, $2)
				ON CONFLICT (key) DO NOTHING
			`, key, value)
		} else {
			_, err = d.db.Exec(`
				INSERT OR IGNORE INTO system_config (key, value)
				VALUES (?, ?)
			`, key, value)
		}
		if err != nil {
			return fmt.Errorf("初始化系统配置失败: %w", err)
		}
	}

	return nil
}

// createTgTradersTable 创建 tg_traders 表（Telegram专用）
func (d *Database) createTgTradersTable() error {
	var err error
	if d.usePostgreSQL {
		_, err = d.db.Exec(`
			CREATE TABLE IF NOT EXISTS tg_traders (
				id TEXT PRIMARY KEY DEFAULT uuid_generate_v4(),
				tg_user_id TEXT NOT NULL,
				name TEXT NOT NULL,
				ai_model_id TEXT NOT NULL,
				exchange_id TEXT NOT NULL,
				initial_balance DECIMAL(20,8) NOT NULL,
				scan_interval_minutes INTEGER DEFAULT 3,
				is_running BOOLEAN DEFAULT FALSE,
				is_configured BOOLEAN DEFAULT FALSE,
				btc_eth_leverage INTEGER DEFAULT 5,
				altcoin_leverage INTEGER DEFAULT 5,
				trading_symbols TEXT DEFAULT '',
				use_coin_pool BOOLEAN DEFAULT FALSE,
				use_oi_top BOOLEAN DEFAULT FALSE,
				custom_prompt TEXT DEFAULT '',
				override_base_prompt BOOLEAN DEFAULT FALSE,
				is_cross_margin BOOLEAN DEFAULT TRUE,
				use_default_coins BOOLEAN DEFAULT TRUE,
				custom_coins TEXT DEFAULT '',
				system_prompt_template TEXT DEFAULT 'default',
				ai_model_api_key TEXT DEFAULT '',
				ai_model_api_url TEXT DEFAULT '',
				private_key TEXT DEFAULT '',
				wallet_address TEXT DEFAULT '',
				created_at TIMESTAMPTZ DEFAULT NOW(),
				updated_at TIMESTAMPTZ DEFAULT NOW(),
				FOREIGN KEY (tg_user_id) REFERENCES tg_users(telegram_id) ON DELETE CASCADE,
				FOREIGN KEY (ai_model_id) REFERENCES ai_models(id),
				FOREIGN KEY (exchange_id) REFERENCES exchanges(id)
			)
		`)
	} else {
		_, err = d.db.Exec(`
			CREATE TABLE IF NOT EXISTS tg_traders (
				id TEXT PRIMARY KEY,
				tg_user_id TEXT NOT NULL,
				name TEXT NOT NULL,
				ai_model_id TEXT NOT NULL,
				exchange_id TEXT NOT NULL,
				initial_balance REAL NOT NULL,
				scan_interval_minutes INTEGER DEFAULT 3,
				is_running BOOLEAN DEFAULT 0,
				is_configured BOOLEAN DEFAULT 0,
				btc_eth_leverage INTEGER DEFAULT 5,
				altcoin_leverage INTEGER DEFAULT 5,
				trading_symbols TEXT DEFAULT '',
				use_coin_pool BOOLEAN DEFAULT 0,
				use_oi_top BOOLEAN DEFAULT 0,
				custom_prompt TEXT DEFAULT '',
				override_base_prompt BOOLEAN DEFAULT 0,
				is_cross_margin BOOLEAN DEFAULT 1,
				use_default_coins BOOLEAN DEFAULT 1,
				custom_coins TEXT DEFAULT '',
				system_prompt_template TEXT DEFAULT 'default',
				ai_model_api_key TEXT DEFAULT '',
				ai_model_api_url TEXT DEFAULT '',
				private_key TEXT DEFAULT '',
				wallet_address TEXT DEFAULT '',
				created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
				updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
			)
		`)
	}
	if err != nil {
		return fmt.Errorf("创建tg_traders表失败: %w", err)
	}

	return nil
}

func (d *Database) migrateLegacyTGSessionData() {
	if !d.tableHasColumn("tg_users", "session_data") {
		return
	}

	var rows *sql.Rows
	var err error
	query := `SELECT telegram_id, session_data FROM tg_users`
	rows, err = d.db.Query(query)
	if err != nil {
		log.Printf("⚠️ 读取旧版 session_data 失败: %v", err)
		return
	}
	defer rows.Close()

	migrated := 0
	for rows.Next() {
		var telegramID int64
		var rawData []byte
		if err := rows.Scan(&telegramID, &rawData); err != nil {
			continue
		}
		if len(rawData) == 0 {
			continue
		}

		var payload map[string]interface{}
		if err := json.Unmarshal(rawData, &payload); err != nil {
			continue
		}

		agentKey, _ := payload["agent_key"].(string)
		if agentKey == "" {
			continue
		}
		decryptedAgentKey, err := d.decryptSecretValue(agentKey)
		if err != nil {
			log.Printf("🚨 CRITICAL: 无法解密Agent密钥，跳过此记录: %v", err)
			continue
		}
		agentKey = decryptedAgentKey
		if agentKey == "" {
			continue
		}
		walletAddr, _ := payload["wallet_address"].(string)

		encryptedKey, err := d.encryptSecretValue(agentKey)
		if err != nil {
			log.Printf("🚨 CRITICAL: 无法加密Agent密钥，跳过此记录: %v", err)
			continue
		}

		var updateQuery string
		var args []interface{}
		if d.usePostgreSQL {
			updateQuery = `
				UPDATE tg_traders
				SET private_key = CASE WHEN $1 <> '' THEN $1 ELSE private_key END,
				    wallet_address = CASE WHEN $2 <> '' THEN $2 ELSE wallet_address END
				WHERE tg_user_id = $3 AND (private_key = '' OR private_key IS NULL)
			`
			args = []interface{}{encryptedKey, walletAddr, telegramID}
		} else {
			updateQuery = `
				UPDATE tg_traders
				SET private_key = CASE WHEN ? <> '' THEN ? ELSE private_key END,
				    wallet_address = CASE WHEN ? <> '' THEN ? ELSE wallet_address END
				WHERE tg_user_id = ? AND (private_key = '' OR private_key IS NULL)
			`
			args = []interface{}{encryptedKey, encryptedKey, walletAddr, walletAddr, telegramID}
		}

		if _, err := d.db.Exec(updateQuery, args...); err == nil {
			migrated++
		} else {
			log.Printf("⚠️ 迁移TG交易员私钥失败 (用户 %d): %v", telegramID, err)
		}
	}

	if migrated > 0 {
		log.Printf("🔐 已迁移 %d 个TG交易员的私钥数据", migrated)
	}
}

func (d *Database) dropTGUserSessionDataColumn() {
	if !d.tableHasColumn("tg_users", "session_data") {
		return
	}

	var query string
	if d.usePostgreSQL {
		query = `ALTER TABLE tg_users DROP COLUMN IF EXISTS session_data`
	} else {
		query = `ALTER TABLE tg_users DROP COLUMN session_data`
	}

	if _, err := d.db.Exec(query); err != nil {
		log.Printf("⚠️ 删除 tg_users.session_data 失败: %v", err)
	} else {
		log.Printf("🗑️ 已删除 tg_users.session_data 列")
	}
}

func (d *Database) tableHasColumn(table, column string) bool {
	if d.usePostgreSQL {
		var count int
		err := d.db.QueryRow(`
			SELECT COUNT(*) FROM information_schema.columns
			WHERE table_name = $1 AND column_name = $2
		`, table, column).Scan(&count)
		if err != nil {
			return false
		}
		return count > 0
	}

	query := fmt.Sprintf("PRAGMA table_info(%s)", table)
	rows, err := d.db.Query(query)
	if err != nil {
		return false
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name string
		var typ string
		var notnull interface{}
		var dfltValue interface{}
		var pk interface{}
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dfltValue, &pk); err != nil {
			continue
		}
		if strings.EqualFold(name, column) {
			return true
		}
	}
	return false
}

// migrateExchangesTable 迁移exchanges表支持多用户
func (d *Database) migrateExchangesTable() error {
	// 检查是否已经迁移过
	var count int
	err := d.db.QueryRow(`
		SELECT COUNT(*) FROM sqlite_master 
		WHERE type='table' AND name='exchanges_new'
	`).Scan(&count)
	if err != nil {
		return err
	}

	// 如果已经迁移过，直接返回
	if count > 0 {
		return nil
	}

	log.Printf("🔄 开始迁移exchanges表...")

	// 创建新的exchanges表，使用复合主键
	_, err = d.db.Exec(`
		CREATE TABLE exchanges_new (
			id TEXT NOT NULL,
			user_id TEXT NOT NULL DEFAULT 'default',
			name TEXT NOT NULL,
			type TEXT NOT NULL,
			enabled BOOLEAN DEFAULT 0,
			api_key TEXT DEFAULT '',
			secret_key TEXT DEFAULT '',
			testnet BOOLEAN DEFAULT 0,
			hyperliquid_wallet_addr TEXT DEFAULT '',
			aster_user TEXT DEFAULT '',
			aster_signer TEXT DEFAULT '',
			aster_private_key TEXT DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (id, user_id),
			FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
		)
	`)
	if err != nil {
		return fmt.Errorf("创建新exchanges表失败: %w", err)
	}

	// 复制数据到新表
	_, err = d.db.Exec(`
		INSERT INTO exchanges_new 
		SELECT * FROM exchanges
	`)
	if err != nil {
		return fmt.Errorf("复制数据失败: %w", err)
	}

	// 删除旧表
	_, err = d.db.Exec(`DROP TABLE exchanges`)
	if err != nil {
		return fmt.Errorf("删除旧表失败: %w", err)
	}

	// 重命名新表
	_, err = d.db.Exec(`ALTER TABLE exchanges_new RENAME TO exchanges`)
	if err != nil {
		return fmt.Errorf("重命名表失败: %w", err)
	}

	// 重新创建触发器
	_, err = d.db.Exec(`
		CREATE TRIGGER IF NOT EXISTS update_exchanges_updated_at
			AFTER UPDATE ON exchanges
			BEGIN
				UPDATE exchanges SET updated_at = CURRENT_TIMESTAMP 
				WHERE id = NEW.id AND user_id = NEW.user_id;
			END
	`)
	if err != nil {
		return fmt.Errorf("创建触发器失败: %w", err)
	}

	log.Printf("✅ exchanges表迁移完成")
	return nil
}

// User 用户配置
type User struct {
	ID           string    `json:"id"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"` // 不返回到前端
	OTPSecret    string    `json:"-"` // 不返回到前端
	OTPVerified  bool      `json:"otp_verified"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// AIModelConfig AI模型配置
type AIModelConfig struct {
	ID              string    `json:"id"`
	UserID          string    `json:"user_id"`
	Name            string    `json:"name"`
	Provider        string    `json:"provider"`
	Enabled         bool      `json:"enabled"`
	APIKey          string    `json:"apiKey"`
	CustomAPIURL    string    `json:"customApiUrl"`
	CustomModelName string    `json:"customModelName"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// ExchangeConfig 交易所配置
type ExchangeConfig struct {
	ID        string `json:"id"`
	UserID    string `json:"user_id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	Enabled   bool   `json:"enabled"`
	APIKey    string `json:"apiKey"`    // For Binance: API Key; For Hyperliquid: Agent Private Key (should have ~0 balance)
	SecretKey string `json:"secretKey"` // For Binance: Secret Key; Not used for Hyperliquid
	Testnet   bool   `json:"testnet"`
	// Hyperliquid Agent Wallet configuration (following official best practices)
	// Reference: https://hyperliquid.gitbook.io/hyperliquid-docs/for-developers/api/nonces-and-api-wallets
	HyperliquidWalletAddr string `json:"hyperliquidWalletAddr"` // Main Wallet Address (holds funds, never expose private key)
	// Aster 特定字段
	AsterUser          string    `json:"asterUser"`
	AsterSigner        string    `json:"asterSigner"`
	AsterPrivateKey    string    `json:"asterPrivateKey"`
	CustomExchangeName string    `json:"customExchangeName"` // 用户自定义交易所名称
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// TraderRecord 交易员配置（数据库实体）
type TraderRecord struct {
	ID                   string    `json:"id"`
	UserID               string    `json:"user_id"`
	Name                 string    `json:"name"`
	AIModelID            string    `json:"ai_model_id"`
	ExchangeID           string    `json:"exchange_id"`
	InitialBalance       float64   `json:"initial_balance"`
	ScanIntervalMinutes  int       `json:"scan_interval_minutes"`
	IsRunning            bool      `json:"is_running"`
	BTCETHLeverage       int       `json:"btc_eth_leverage"`       // BTC/ETH杠杆倍数
	AltcoinLeverage      int       `json:"altcoin_leverage"`       // 山寨币杠杆倍数
	TradingSymbols       string    `json:"trading_symbols"`        // 交易币种，逗号分隔
	UseCoinPool          bool      `json:"use_coin_pool"`          // 是否使用COIN POOL信号源
	UseOITop             bool      `json:"use_oi_top"`             // 是否使用OI TOP信号源
	CustomPrompt         string    `json:"custom_prompt"`          // 自定义交易策略prompt
	OverrideBasePrompt   bool      `json:"override_base_prompt"`   // 是否覆盖基础prompt
	SystemPromptTemplate string    `json:"system_prompt_template"` // 系统提示词模板名称
	IsCrossMargin        bool      `json:"is_cross_margin"`        // 是否为全仓模式（true=全仓，false=逐仓）
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

// TgTraderRecord TG交易员记录
type TgTraderRecord struct {
	ID                   string    `json:"id"`
	TgUserID             int64     `json:"tg_user_id"` // TG用户ID
	Name                 string    `json:"name"`
	AIModelID            string    `json:"ai_model_id"`
	AIModelName          string    `json:"ai_model_name"` // 自定义模型名称
	ExchangeID           string    `json:"exchange_id"`
	InitialBalance       float64   `json:"initial_balance"`
	ScanIntervalMinutes  int       `json:"scan_interval_minutes"`
	IsRunning            bool      `json:"is_running"`
	IsConfigured         bool      `json:"is_configured"`
	BTCETHLeverage       int       `json:"btc_eth_leverage"`
	AltcoinLeverage      int       `json:"altcoin_leverage"`
	TradingSymbols       string    `json:"trading_symbols"`
	UseCoinPool          bool      `json:"use_coin_pool"`
	UseOITop             bool      `json:"use_oi_top"`
	CustomPrompt         string    `json:"custom_prompt"`
	OverrideBasePrompt   bool      `json:"override_base_prompt"`
	IsCrossMargin        bool      `json:"is_cross_margin"`
	UseDefaultCoins      bool      `json:"use_default_coins"`
	CustomCoins          string    `json:"custom_coins"`
	SystemPromptTemplate string    `json:"system_prompt_template"`
	AIModelAPIKey        string    `json:"ai_model_api_key"`
	AIModelAPIURL        string    `json:"ai_model_api_url"`
	PrivateKey           string    `json:"private_key"`
	WalletAddress        string    `json:"wallet_address"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

// TgGasSponsorshipRecord 记录 Gas 赞助动作
type TgGasSponsorshipRecord struct {
	ID            int64     `json:"id"`
	TgUserID      int64     `json:"tg_user_id"`
	WalletAddress string    `json:"wallet_address"`
	AmountWei     string    `json:"amount_wei"`
	USDCAmount    string    `json:"usdc_amount"`
	TxHash        string    `json:"tx_hash"`
	GasTxHash     string    `json:"gas_tx_hash"`
	BridgeTxHash  string    `json:"bridge_tx_hash"`
	Status        string    `json:"status"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// UserSignalSource 用户信号源配置
type UserSignalSource struct {
	ID          int       `json:"id"`
	UserID      string    `json:"user_id"`
	CoinPoolURL string    `json:"coin_pool_url"`
	OITopURL    string    `json:"oi_top_url"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// GenerateOTPSecret 生成OTP密钥
func GenerateOTPSecret() (string, error) {
	secret := make([]byte, 20)
	_, err := rand.Read(secret)
	if err != nil {
		return "", err
	}
	return base32.StdEncoding.EncodeToString(secret), nil
}

// CreateUser 创建用户
func (d *Database) CreateUser(user *User) error {
	var err error
	if d.usePostgreSQL {
		_, err = d.db.Exec(`
			INSERT INTO users (id, email, password_hash, otp_secret, otp_verified)
			VALUES ($1, $2, $3, $4, $5)
		`, user.ID, user.Email, user.PasswordHash, user.OTPSecret, user.OTPVerified)
	} else {
		_, err = d.db.Exec(`
			INSERT INTO users (id, email, password_hash, otp_secret, otp_verified)
			VALUES (?, ?, ?, ?, ?)
		`, user.ID, user.Email, user.PasswordHash, user.OTPSecret, user.OTPVerified)
	}
	return err
}

// EnsureAdminUser 确保admin用户存在（用于管理员模式）
func (d *Database) EnsureAdminUser() error {
	// 检查admin用户是否已存在
	var count int
	err := d.db.QueryRow(`SELECT COUNT(*) FROM users WHERE id = 'admin'`).Scan(&count)
	if err != nil {
		return err
	}

	// 如果已存在，直接返回
	if count > 0 {
		return nil
	}

	// 创建admin用户（密码为空，因为管理员模式下不需要密码）
	adminUser := &User{
		ID:           "admin",
		Email:        "admin@localhost",
		PasswordHash: "", // 管理员模式下不使用密码
		OTPSecret:    "",
		OTPVerified:  true,
	}

	return d.CreateUser(adminUser)
}

// GetUserByEmail 通过邮箱获取用户
func (d *Database) GetUserByEmail(email string) (*User, error) {
	var user User
	var err error
	if d.usePostgreSQL {
		err = d.db.QueryRow(`
			SELECT id, email, password_hash, otp_secret, otp_verified, created_at, updated_at
			FROM users WHERE email = $1
		`, email).Scan(
			&user.ID, &user.Email, &user.PasswordHash, &user.OTPSecret,
			&user.OTPVerified, &user.CreatedAt, &user.UpdatedAt,
		)
	} else {
		err = d.db.QueryRow(`
			SELECT id, email, password_hash, otp_secret, otp_verified, created_at, updated_at
			FROM users WHERE email = ?
		`, email).Scan(
			&user.ID, &user.Email, &user.PasswordHash, &user.OTPSecret,
			&user.OTPVerified, &user.CreatedAt, &user.UpdatedAt,
		)
	}
	if err != nil {
		return nil, err
	}
	return &user, nil
}

// GetUserByID 通过ID获取用户
func (d *Database) GetUserByID(userID string) (*User, error) {
	var user User
	var err error
	if d.usePostgreSQL {
		err = d.db.QueryRow(`
			SELECT id, email, password_hash, otp_secret, otp_verified, created_at, updated_at
			FROM users WHERE id = $1
		`, userID).Scan(
			&user.ID, &user.Email, &user.PasswordHash, &user.OTPSecret,
			&user.OTPVerified, &user.CreatedAt, &user.UpdatedAt,
		)
	} else {
		err = d.db.QueryRow(`
			SELECT id, email, password_hash, otp_secret, otp_verified, created_at, updated_at
			FROM users WHERE id = ?
		`, userID).Scan(
			&user.ID, &user.Email, &user.PasswordHash, &user.OTPSecret,
			&user.OTPVerified, &user.CreatedAt, &user.UpdatedAt,
		)
	}
	if err != nil {
		return nil, err
	}
	return &user, nil
}

// GetAllUsers 获取所有用户ID列表
func (d *Database) GetAllUsers() ([]string, error) {
	rows, err := d.db.Query(`SELECT id FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var userIDs []string
	for rows.Next() {
		var userID string
		if err := rows.Scan(&userID); err != nil {
			return nil, err
		}
		userIDs = append(userIDs, userID)
	}
	return userIDs, nil
}

// GetAllTGUsers 获取所有TG用户ID列表
func (d *Database) GetAllTGUsers() ([]int64, error) {
	var query string
	if d.usePostgreSQL {
		query = `SELECT DISTINCT telegram_id FROM tg_users ORDER BY telegram_id`
	} else {
		query = `SELECT DISTINCT telegram_id FROM tg_users ORDER BY telegram_id`
	}

	rows, err := d.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tgUserIDs []int64
	for rows.Next() {
		var telegramID int64
		if err := rows.Scan(&telegramID); err != nil {
			return nil, err
		}
		tgUserIDs = append(tgUserIDs, telegramID)
	}

	return tgUserIDs, nil
}

// UpdateUserOTPVerified 更新用户OTP验证状态
func (d *Database) UpdateUserOTPVerified(userID string, verified bool) error {
	var err error
	if d.usePostgreSQL {
		_, err = d.db.Exec(`UPDATE users SET otp_verified = $1 WHERE id = $2`, verified, userID)
	} else {
		_, err = d.db.Exec(`UPDATE users SET otp_verified = ? WHERE id = ?`, verified, userID)
	}
	return err
}

// UpdateUserPassword 更新用户密码
func (d *Database) UpdateUserPassword(userID, passwordHash string) error {
	var err error
	if d.usePostgreSQL {
		_, err = d.db.Exec(`
			UPDATE users
			SET password_hash = $1, updated_at = NOW()
			WHERE id = $2
		`, passwordHash, userID)
	} else {
		_, err = d.db.Exec(`
			UPDATE users
			SET password_hash = ?, updated_at = CURRENT_TIMESTAMP
			WHERE id = ?
		`, passwordHash, userID)
	}
	return err
}

// GetAIModels 获取用户的AI模型配置
func (d *Database) GetAIModels(userID string) ([]*AIModelConfig, error) {
	var rows *sql.Rows
	var err error

	if d.usePostgreSQL {
		rows, err = d.db.Query(`
			SELECT id, user_id, name, provider, enabled, api_key,
			       COALESCE(custom_api_url, '') as custom_api_url,
			       COALESCE(custom_model_name, '') as custom_model_name,
			       created_at, updated_at
			FROM ai_models WHERE user_id = $1 ORDER BY id
		`, userID)
	} else {
		rows, err = d.db.Query(`
			SELECT id, user_id, name, provider, enabled, api_key,
			       COALESCE(custom_api_url, '') as custom_api_url,
			       COALESCE(custom_model_name, '') as custom_model_name,
			       created_at, updated_at
			FROM ai_models WHERE user_id = ? ORDER BY id
		`, userID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// 初始化为空切片而不是nil，确保JSON序列化为[]而不是null
	models := make([]*AIModelConfig, 0)
	for rows.Next() {
		var model AIModelConfig
		err := rows.Scan(
			&model.ID, &model.UserID, &model.Name, &model.Provider,
			&model.Enabled, &model.APIKey, &model.CustomAPIURL, &model.CustomModelName,
			&model.CreatedAt, &model.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		// 解密API Key
		decryptedAPIKey, err := d.decryptSensitiveData(model.APIKey)
		if err != nil {
			log.Printf("🚨 CRITICAL: 无法解密API密钥，数据可能已损坏: %v", err)
			return nil, fmt.Errorf("无法解密API密钥: %w", err)
		}
		model.APIKey = decryptedAPIKey
		models = append(models, &model)
	}

	return models, nil
}

// UpdateAIModel 更新AI模型配置，如果不存在则创建用户特定配置
func (d *Database) UpdateAIModel(userID, id string, enabled bool, apiKey, customAPIURL, customModelName string) error {
	// 先尝试精确匹配 ID（新版逻辑，支持多个相同 provider 的模型）
	var existingID string
	var err error

	if d.usePostgreSQL {
		err = d.db.QueryRow(`
			SELECT id FROM ai_models WHERE user_id = $1 AND id = $2 LIMIT 1
		`, userID, id).Scan(&existingID)
	} else {
		err = d.db.QueryRow(`
			SELECT id FROM ai_models WHERE user_id = ? AND id = ? LIMIT 1
		`, userID, id).Scan(&existingID)
	}

	if err == nil {
		// 找到了现有配置（精确匹配 ID），更新它
		encryptedAPIKey, err := d.encryptSensitiveData(apiKey)
		if err != nil {
			log.Printf("🚨 CRITICAL: 无法加密AI模型API密钥，配置将不被保存: %v", err)
			return fmt.Errorf("无法加密AI模型API密钥: %w", err)
		}
		if d.usePostgreSQL {
			_, err = d.db.Exec(`
				UPDATE ai_models SET enabled = $1, api_key = $2, custom_api_url = $3, custom_model_name = $4, updated_at = NOW()
				WHERE id = $5 AND user_id = $6
			`, enabled, encryptedAPIKey, customAPIURL, customModelName, existingID, userID)
		} else {
			_, err = d.db.Exec(`
				UPDATE ai_models SET enabled = ?, api_key = ?, custom_api_url = ?, custom_model_name = ?, updated_at = datetime('now')
				WHERE id = ? AND user_id = ?
			`, enabled, encryptedAPIKey, customAPIURL, customModelName, existingID, userID)
		}
		return err
	}

	// ID 不存在，尝试兼容旧逻辑：将 id 作为 provider 查找
	provider := id
	if d.usePostgreSQL {
		err = d.db.QueryRow(`
			SELECT id FROM ai_models WHERE user_id = $1 AND provider = $2 LIMIT 1
		`, userID, provider).Scan(&existingID)
	} else {
		err = d.db.QueryRow(`
			SELECT id FROM ai_models WHERE user_id = ? AND provider = ? LIMIT 1
		`, userID, provider).Scan(&existingID)
	}

	if err == nil {
		// 找到了现有配置（通过 provider 匹配，兼容旧版），更新它
		log.Printf("⚠️  使用旧版 provider 匹配更新模型: %s -> %s", provider, existingID)
		encryptedAPIKey, err := d.encryptSensitiveData(apiKey)
		if err != nil {
			log.Printf("🚨 CRITICAL: 无法加密AI模型API密钥，配置将不被保存: %v", err)
			return fmt.Errorf("无法加密AI模型API密钥: %w", err)
		}
		if d.usePostgreSQL {
			_, err = d.db.Exec(`
				UPDATE ai_models SET enabled = $1, api_key = $2, custom_api_url = $3, custom_model_name = $4, updated_at = NOW()
				WHERE id = $5 AND user_id = $6
			`, enabled, encryptedAPIKey, customAPIURL, customModelName, existingID, userID)
		} else {
			_, err = d.db.Exec(`
				UPDATE ai_models SET enabled = ?, api_key = ?, custom_api_url = ?, custom_model_name = ?, updated_at = datetime('now')
				WHERE id = ? AND user_id = ?
			`, enabled, encryptedAPIKey, customAPIURL, customModelName, existingID, userID)
		}
		return err
	}

	// 没有找到任何现有配置，创建新的
	// 推断 provider（从 id 中提取，或者直接使用 id）
	if provider == id && (provider == "deepseek" || provider == "qwen") {
		// id 本身就是 provider
		provider = id
	} else {
		// 从 id 中提取 provider（假设格式是 userID_provider 或 timestamp_userID_provider）
		parts := strings.Split(id, "_")
		if len(parts) >= 2 {
			provider = parts[len(parts)-1] // 取最后一部分作为 provider
		} else {
			provider = id
		}
	}

	// 获取模型的基本信息
	var name string
	if d.usePostgreSQL {
		err = d.db.QueryRow(`
			SELECT name FROM ai_models WHERE provider = $1 LIMIT 1
		`, provider).Scan(&name)
	} else {
		err = d.db.QueryRow(`
			SELECT name FROM ai_models WHERE provider = ? LIMIT 1
		`, provider).Scan(&name)
	}
	if err != nil {
		// 如果找不到基本信息，使用默认值
		if provider == "deepseek" {
			name = "DeepSeek AI"
		} else if provider == "qwen" {
			name = "Qwen AI"
		} else {
			name = provider + " AI"
		}
	}

	// 如果传入的 ID 已经是完整格式（如 "admin_deepseek_custom1"），直接使用
	// 否则生成新的 ID
	newModelID := id
	if id == provider {
		// id 就是 provider，生成新的用户特定 ID
		newModelID = fmt.Sprintf("%s_%s", userID, provider)
	}

	log.Printf("✓ 创建新的 AI 模型配置: ID=%s, Provider=%s, Name=%s", newModelID, provider, name)
	encryptedAPIKey, err := d.encryptSensitiveData(apiKey)
	if err != nil {
		log.Printf("🚨 CRITICAL: 无法加密AI模型API密钥，配置将不被保存: %v", err)
		return fmt.Errorf("无法加密AI模型API密钥: %w", err)
	}
	if d.usePostgreSQL {
		_, err = d.db.Exec(`
			INSERT INTO ai_models (id, user_id, name, provider, enabled, api_key, custom_api_url, custom_model_name, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW(), NOW())
		`, newModelID, userID, name, provider, enabled, encryptedAPIKey, customAPIURL, customModelName)
	} else {
		_, err = d.db.Exec(`
			INSERT INTO ai_models (id, user_id, name, provider, enabled, api_key, custom_api_url, custom_model_name, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, datetime('now'), datetime('now'))
		`, newModelID, userID, name, provider, enabled, encryptedAPIKey, customAPIURL, customModelName)
	}

	return err
}

// UpdateAIModelExceptAPIKey 更新AI模型配置，但不更新API Key（用于编辑模式）
func (d *Database) UpdateAIModelExceptAPIKey(userID, id string, enabled bool, customAPIURL, customModelName string) error {
	// 先尝试精确匹配 ID（新版逻辑，支持多个相同 provider 的模型）
	var existingID string
	var err error

	if d.usePostgreSQL {
		err = d.db.QueryRow(`
			SELECT id FROM ai_models WHERE user_id = $1 AND id = $2 LIMIT 1
		`, userID, id).Scan(&existingID)
	} else {
		err = d.db.QueryRow(`
			SELECT id FROM ai_models WHERE user_id = ? AND id = ? LIMIT 1
		`, userID, id).Scan(&existingID)
	}

	if err == nil {
		// 找到了现有配置（精确匹配 ID），更新除API Key外的字段
		if d.usePostgreSQL {
			_, err = d.db.Exec(`
				UPDATE ai_models SET enabled = $1, custom_api_url = $2, custom_model_name = $3, updated_at = NOW()
				WHERE id = $4 AND user_id = $5
			`, enabled, customAPIURL, customModelName, existingID, userID)
		} else {
			_, err = d.db.Exec(`
				UPDATE ai_models SET enabled = ?, custom_api_url = ?, custom_model_name = ?, updated_at = datetime('now')
				WHERE id = ? AND user_id = ?
			`, enabled, customAPIURL, customModelName, existingID, userID)
		}
		return err
	}

	// ID 不存在，尝试兼容旧逻辑：将 id 作为 provider 查找
	provider := id
	if d.usePostgreSQL {
		err = d.db.QueryRow(`
			SELECT id FROM ai_models WHERE user_id = $1 AND provider = $2 LIMIT 1
		`, userID, provider).Scan(&existingID)
	} else {
		err = d.db.QueryRow(`
			SELECT id FROM ai_models WHERE user_id = ? AND provider = ? LIMIT 1
		`, userID, provider).Scan(&existingID)
	}

	if err == nil {
		// 找到了现有配置（通过 provider 匹配，兼容旧版），更新它（但不更新API Key）
		log.Printf("⚠️  使用旧版 provider 匹配更新模型（不更新API Key）: %s -> %s", provider, existingID)
		if d.usePostgreSQL {
			_, err = d.db.Exec(`
				UPDATE ai_models SET enabled = $1, custom_api_url = $2, custom_model_name = $3, updated_at = NOW()
				WHERE id = $4 AND user_id = $5
			`, enabled, customAPIURL, customModelName, existingID, userID)
		} else {
			_, err = d.db.Exec(`
				UPDATE ai_models SET enabled = ?, custom_api_url = ?, custom_model_name = ?, updated_at = datetime('now')
				WHERE id = ? AND user_id = ?
			`, enabled, customAPIURL, customModelName, existingID, userID)
		}
		return err
	}

	// 没有找到任何现有配置，返回错误
	return fmt.Errorf("模型 %s 不存在", id)
}

// GetExchanges 获取用户的交易所配置
func (d *Database) GetExchanges(userID string) ([]*ExchangeConfig, error) {
	query := `
		SELECT id, user_id, name, type, enabled, api_key, secret_key, testnet,
		       COALESCE(hyperliquid_wallet_addr, '') as hyperliquid_wallet_addr,
		       COALESCE(aster_user, '') as aster_user,
		       COALESCE(aster_signer, '') as aster_signer,
		       COALESCE(aster_private_key, '') as aster_private_key,
		       COALESCE(custom_exchange_name, '') as custom_exchange_name,
		       created_at, updated_at
		FROM exchanges WHERE user_id = $1 ORDER BY id
	`

	var rows *sql.Rows
	var err error
	if d.usePostgreSQL {
		rows, err = d.db.Query(query, userID)
	} else {
		// SQLite 使用 ? 占位符
		querySQLite := `
			SELECT id, user_id, name, type, enabled, api_key, secret_key, testnet,
			       COALESCE(hyperliquid_wallet_addr, '') as hyperliquid_wallet_addr,
			       COALESCE(aster_user, '') as aster_user,
			       COALESCE(aster_signer, '') as aster_signer,
			       COALESCE(aster_private_key, '') as aster_private_key,
			       COALESCE(custom_exchange_name, '') as custom_exchange_name,
			       created_at, updated_at
			FROM exchanges WHERE user_id = ? ORDER BY id
		`
		rows, err = d.db.Query(querySQLite, userID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// 初始化为空切片而不是nil，确保JSON序列化为[]而不是null
	exchanges := make([]*ExchangeConfig, 0)
	for rows.Next() {
		var exchange ExchangeConfig
		err := rows.Scan(
			&exchange.ID, &exchange.UserID, &exchange.Name, &exchange.Type,
			&exchange.Enabled, &exchange.APIKey, &exchange.SecretKey, &exchange.Testnet,
			&exchange.HyperliquidWalletAddr, &exchange.AsterUser,
			&exchange.AsterSigner, &exchange.AsterPrivateKey,
			&exchange.CustomExchangeName,
			&exchange.CreatedAt, &exchange.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}

		// 解密敏感字段
		decryptedAPIKey, err := d.decryptSensitiveData(exchange.APIKey)
		if err != nil {
			log.Printf("🚨 CRITICAL: 无法解密交易所API密钥，数据可能已损坏: %v", err)
			return nil, fmt.Errorf("无法解密交易所API密钥: %w", err)
		}
		exchange.APIKey = decryptedAPIKey

		decryptedSecretKey, err := d.decryptSensitiveData(exchange.SecretKey)
		if err != nil {
			log.Printf("🚨 CRITICAL: 无法解密交易所Secret密钥，数据可能已损坏: %v", err)
			return nil, fmt.Errorf("无法解密交易所Secret密钥: %w", err)
		}
		exchange.SecretKey = decryptedSecretKey

		decryptedAsterPrivateKey, err := d.decryptSensitiveData(exchange.AsterPrivateKey)
		if err != nil {
			log.Printf("🚨 CRITICAL: 无法解密Aster私钥，数据可能已损坏: %v", err)
			return nil, fmt.Errorf("无法解密Aster私钥: %w", err)
		}
		exchange.AsterPrivateKey = decryptedAsterPrivateKey

		exchanges = append(exchanges, &exchange)
	}

	return exchanges, nil
}

// UpdateExchange 更新交易所配置，为用户创建新的交易所记录
func (d *Database) UpdateExchange(userID, exchangeType string, enabled bool, apiKey, secretKey string, testnet bool, hyperliquidWalletAddr, asterUser, asterSigner, asterPrivateKey, customExchangeName string) error {
	log.Printf("🔧 UpdateExchange: userID=%s, exchangeType=%s, enabled=%v", userID, exchangeType, enabled)

	// 加密敏感字段
	encryptedAPIKey, err := d.encryptSensitiveData(apiKey)
	if err != nil {
		log.Printf("🚨 CRITICAL: 无法加密交易所API密钥，配置将不被保存: %v", err)
		return fmt.Errorf("无法加密交易所API密钥: %w", err)
	}
	encryptedSecretKey, err := d.encryptSensitiveData(secretKey)
	if err != nil {
		log.Printf("🚨 CRITICAL: 无法加密交易所Secret密钥，配置将不被保存: %v", err)
		return fmt.Errorf("无法加密交易所Secret密钥: %w", err)
	}
	encryptedAsterPrivateKey, err := d.encryptSensitiveData(asterPrivateKey)
	if err != nil {
		log.Printf("🚨 CRITICAL: 无法加密Aster私钥，配置将不被保存: %v", err)
		return fmt.Errorf("无法加密Aster私钥: %w", err)
	}

	// 确定交易所的基本信息
	var name string
	if exchangeType == "binance" {
		name = "Binance Futures"
	} else if exchangeType == "hyperliquid" {
		name = "Hyperliquid"
	} else if exchangeType == "aster" {
		name = "Aster DEX"
	} else {
		name = exchangeType + " Exchange"
	}

	// 为用户创建新的交易所记录，使用 UUID 作为主键
	if d.usePostgreSQL {
		// PostgreSQL: 插入新记录
		_, err := d.db.Exec(`
			INSERT INTO exchanges (user_id, name, type, enabled, api_key, secret_key, testnet,
			                       hyperliquid_wallet_addr, aster_user, aster_signer, aster_private_key, custom_exchange_name, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, NOW(), NOW())
		`, userID, name, exchangeType, enabled, encryptedAPIKey, encryptedSecretKey, testnet, hyperliquidWalletAddr, asterUser, asterSigner, encryptedAsterPrivateKey, customExchangeName)
		if err != nil {
			log.Printf("❌ UpdateExchange: 插入失败: %v", err)
			return err
		}
		log.Printf("✅ UpdateExchange: 成功为用户 %s 创建 %s 交易所", userID, exchangeType)
		return nil
	} else {
		// SQLite: 插入新记录
		_, err := d.db.Exec(`
			INSERT INTO exchanges (user_id, name, type, enabled, api_key, secret_key, testnet,
			                       hyperliquid_wallet_addr, aster_user, aster_signer, aster_private_key, custom_exchange_name, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'), datetime('now'))
		`, userID, name, exchangeType, enabled, encryptedAPIKey, encryptedSecretKey, testnet, hyperliquidWalletAddr, asterUser, asterSigner, encryptedAsterPrivateKey, customExchangeName)
		if err != nil {
			log.Printf("❌ UpdateExchange: 插入失败: %v", err)
			return err
		}
		log.Printf("✅ UpdateExchange: 成功为用户 %s 创建 %s 交易所", userID, exchangeType)
		return nil
	}
}

// CreateAIModel 创建AI模型配置
func (d *Database) CreateAIModel(userID, id, name, provider string, enabled bool, apiKey, customAPIURL string) error {
	var err error
	if d.usePostgreSQL {
		_, err = d.db.Exec(`
			INSERT INTO ai_models (id, user_id, name, provider, enabled, api_key, custom_api_url)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (id) DO NOTHING
		`, id, userID, name, provider, enabled, apiKey, customAPIURL)
	} else {
		_, err = d.db.Exec(`
			INSERT OR IGNORE INTO ai_models (id, user_id, name, provider, enabled, api_key, custom_api_url)
			VALUES (?, ?, ?, ?, ?, ?, ?)
		`, id, userID, name, provider, enabled, apiKey, customAPIURL)
	}
	return err
}

// CreateExchange 创建交易所配置
func (d *Database) CreateExchange(userID, id, name, typ string, enabled bool, apiKey, secretKey string, testnet bool, hyperliquidWalletAddr, asterUser, asterSigner, asterPrivateKey string) error {
	// 加密敏感字段
	encryptedAPIKey, err := d.encryptSensitiveData(apiKey)
	if err != nil {
		log.Printf("🚨 CRITICAL: 无法加密交易所API密钥，配置将不被保存: %v", err)
		return fmt.Errorf("无法加密交易所API密钥: %w", err)
	}
	encryptedSecretKey, err := d.encryptSensitiveData(secretKey)
	if err != nil {
		log.Printf("🚨 CRITICAL: 无法加密交易所Secret密钥，配置将不被保存: %v", err)
		return fmt.Errorf("无法加密交易所Secret密钥: %w", err)
	}
	encryptedAsterPrivateKey, err := d.encryptSensitiveData(asterPrivateKey)
	if err != nil {
		log.Printf("🚨 CRITICAL: 无法加密Aster私钥，配置将不被保存: %v", err)
		return fmt.Errorf("无法加密Aster私钥: %w", err)
	}

	if d.usePostgreSQL {
		_, err = d.db.Exec(`
			INSERT INTO exchanges (id, user_id, name, type, enabled, api_key, secret_key, testnet, hyperliquid_wallet_addr, aster_user, aster_signer, aster_private_key)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
			ON CONFLICT (id, user_id) DO NOTHING
		`, id, userID, name, typ, enabled, encryptedAPIKey, encryptedSecretKey, testnet, hyperliquidWalletAddr, asterUser, asterSigner, encryptedAsterPrivateKey)
	} else {
		_, err = d.db.Exec(`
			INSERT OR IGNORE INTO exchanges (id, user_id, name, type, enabled, api_key, secret_key, testnet, hyperliquid_wallet_addr, aster_user, aster_signer, aster_private_key)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, id, userID, name, typ, enabled, encryptedAPIKey, encryptedSecretKey, testnet, hyperliquidWalletAddr, asterUser, asterSigner, encryptedAsterPrivateKey)
	}
	return err
}

// CreateTrader 创建交易员
func (d *Database) CreateTrader(trader *TraderRecord) error {
	var err error
	if d.usePostgreSQL {
		_, err = d.db.Exec(`
			INSERT INTO traders (id, user_id, name, ai_model_id, exchange_id, initial_balance, scan_interval_minutes, is_running, btc_eth_leverage, altcoin_leverage, trading_symbols, use_coin_pool, use_oi_top, custom_prompt, override_base_prompt, system_prompt_template, is_cross_margin)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
		`, trader.ID, trader.UserID, trader.Name, trader.AIModelID, trader.ExchangeID, trader.InitialBalance, trader.ScanIntervalMinutes, trader.IsRunning, trader.BTCETHLeverage, trader.AltcoinLeverage, trader.TradingSymbols, trader.UseCoinPool, trader.UseOITop, trader.CustomPrompt, trader.OverrideBasePrompt, trader.SystemPromptTemplate, trader.IsCrossMargin)
	} else {
		_, err = d.db.Exec(`
			INSERT INTO traders (id, user_id, name, ai_model_id, exchange_id, initial_balance, scan_interval_minutes, is_running, btc_eth_leverage, altcoin_leverage, trading_symbols, use_coin_pool, use_oi_top, custom_prompt, override_base_prompt, system_prompt_template, is_cross_margin)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, trader.ID, trader.UserID, trader.Name, trader.AIModelID, trader.ExchangeID, trader.InitialBalance, trader.ScanIntervalMinutes, trader.IsRunning, trader.BTCETHLeverage, trader.AltcoinLeverage, trader.TradingSymbols, trader.UseCoinPool, trader.UseOITop, trader.CustomPrompt, trader.OverrideBasePrompt, trader.SystemPromptTemplate, trader.IsCrossMargin)
	}
	return err
}

// GetTraders 获取用户的交易员
func (d *Database) GetTraders(userID string) ([]*TraderRecord, error) {
	var rows *sql.Rows
	var err error

	if d.usePostgreSQL {
		rows, err = d.db.Query(`
			SELECT id, user_id, name, ai_model_id, exchange_id, initial_balance, scan_interval_minutes, is_running,
			       COALESCE(btc_eth_leverage, 5::INTEGER) as btc_eth_leverage, COALESCE(altcoin_leverage, 5::INTEGER) as altcoin_leverage,
			       COALESCE(trading_symbols, '') as trading_symbols,
			       COALESCE(use_coin_pool, 0::BOOLEAN) as use_coin_pool, COALESCE(use_oi_top, 0::BOOLEAN) as use_oi_top,
			       COALESCE(custom_prompt, '') as custom_prompt, COALESCE(override_base_prompt, 0::BOOLEAN) as override_base_prompt,
			       COALESCE(system_prompt_template, 'default') as system_prompt_template,
			       COALESCE(is_cross_margin, 1::BOOLEAN) as is_cross_margin, created_at, updated_at
			FROM traders WHERE user_id = $1 ORDER BY created_at DESC
		`, userID)
	} else {
		rows, err = d.db.Query(`
			SELECT id, user_id, name, ai_model_id, exchange_id, initial_balance, scan_interval_minutes, is_running,
			       COALESCE(btc_eth_leverage, 5) as btc_eth_leverage, COALESCE(altcoin_leverage, 5) as altcoin_leverage,
			       COALESCE(trading_symbols, '') as trading_symbols,
			       COALESCE(use_coin_pool, 0) as use_coin_pool, COALESCE(use_oi_top, 0) as use_oi_top,
			       COALESCE(custom_prompt, '') as custom_prompt, COALESCE(override_base_prompt, 0) as override_base_prompt,
			       COALESCE(system_prompt_template, 'default') as system_prompt_template,
			       COALESCE(is_cross_margin, 1) as is_cross_margin, created_at, updated_at
			FROM traders WHERE user_id = ? ORDER BY created_at DESC
		`, userID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var traders []*TraderRecord
	for rows.Next() {
		var trader TraderRecord
		err := rows.Scan(
			&trader.ID, &trader.UserID, &trader.Name, &trader.AIModelID, &trader.ExchangeID,
			&trader.InitialBalance, &trader.ScanIntervalMinutes, &trader.IsRunning,
			&trader.BTCETHLeverage, &trader.AltcoinLeverage, &trader.TradingSymbols,
			&trader.UseCoinPool, &trader.UseOITop,
			&trader.CustomPrompt, &trader.OverrideBasePrompt, &trader.SystemPromptTemplate,
			&trader.IsCrossMargin,
			&trader.CreatedAt, &trader.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		traders = append(traders, &trader)
	}

	return traders, nil
}

// UpdateTraderStatus 更新交易员状态
func (d *Database) UpdateTraderStatus(userID, id string, isRunning bool) error {
	var err error
	if d.usePostgreSQL {
		_, err = d.db.Exec(`UPDATE traders SET is_running = $1 WHERE id = $2 AND user_id = $3`, isRunning, id, userID)
	} else {
		_, err = d.db.Exec(`UPDATE traders SET is_running = ? WHERE id = ? AND user_id = ?`, isRunning, id, userID)
	}
	return err
}

// GetRunningTraders 获取所有运行中的交易员
func (d *Database) GetRunningTraders() ([]TraderRecord, error) {
	var rows *sql.Rows
	var err error

	if d.usePostgreSQL {
		rows, err = d.db.Query(`
			SELECT id, user_id, name, ai_model_id, exchange_id, initial_balance, scan_interval_minutes, is_running,
			       btc_eth_leverage, altcoin_leverage, trading_symbols, use_coin_pool, use_oi_top,
			       custom_prompt, override_base_prompt, system_prompt_template, is_cross_margin
			FROM traders
			WHERE is_running = true
		`)
	} else {
		rows, err = d.db.Query(`
			SELECT id, user_id, name, ai_model_id, exchange_id, initial_balance, scan_interval_minutes, is_running,
			       btc_eth_leverage, altcoin_leverage, trading_symbols, use_coin_pool, use_oi_top,
			       custom_prompt, override_base_prompt, system_prompt_template, is_cross_margin
			FROM traders
			WHERE is_running = 1
		`)
	}

	if err != nil {
		return nil, fmt.Errorf("查询运行中交易员失败: %w", err)
	}
	defer rows.Close()

	var traders []TraderRecord
	for rows.Next() {
		var trader TraderRecord
		var tradingSymbols, customPrompt, systemPromptTemplate sql.NullString

		err := rows.Scan(
			&trader.ID, &trader.UserID, &trader.Name, &trader.AIModelID, &trader.ExchangeID,
			&trader.InitialBalance, &trader.ScanIntervalMinutes, &trader.IsRunning,
			&trader.BTCETHLeverage, &trader.AltcoinLeverage, &tradingSymbols,
			&trader.UseCoinPool, &trader.UseOITop, &customPrompt,
			&trader.OverrideBasePrompt, &systemPromptTemplate, &trader.IsCrossMargin,
		)
		if err != nil {
			return nil, fmt.Errorf("扫描交易员数据失败: %w", err)
		}

		if tradingSymbols.Valid {
			trader.TradingSymbols = tradingSymbols.String
		}
		if customPrompt.Valid {
			trader.CustomPrompt = customPrompt.String
		}
		if systemPromptTemplate.Valid {
			trader.SystemPromptTemplate = systemPromptTemplate.String
		}

		traders = append(traders, trader)
	}

	return traders, nil
}

// UpdateTrader 更新交易员配置
func (d *Database) UpdateTrader(trader *TraderRecord) error {
	var err error
	if d.usePostgreSQL {
		_, err = d.db.Exec(`
			UPDATE traders SET
				name = $1, ai_model_id = $2, exchange_id = $3, initial_balance = $4,
				scan_interval_minutes = $5, btc_eth_leverage = $6, altcoin_leverage = $7,
				trading_symbols = $8, custom_prompt = $9, override_base_prompt = $10,
				system_prompt_template = $11, is_cross_margin = $12, updated_at = NOW()
			WHERE id = $13 AND user_id = $14
		`, trader.Name, trader.AIModelID, trader.ExchangeID, trader.InitialBalance,
			trader.ScanIntervalMinutes, trader.BTCETHLeverage, trader.AltcoinLeverage,
			trader.TradingSymbols, trader.CustomPrompt, trader.OverrideBasePrompt,
			trader.SystemPromptTemplate, trader.IsCrossMargin, trader.ID, trader.UserID)
	} else {
		_, err = d.db.Exec(`
			UPDATE traders SET
				name = ?, ai_model_id = ?, exchange_id = ?, initial_balance = ?,
				scan_interval_minutes = ?, btc_eth_leverage = ?, altcoin_leverage = ?,
				trading_symbols = ?, custom_prompt = ?, override_base_prompt = ?,
				system_prompt_template = ?, is_cross_margin = ?, updated_at = CURRENT_TIMESTAMP
			WHERE id = ? AND user_id = ?
		`, trader.Name, trader.AIModelID, trader.ExchangeID, trader.InitialBalance,
			trader.ScanIntervalMinutes, trader.BTCETHLeverage, trader.AltcoinLeverage,
			trader.TradingSymbols, trader.CustomPrompt, trader.OverrideBasePrompt,
			trader.SystemPromptTemplate, trader.IsCrossMargin, trader.ID, trader.UserID)
	}
	return err
}

// UpdateTraderCustomPrompt 更新交易员自定义Prompt
func (d *Database) UpdateTraderCustomPrompt(userID, id string, customPrompt string, overrideBase bool) error {
	var err error
	if d.usePostgreSQL {
		_, err = d.db.Exec(`UPDATE traders SET custom_prompt = $1, override_base_prompt = $2 WHERE id = $3 AND user_id = $4`, customPrompt, overrideBase, id, userID)
	} else {
		_, err = d.db.Exec(`UPDATE traders SET custom_prompt = ?, override_base_prompt = ? WHERE id = ? AND user_id = ?`, customPrompt, overrideBase, id, userID)
	}
	return err
}

// UpdateTraderInitialBalance 更新交易员初始余额（用于自动同步交易所实际余额）
func (d *Database) UpdateTraderInitialBalance(userID, id string, newBalance float64) error {
	var err error
	if d.usePostgreSQL {
		_, err = d.db.Exec(`UPDATE traders SET initial_balance = $1 WHERE id = $2 AND user_id = $3`, newBalance, id, userID)
	} else {
		_, err = d.db.Exec(`UPDATE traders SET initial_balance = ? WHERE id = ? AND user_id = ?`, newBalance, id, userID)
	}
	return err
}

// DeleteTrader 删除交易员
func (d *Database) DeleteTrader(userID, id string) error {
	var err error
	if d.usePostgreSQL {
		_, err = d.db.Exec(`DELETE FROM traders WHERE id = $1 AND user_id = $2`, id, userID)
	} else {
		_, err = d.db.Exec(`DELETE FROM traders WHERE id = ? AND user_id = ?`, id, userID)
	}
	return err
}

// DeleteAIModel 删除用户的AI模型配置
func (d *Database) DeleteAIModel(userID, id string) error {
	var err error
	if d.usePostgreSQL {
		_, err = d.db.Exec(`DELETE FROM ai_models WHERE id = $1 AND user_id = $2`, id, userID)
	} else {
		_, err = d.db.Exec(`DELETE FROM ai_models WHERE id = ? AND user_id = ?`, id, userID)
	}
	return err
}

// IsModelUsedByTrader 检查AI模型是否被交易员使用
func (d *Database) IsModelUsedByTrader(userID, modelID string) (bool, error) {
	var count int
	var err error
	if d.usePostgreSQL {
		err = d.db.QueryRow(`SELECT COUNT(*) FROM traders WHERE user_id = $1 AND ai_model_id = $2`, userID, modelID).Scan(&count)
	} else {
		err = d.db.QueryRow(`SELECT COUNT(*) FROM traders WHERE user_id = ? AND ai_model_id = ?`, userID, modelID).Scan(&count)
	}
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// DeleteExchange 删除用户的交易所配置
func (d *Database) DeleteExchange(userID, id string) error {
	var err error
	if d.usePostgreSQL {
		_, err = d.db.Exec(`DELETE FROM exchanges WHERE id = $1 AND user_id = $2`, id, userID)
	} else {
		_, err = d.db.Exec(`DELETE FROM exchanges WHERE id = ? AND user_id = ?`, id, userID)
	}
	return err
}

// IsExchangeUsedByTrader 检查交易所是否被交易员使用
func (d *Database) IsExchangeUsedByTrader(userID, exchangeID string) (bool, error) {
	var count int
	var err error
	if d.usePostgreSQL {
		err = d.db.QueryRow(`SELECT COUNT(*) FROM traders WHERE user_id = $1 AND exchange_id = $2`, userID, exchangeID).Scan(&count)
	} else {
		err = d.db.QueryRow(`SELECT COUNT(*) FROM traders WHERE user_id = ? AND exchange_id = ?`, userID, exchangeID).Scan(&count)
	}
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// GetTraderConfig 获取交易员完整配置（包含AI模型和交易所信息）
func (d *Database) GetTraderConfig(userID, traderID string) (*TraderRecord, *AIModelConfig, *ExchangeConfig, error) {
	var trader TraderRecord
	var aiModel AIModelConfig
	var exchange ExchangeConfig

	var err error
	if d.usePostgreSQL {
		err = d.db.QueryRow(`
			SELECT
				t.id, t.user_id, t.name, t.ai_model_id, t.exchange_id, t.initial_balance, t.scan_interval_minutes, t.is_running,
				COALESCE(t.btc_eth_leverage, 5) as btc_eth_leverage,
				COALESCE(t.altcoin_leverage, 5) as altcoin_leverage,
				COALESCE(t.trading_symbols, '') as trading_symbols,
				COALESCE(t.use_coin_pool, FALSE) as use_coin_pool,
				COALESCE(t.use_oi_top, FALSE) as use_oi_top,
				COALESCE(t.custom_prompt, '') as custom_prompt,
				COALESCE(t.override_base_prompt, FALSE) as override_base_prompt,
				COALESCE(t.system_prompt_template, 'default') as system_prompt_template,
				COALESCE(t.is_cross_margin, TRUE) as is_cross_margin,
				t.created_at, t.updated_at,
				a.id, a.user_id, a.name, a.provider, a.enabled, a.api_key,
				COALESCE(a.custom_api_url, '') as custom_api_url,
				COALESCE(a.custom_model_name, '') as custom_model_name,
				a.created_at, a.updated_at,
				e.id, e.user_id, e.name, e.type, e.enabled, e.api_key, e.secret_key, e.testnet,
				COALESCE(e.hyperliquid_wallet_addr, '') as hyperliquid_wallet_addr,
				COALESCE(e.aster_user, '') as aster_user,
				COALESCE(e.aster_signer, '') as aster_signer,
				COALESCE(e.aster_private_key, '') as aster_private_key,
				e.created_at, e.updated_at
			FROM traders t
			JOIN ai_models a ON t.ai_model_id = a.id AND t.user_id = a.user_id
			JOIN exchanges e ON t.exchange_id = e.id AND t.user_id = e.user_id
			WHERE t.id = $1 AND t.user_id = $2
		`, traderID, userID).Scan(
			&trader.ID, &trader.UserID, &trader.Name, &trader.AIModelID, &trader.ExchangeID,
			&trader.InitialBalance, &trader.ScanIntervalMinutes, &trader.IsRunning,
			&trader.BTCETHLeverage, &trader.AltcoinLeverage, &trader.TradingSymbols,
			&trader.UseCoinPool, &trader.UseOITop,
			&trader.CustomPrompt, &trader.OverrideBasePrompt, &trader.SystemPromptTemplate,
			&trader.IsCrossMargin,
			&trader.CreatedAt, &trader.UpdatedAt,
			&aiModel.ID, &aiModel.UserID, &aiModel.Name, &aiModel.Provider, &aiModel.Enabled, &aiModel.APIKey,
			&aiModel.CustomAPIURL, &aiModel.CustomModelName,
			&aiModel.CreatedAt, &aiModel.UpdatedAt,
			&exchange.ID, &exchange.UserID, &exchange.Name, &exchange.Type, &exchange.Enabled,
			&exchange.APIKey, &exchange.SecretKey, &exchange.Testnet,
			&exchange.HyperliquidWalletAddr, &exchange.AsterUser, &exchange.AsterSigner, &exchange.AsterPrivateKey,
			&exchange.CreatedAt, &exchange.UpdatedAt,
		)
	} else {
		err = d.db.QueryRow(`
			SELECT
				t.id, t.user_id, t.name, t.ai_model_id, t.exchange_id, t.initial_balance, t.scan_interval_minutes, t.is_running,
				COALESCE(t.btc_eth_leverage, 5) as btc_eth_leverage,
				COALESCE(t.altcoin_leverage, 5) as altcoin_leverage,
				COALESCE(t.trading_symbols, '') as trading_symbols,
				COALESCE(t.use_coin_pool, 0) as use_coin_pool,
				COALESCE(t.use_oi_top, 0) as use_oi_top,
				COALESCE(t.custom_prompt, '') as custom_prompt,
				COALESCE(t.override_base_prompt, 0) as override_base_prompt,
				COALESCE(t.system_prompt_template, 'default') as system_prompt_template,
				COALESCE(t.is_cross_margin, 1) as is_cross_margin,
				t.created_at, t.updated_at,
				a.id, a.user_id, a.name, a.provider, a.enabled, a.api_key,
				COALESCE(a.custom_api_url, '') as custom_api_url,
				COALESCE(a.custom_model_name, '') as custom_model_name,
				a.created_at, a.updated_at,
				e.id, e.user_id, e.name, e.type, e.enabled, e.api_key, e.secret_key, e.testnet,
				COALESCE(e.hyperliquid_wallet_addr, '') as hyperliquid_wallet_addr,
				COALESCE(e.aster_user, '') as aster_user,
				COALESCE(e.aster_signer, '') as aster_signer,
				COALESCE(e.aster_private_key, '') as aster_private_key,
				e.created_at, e.updated_at
			FROM traders t
			JOIN ai_models a ON t.ai_model_id = a.id AND t.user_id = a.user_id
			JOIN exchanges e ON t.exchange_id = e.id AND t.user_id = e.user_id
			WHERE t.id = ? AND t.user_id = ?
		`, traderID, userID).Scan(
			&trader.ID, &trader.UserID, &trader.Name, &trader.AIModelID, &trader.ExchangeID,
			&trader.InitialBalance, &trader.ScanIntervalMinutes, &trader.IsRunning,
			&trader.BTCETHLeverage, &trader.AltcoinLeverage, &trader.TradingSymbols,
			&trader.UseCoinPool, &trader.UseOITop,
			&trader.CustomPrompt, &trader.OverrideBasePrompt, &trader.SystemPromptTemplate,
			&trader.IsCrossMargin,
			&trader.CreatedAt, &trader.UpdatedAt,
			&aiModel.ID, &aiModel.UserID, &aiModel.Name, &aiModel.Provider, &aiModel.Enabled, &aiModel.APIKey,
			&aiModel.CustomAPIURL, &aiModel.CustomModelName,
			&aiModel.CreatedAt, &aiModel.UpdatedAt,
			&exchange.ID, &exchange.UserID, &exchange.Name, &exchange.Type, &exchange.Enabled,
			&exchange.APIKey, &exchange.SecretKey, &exchange.Testnet,
			&exchange.HyperliquidWalletAddr, &exchange.AsterUser, &exchange.AsterSigner, &exchange.AsterPrivateKey,
			&exchange.CreatedAt, &exchange.UpdatedAt,
		)
	}

	if err != nil {
		return nil, nil, nil, err
	}

	return &trader, &aiModel, &exchange, nil
}

// GetSystemConfig 获取系统配置
func (d *Database) GetSystemConfig(key string) (string, error) {
	var value string
	var err error
	if d.usePostgreSQL {
		err = d.db.QueryRow(`SELECT value FROM system_config WHERE key = $1`, key).Scan(&value)
	} else {
		err = d.db.QueryRow(`SELECT value FROM system_config WHERE key = ?`, key).Scan(&value)
	}
	return value, err
}

// SetSystemConfig 设置系统配置
func (d *Database) SetSystemConfig(key, value string) error {
	var err error
	if d.usePostgreSQL {
		_, err = d.db.Exec(`
			INSERT INTO system_config (key, value)
			VALUES ($1, $2)
			ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()
		`, key, value)
	} else {
		_, err = d.db.Exec(`
			INSERT OR REPLACE INTO system_config (key, value) VALUES (?, ?)
		`, key, value)
	}
	return err
}

// CreateUserSignalSource 创建用户信号源配置
func (d *Database) CreateUserSignalSource(userID, coinPoolURL, oiTopURL string) error {
	var err error
	if d.usePostgreSQL {
		_, err = d.db.Exec(`
			INSERT INTO user_signal_sources (user_id, coin_pool_url, oi_top_url, updated_at)
			VALUES ($1, $2, $3, NOW())
			ON CONFLICT (user_id) DO UPDATE SET
				coin_pool_url = EXCLUDED.coin_pool_url,
				oi_top_url = EXCLUDED.oi_top_url,
				updated_at = NOW()
		`, userID, coinPoolURL, oiTopURL)
	} else {
		_, err = d.db.Exec(`
			INSERT OR REPLACE INTO user_signal_sources (user_id, coin_pool_url, oi_top_url, updated_at)
			VALUES (?, ?, ?, CURRENT_TIMESTAMP)
		`, userID, coinPoolURL, oiTopURL)
	}
	return err
}

// GetUserSignalSource 获取用户信号源配置
func (d *Database) GetUserSignalSource(userID string) (*UserSignalSource, error) {
	var source UserSignalSource
	var err error
	if d.usePostgreSQL {
		err = d.db.QueryRow(`
			SELECT id, user_id, coin_pool_url, oi_top_url, created_at, updated_at
			FROM user_signal_sources WHERE user_id = $1
		`, userID).Scan(
			&source.ID, &source.UserID, &source.CoinPoolURL, &source.OITopURL,
			&source.CreatedAt, &source.UpdatedAt,
		)
	} else {
		err = d.db.QueryRow(`
			SELECT id, user_id, coin_pool_url, oi_top_url, created_at, updated_at
			FROM user_signal_sources WHERE user_id = ?
		`, userID).Scan(
			&source.ID, &source.UserID, &source.CoinPoolURL, &source.OITopURL,
			&source.CreatedAt, &source.UpdatedAt,
		)
	}
	if err != nil {
		return nil, err
	}
	return &source, nil
}

// UpdateUserSignalSource 更新用户信号源配置
func (d *Database) UpdateUserSignalSource(userID, coinPoolURL, oiTopURL string) error {
	var err error
	if d.usePostgreSQL {
		_, err = d.db.Exec(`
			UPDATE user_signal_sources SET coin_pool_url = $1, oi_top_url = $2, updated_at = NOW()
			WHERE user_id = $3
		`, coinPoolURL, oiTopURL, userID)
	} else {
		_, err = d.db.Exec(`
			UPDATE user_signal_sources SET coin_pool_url = ?, oi_top_url = ?, updated_at = CURRENT_TIMESTAMP
			WHERE user_id = ?
		`, coinPoolURL, oiTopURL, userID)
	}
	return err
}

// GetCustomCoins 获取所有交易员自定义币种 / Get all trader-customized currencies
func (d *Database) GetCustomCoins() []string {
	var symbol string
	var symbols []string
	_ = d.db.QueryRow(`
		SELECT GROUP_CONCAT(custom_coins , ',') as symbol
		FROM main.traders where custom_coins != ''
	`).Scan(&symbol)
	// 检测用户是否未配置币种 - 兼容性
	if symbol == "" {
		symbolJSON, _ := d.GetSystemConfig("default_coins")
		if err := json.Unmarshal([]byte(symbolJSON), &symbols); err != nil {
			log.Printf("⚠️  解析default_coins配置失败: %v，使用硬编码默认值", err)
			symbols = []string{"BTCUSDT", "ETHUSDT", "SOLUSDT", "BNBUSDT"}
		}
	}
	// filter Symbol
	for _, s := range strings.Split(symbol, ",") {
		if s == "" {
			continue
		}
		coin := market.Normalize(s)
		if !slices.Contains(symbols, coin) {
			symbols = append(symbols, coin)
		}
	}
	return symbols
}

// Close 关闭数据库连接
func (d *Database) Close() error {
	return d.db.Close()
}

// LoadBetaCodesFromFile 从文件加载内测码到数据库
func (d *Database) LoadBetaCodesFromFile(filePath string) error {
	// 读取文件内容
	content, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("读取内测码文件失败: %w", err)
	}

	// 按行分割内测码
	lines := strings.Split(string(content), "\n")
	var codes []string
	for _, line := range lines {
		code := strings.TrimSpace(line)
		if code != "" && !strings.HasPrefix(code, "#") {
			codes = append(codes, code)
		}
	}

	// 批量插入内测码
	tx, err := d.db.Begin()
	if err != nil {
		return fmt.Errorf("开始事务失败: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO beta_codes (code) VALUES (?)`)
	if err != nil {
		return fmt.Errorf("准备语句失败: %w", err)
	}
	defer stmt.Close()

	insertedCount := 0
	for _, code := range codes {
		result, err := stmt.Exec(code)
		if err != nil {
			log.Printf("插入内测码 %s 失败: %v", code, err)
			continue
		}

		if rowsAffected, _ := result.RowsAffected(); rowsAffected > 0 {
			insertedCount++
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交事务失败: %w", err)
	}

	log.Printf("✅ 成功加载 %d 个内测码到数据库 (总计 %d 个)", insertedCount, len(codes))
	return nil
}

// ValidateBetaCode 验证内测码是否有效且未使用
func (d *Database) ValidateBetaCode(code string) (bool, error) {
	var used bool
	err := d.db.QueryRow(`SELECT used FROM beta_codes WHERE code = ?`, code).Scan(&used)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil // 内测码不存在
		}
		return false, err
	}
	return !used, nil // 内测码存在且未使用
}

// UseBetaCode 使用内测码（标记为已使用）
func (d *Database) UseBetaCode(code, userEmail string) error {
	result, err := d.db.Exec(`
		UPDATE beta_codes SET used = 1, used_by = ?, used_at = CURRENT_TIMESTAMP 
		WHERE code = ? AND used = 0
	`, userEmail, code)
	if err != nil {
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rowsAffected == 0 {
		return fmt.Errorf("内测码无效或已被使用")
	}

	return nil
}

// GetBetaCodeStats 获取内测码统计信息
func (d *Database) GetBetaCodeStats() (total, used int, err error) {
	err = d.db.QueryRow(`SELECT COUNT(*) FROM beta_codes`).Scan(&total)
	if err != nil {
		return 0, 0, err
	}

	err = d.db.QueryRow(`SELECT COUNT(*) FROM beta_codes WHERE used = 1`).Scan(&used)
	if err != nil {
		return 0, 0, err
	}

	return total, used, nil
}

// SetCryptoService 设置加密服务
func (d *Database) SetCryptoService(cs *crypto.CryptoService) {
	d.cryptoService = cs
}

// encryptSensitiveData 加密敏感数据用于存储
func (d *Database) encryptSensitiveData(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}

	if d.cryptoService == nil {
		log.Printf("🚨 CRITICAL: 加密服务未初始化，无法加密敏感数据")
		return "", fmt.Errorf("加密服务未初始化")
	}

	encrypted, err := d.cryptoService.EncryptForStorage(plaintext)
	if err != nil {
		log.Printf("🚨 CRITICAL: 敏感数据加密失败，数据将不被保存: %v", err)
		return "", fmt.Errorf("敏感数据加密失败: %w", err)
	}

	return encrypted, nil
}

// decryptSensitiveData 解密敏感数据
func (d *Database) decryptSensitiveData(encrypted string) (string, error) {
	if encrypted == "" {
		return "", nil
	}

	if d.cryptoService == nil {
		log.Printf("🚨 CRITICAL: 解密服务未初始化，无法解密敏感数据")
		return "", fmt.Errorf("解密服务未初始化")
	}

	// 如果不是加密格式，可能是旧数据，需要特殊处理
	if !d.cryptoService.IsEncryptedStorageValue(encrypted) {
		log.Printf("🚨 WARNING: 检测到未加密的敏感数据，这可能是安全风险")
		return "", fmt.Errorf("检测到未加密的敏感数据，系统安全可能受损")
	}

	decrypted, err := d.cryptoService.DecryptFromStorage(encrypted)
	if err != nil {
		log.Printf("🚨 CRITICAL: 敏感数据解密失败，数据可能已损坏: %v", err)
		return "", fmt.Errorf("敏感数据解密失败: %w", err)
	}

	return decrypted, nil
}

const secretValuePrefix = "SEC:v1:"

type secretCipher struct {
	once sync.Once
	key  []byte
	err  error
}

var tgSecretCipher secretCipher

func (sc *secretCipher) loadKey() error {
	sc.once.Do(func() {
		keyStr := strings.TrimSpace(os.Getenv("SECRET_ENCRYPTION_KEY"))
		if keyStr == "" {
			sc.err = fmt.Errorf("SECRET_ENCRYPTION_KEY not set")
			return
		}

		if key, ok := decodeKeyMaterial(keyStr); ok {
			sc.key = key
			return
		}

		sum := sha256.Sum256([]byte(keyStr))
		key := make([]byte, len(sum))
		copy(key, sum[:])
		sc.key = key
	})
	return sc.err
}

func decodeKeyMaterial(value string) ([]byte, bool) {
	decoders := []func(string) ([]byte, error){
		base64.StdEncoding.DecodeString,
		base64.RawStdEncoding.DecodeString,
		func(s string) ([]byte, error) { return hex.DecodeString(s) },
	}

	for _, decoder := range decoders {
		if decoded, err := decoder(value); err == nil {
			switch len(decoded) {
			case 16, 24, 32:
				return decoded, true
			default:
				sum := sha256.Sum256(decoded)
				key := make([]byte, len(sum))
				copy(key, sum[:])
				return key, true
			}
		}
	}
	return nil, false
}

func (sc *secretCipher) encrypt(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if err := sc.loadKey(); err != nil {
		return value, err
	}

	block, err := aes.NewCipher(sc.key)
	if err != nil {
		return value, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return value, err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return value, err
	}

	ciphertext := gcm.Seal(nil, nonce, []byte(value), nil)

	return secretValuePrefix +
		base64.StdEncoding.EncodeToString(nonce) + ":" +
		base64.StdEncoding.EncodeToString(ciphertext), nil
}

func (sc *secretCipher) decrypt(value string) (string, error) {
	if value == "" || !strings.HasPrefix(value, secretValuePrefix) {
		return value, nil
	}

	if err := sc.loadKey(); err != nil {
		return value, err
	}

	payload := strings.TrimPrefix(value, secretValuePrefix)
	parts := strings.SplitN(payload, ":", 2)
	if len(parts) != 2 {
		return value, fmt.Errorf("invalid secret payload format")
	}

	nonce, err := base64.StdEncoding.DecodeString(parts[0])
	if err != nil {
		return value, err
	}
	ciphertext, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return value, err
	}

	block, err := aes.NewCipher(sc.key)
	if err != nil {
		return value, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return value, err
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return value, err
	}
	return string(plaintext), nil
}

func (d *Database) encryptSecretValue(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	encrypted, err := tgSecretCipher.encrypt(plaintext)
	if err != nil {
		log.Printf("🚨 CRITICAL: Telegram密钥加密失败，数据将不被保存: %v", err)
		return "", fmt.Errorf("Telegram密钥加密失败: %w", err)
	}
	return encrypted, nil
}

func (d *Database) decryptSecretValue(encrypted string) (string, error) {
	if encrypted == "" {
		return "", nil
	}
	decrypted, err := tgSecretCipher.decrypt(encrypted)
	if err != nil {
		log.Printf("🚨 CRITICAL: Telegram密钥解密失败，数据可能已损坏: %v", err)
		return "", fmt.Errorf("Telegram密钥解密失败: %w", err)
	}
	return decrypted, nil
}

// TGUser 数据库相关实现

// CreateTGUserTable 创建 tg_users 表
func (d *Database) CreateTGUserTable() error {
	var query string
	if d.usePostgreSQL {
		query = `
		CREATE TABLE IF NOT EXISTS tg_users (
			id TEXT PRIMARY KEY DEFAULT uuid_generate_v4(),
			telegram_id BIGINT UNIQUE NOT NULL,
			telegram_username VARCHAR(255),
			telegram_first_name VARCHAR(255),
			telegram_chat_id BIGINT NOT NULL,
			language_code VARCHAR(10) DEFAULT 'en',
			current_action VARCHAR(100) DEFAULT 'idle',
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
		DROP TRIGGER IF EXISTS update_tg_users_updated_at ON tg_users;
		CREATE TRIGGER update_tg_users_updated_at
		BEFORE UPDATE ON tg_users
		FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
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

	_, err := d.db.Exec(query)
	if err != nil {
		return fmt.Errorf("创建 tg_users 表失败: %w", err)
	}

	log.Printf("✅ tg_users 表创建成功")
	return nil
}

// CreateTGUser 创建 Telegram 用户
func (d *Database) CreateTGUser(telegramID int64, username, firstName string, chatID int64, languageCode string) error {
	var query string
	var err error
	if d.usePostgreSQL {
		query = `
			INSERT INTO tg_users (telegram_id, telegram_username, telegram_first_name, telegram_chat_id,
			                      language_code, current_action, notification_enabled, is_active)
			VALUES ($1, $2, $3, $4, $5, 'account_created', TRUE, TRUE)
			RETURNING id
		`
		err = d.db.QueryRow(query, telegramID, username, firstName, chatID, languageCode).Scan(new(string))
	} else {
		query = `
			INSERT INTO tg_users (id, telegram_id, telegram_username, telegram_first_name, telegram_chat_id,
			                      language_code, current_action, notification_enabled, is_active)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		`
		id := fmt.Sprintf("tg_%d_%d", telegramID, time.Now().Unix())
		_, err = d.db.Exec(query, id, telegramID, username, firstName, chatID, languageCode, "account_created", true, true)
	}

	if err != nil {
		return fmt.Errorf("创建 TGUser 失败: %w", err)
	}

	return nil
}

// GetTGUserByTelegramID 通过 Telegram ID 获取用户
func (d *Database) GetTGUserByTelegramID(telegramID int64) (interface{}, error) {
	var id, telegramUsername, telegramFirstName, languageCode, currentAction string
	var telegramChatID int64
	var notificationEnabled, isActive bool
	var lastInteractionAt, createdAt, updatedAt time.Time

	var query string
	if d.usePostgreSQL {
		query = `
			SELECT id, telegram_id, telegram_username, telegram_first_name, telegram_chat_id,
			       language_code, current_action, notification_enabled,
			       last_interaction_at, is_active, created_at, updated_at
			FROM tg_users WHERE telegram_id = $1
		`
		err := d.db.QueryRow(query, telegramID).Scan(
			&id, &telegramID, &telegramUsername, &telegramFirstName, &telegramChatID,
			&languageCode, &currentAction, &notificationEnabled,
			&lastInteractionAt, &isActive, &createdAt, &updatedAt,
		)
		if err != nil {
			return nil, err
		}
	} else {
		query = `
			SELECT id, telegram_id, telegram_username, telegram_first_name, telegram_chat_id,
			       language_code, current_action, notification_enabled,
			       last_interaction_at, is_active, created_at, updated_at
			FROM tg_users WHERE telegram_id = ?
		`
		err := d.db.QueryRow(query, telegramID).Scan(
			&id, &telegramID, &telegramUsername, &telegramFirstName, &telegramChatID,
			&languageCode, &currentAction, &notificationEnabled,
			&lastInteractionAt, &isActive, &createdAt, &updatedAt,
		)
		if err != nil {
			return nil, err
		}
	}

	user := map[string]interface{}{
		"id":                   id,
		"telegram_id":          telegramID,
		"telegram_username":    telegramUsername,
		"telegram_first_name":  telegramFirstName,
		"telegram_chat_id":     telegramChatID,
		"language_code":        languageCode,
		"current_action":       currentAction,
		"notification_enabled": notificationEnabled,
		"last_interaction_at":  lastInteractionAt,
		"is_active":            isActive,
		"created_at":           createdAt,
		"updated_at":           updatedAt,
	}

	return user, nil
}

// GetTGUserByChatID 通过 Chat ID 获取用户
func (d *Database) GetTGUserByChatID(chatID int64) (interface{}, error) {
	var id, telegramUsername, telegramFirstName, languageCode, currentAction string
	var telegramID int64
	var notificationEnabled, isActive bool
	var lastInteractionAt, createdAt, updatedAt time.Time

	var query string
	if d.usePostgreSQL {
		query = `
			SELECT id, telegram_id, telegram_username, telegram_first_name, telegram_chat_id,
			       language_code, current_action, notification_enabled,
			       last_interaction_at, is_active, created_at, updated_at
			FROM tg_users WHERE telegram_chat_id = $1
		`
		err := d.db.QueryRow(query, chatID).Scan(
			&id, &telegramID, &telegramUsername, &telegramFirstName, &chatID,
			&languageCode, &currentAction, &notificationEnabled,
			&lastInteractionAt, &isActive, &createdAt, &updatedAt,
		)
		if err != nil {
			return nil, err
		}
	} else {
		query = `
			SELECT id, telegram_id, telegram_username, telegram_first_name, telegram_chat_id,
			       language_code, current_action, notification_enabled,
			       last_interaction_at, is_active, created_at, updated_at
			FROM tg_users WHERE telegram_chat_id = ?
		`
		err := d.db.QueryRow(query, chatID).Scan(
			&id, &telegramID, &telegramUsername, &telegramFirstName, &chatID,
			&languageCode, &currentAction, &notificationEnabled,
			&lastInteractionAt, &isActive, &createdAt, &updatedAt,
		)
		if err != nil {
			return nil, err
		}
	}

	user := map[string]interface{}{
		"id":                   id,
		"telegram_id":          telegramID,
		"telegram_username":    telegramUsername,
		"telegram_first_name":  telegramFirstName,
		"telegram_chat_id":     chatID,
		"language_code":        languageCode,
		"current_action":       currentAction,
		"notification_enabled": notificationEnabled,
		"last_interaction_at":  lastInteractionAt,
		"is_active":            isActive,
		"created_at":           createdAt,
		"updated_at":           updatedAt,
	}

	return user, nil
}

// UpdateTGUserAction 更新用户当前操作
func (d *Database) UpdateTGUserAction(telegramID int64, action string) error {
	var query string
	if d.usePostgreSQL {
		query = `
			UPDATE tg_users
			SET current_action = $1, last_interaction_at = NOW()
			WHERE telegram_id = $2
		`
		_, err := d.db.Exec(query, action, telegramID)
		return err
	} else {
		query = `
			UPDATE tg_users
			SET current_action = ?, last_interaction_at = CURRENT_TIMESTAMP
			WHERE telegram_id = ?
		`
		_, err := d.db.Exec(query, action, telegramID)
		return err
	}
}

// UpdateTGUserLastInteraction 更新用户最后交互时间
func (d *Database) UpdateTGUserLastInteraction(telegramID int64) error {
	var query string
	if d.usePostgreSQL {
		query = `UPDATE tg_users SET last_interaction_at = NOW() WHERE telegram_id = $1`
		_, err := d.db.Exec(query, telegramID)
		return err
	} else {
		query = `UPDATE tg_users SET last_interaction_at = CURRENT_TIMESTAMP WHERE telegram_id = ?`
		_, err := d.db.Exec(query, telegramID)
		return err
	}
}

// EnsureUserInUsersTable 确保用户在users表中存在
func (d *Database) EnsureUserInUsersTable(userID string) error {
	// 检查用户是否在users表中存在
	var count int
	err := d.db.QueryRow(`
		SELECT COUNT(*) FROM users WHERE id = ?
	`, userID).Scan(&count)
	if err != nil {
		return fmt.Errorf("查询users表失败: %w", err)
	}

	// 如果不存在，创建一个记录
	if count == 0 {
		_, err = d.db.Exec(`
			INSERT INTO users (id, email, password_hash, otp_verified)
			VALUES (?, ?, ?, 1)
		`, userID, userID+"@telegram.local", "telegram_user")
		if err != nil {
			return fmt.Errorf("创建用户记录失败: %w", err)
		}
		log.Printf("✅ 为Telegram用户创建users表记录: %s", userID)
	}

	return nil
}

// CreateTgTrader 创建TG交易员
func (d *Database) CreateTgTrader(tgUserID int64, traderRecord *TgTraderRecord) error {
	encryptedAPIKey, err := d.encryptSecretValue(traderRecord.AIModelAPIKey)
	if err != nil {
		log.Printf("🚨 CRITICAL: 无法加密AI模型API密钥，交易员将不被创建: %v", err)
		return fmt.Errorf("无法加密AI模型API密钥: %w", err)
	}
	encryptedPrivateKey, err := d.encryptSecretValue(traderRecord.PrivateKey)
	if err != nil {
		log.Printf("🚨 CRITICAL: 无法加密私钥，交易员将不被创建: %v", err)
		return fmt.Errorf("无法加密私钥: %w", err)
	}

	if d.usePostgreSQL {
		query := `
			INSERT INTO tg_traders (
				id, tg_user_id, name, ai_model_id, ai_model_name, exchange_id,
				initial_balance, scan_interval_minutes, is_running, is_configured,
				btc_eth_leverage, altcoin_leverage, trading_symbols,
				use_coin_pool, use_oi_top, custom_prompt, override_base_prompt,
				is_cross_margin, use_default_coins, custom_coins,
				system_prompt_template, ai_model_api_key, ai_model_api_url,
				private_key, wallet_address, created_at, updated_at
			) VALUES (
				$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, NOW(), NOW()
			)`
		_, err := d.db.Exec(query,
			traderRecord.ID, traderRecord.TgUserID, traderRecord.Name, traderRecord.AIModelID, traderRecord.AIModelName,
			traderRecord.ExchangeID, traderRecord.InitialBalance, traderRecord.ScanIntervalMinutes,
			traderRecord.IsRunning, traderRecord.IsConfigured, traderRecord.BTCETHLeverage, traderRecord.AltcoinLeverage,
			traderRecord.TradingSymbols, traderRecord.UseCoinPool, traderRecord.UseOITop,
			traderRecord.CustomPrompt, traderRecord.OverrideBasePrompt, traderRecord.IsCrossMargin,
			traderRecord.UseDefaultCoins, traderRecord.CustomCoins, traderRecord.SystemPromptTemplate,
			encryptedAPIKey, traderRecord.AIModelAPIURL,
			encryptedPrivateKey, traderRecord.WalletAddress,
		)
		return err
	} else {
		query := `
			INSERT INTO tg_traders (
				id, tg_user_id, name, ai_model_id, ai_model_name, exchange_id,
				initial_balance, scan_interval_minutes, is_running, is_configured,
				btc_eth_leverage, altcoin_leverage, trading_symbols,
				use_coin_pool, use_oi_top, custom_prompt, override_base_prompt,
				is_cross_margin, use_default_coins, custom_coins,
				system_prompt_template, ai_model_api_key, ai_model_api_url,
				private_key, wallet_address, created_at, updated_at
			) VALUES (
				?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
			)`
		_, err := d.db.Exec(query,
			traderRecord.ID, traderRecord.TgUserID, traderRecord.Name, traderRecord.AIModelID, traderRecord.AIModelName,
			traderRecord.ExchangeID, traderRecord.InitialBalance, traderRecord.ScanIntervalMinutes,
			traderRecord.IsRunning, traderRecord.IsConfigured, traderRecord.BTCETHLeverage, traderRecord.AltcoinLeverage,
			traderRecord.TradingSymbols, traderRecord.UseCoinPool, traderRecord.UseOITop,
			traderRecord.CustomPrompt, traderRecord.OverrideBasePrompt, traderRecord.IsCrossMargin,
			traderRecord.UseDefaultCoins, traderRecord.CustomCoins, traderRecord.SystemPromptTemplate,
			encryptedAPIKey, traderRecord.AIModelAPIURL,
			encryptedPrivateKey, traderRecord.WalletAddress,
		)
		return err
	}
}

// GetTgTraders 获取用户的TG交易员列表
func (d *Database) GetTgTraders(tgUserID int64) ([]TgTraderRecord, error) {
	var query string
	if d.usePostgreSQL {
		query = `
			SELECT id, tg_user_id, name, ai_model_id, COALESCE(ai_model_name, '') AS ai_model_name, exchange_id,
				   initial_balance, scan_interval_minutes, is_running, is_configured,
				   btc_eth_leverage, altcoin_leverage, trading_symbols,
				   use_coin_pool, use_oi_top, custom_prompt, override_base_prompt,
				   is_cross_margin, use_default_coins, custom_coins,
				   system_prompt_template,
				   COALESCE(ai_model_api_key, '') AS ai_model_api_key,
				   COALESCE(ai_model_api_url, '') AS ai_model_api_url,
				   COALESCE(private_key, '') AS private_key,
				   COALESCE(wallet_address, '') AS wallet_address,
				   created_at, updated_at
			FROM tg_traders
			WHERE tg_user_id = $1
			ORDER BY created_at DESC
		`
	} else {
		query = `
			SELECT id, tg_user_id, name, ai_model_id, COALESCE(ai_model_name, '') AS ai_model_name, exchange_id,
				   initial_balance, scan_interval_minutes, is_running, is_configured,
				   btc_eth_leverage, altcoin_leverage, trading_symbols,
				   use_coin_pool, use_oi_top, custom_prompt, override_base_prompt,
				   is_cross_margin, use_default_coins, custom_coins,
				   system_prompt_template,
				   COALESCE(ai_model_api_key, '') AS ai_model_api_key,
				   COALESCE(ai_model_api_url, '') AS ai_model_api_url,
				   COALESCE(private_key, '') AS private_key,
				   COALESCE(wallet_address, '') AS wallet_address,
				   created_at, updated_at
			FROM tg_traders
			WHERE tg_user_id = ?
			ORDER BY created_at DESC
		`
	}

	rows, err := d.db.Query(query, tgUserID)
	if err != nil {
		return nil, fmt.Errorf("查询tg_traders失败: %w", err)
	}
	defer rows.Close()

	var traders []TgTraderRecord
	for rows.Next() {
		var trader TgTraderRecord
		err := rows.Scan(
			&trader.ID, &trader.TgUserID, &trader.Name, &trader.AIModelID, &trader.AIModelName,
			&trader.ExchangeID, &trader.InitialBalance, &trader.ScanIntervalMinutes,
			&trader.IsRunning, &trader.IsConfigured, &trader.BTCETHLeverage, &trader.AltcoinLeverage,
			&trader.TradingSymbols, &trader.UseCoinPool, &trader.UseOITop,
			&trader.CustomPrompt, &trader.OverrideBasePrompt, &trader.IsCrossMargin,
			&trader.UseDefaultCoins, &trader.CustomCoins, &trader.SystemPromptTemplate,
			&trader.AIModelAPIKey, &trader.AIModelAPIURL, &trader.PrivateKey, &trader.WalletAddress, &trader.CreatedAt, &trader.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("扫描tg_traders行失败: %w", err)
		}

		decryptedAPIKey, err := d.decryptSecretValue(trader.AIModelAPIKey)
		if err != nil {
			log.Printf("🚨 CRITICAL: 无法解密AI模型API密钥: %v", err)
			return nil, fmt.Errorf("无法解密AI模型API密钥: %w", err)
		}
		trader.AIModelAPIKey = decryptedAPIKey
		decryptedPrivateKey, err := d.decryptSecretValue(trader.PrivateKey)
		if err != nil {
			log.Printf("🚨 CRITICAL: 无法解密私钥: %v", err)
			return nil, fmt.Errorf("无法解密私钥: %w", err)
		}
		trader.PrivateKey = decryptedPrivateKey
		traders = append(traders, trader)
	}

	return traders, nil
}

// UpdateTgTraderStatus 更新TG交易员状态
func (d *Database) UpdateTgTraderStatus(tgUserID int64, traderID string, isRunning bool) error {
	var query string
	var args []interface{}

	if d.usePostgreSQL {
		query = `
			UPDATE tg_traders
			SET is_running = $1, updated_at = NOW()
			WHERE tg_user_id = $2 AND id = $3
		`
		args = []interface{}{isRunning, tgUserID, traderID}
	} else {
		query = `
			UPDATE tg_traders
			SET is_running = ?, updated_at = CURRENT_TIMESTAMP
			WHERE tg_user_id = ? AND id = ?
		`
		args = []interface{}{isRunning, tgUserID, traderID}
	}

	result, err := d.db.Exec(query, args...)
	if err != nil {
		return fmt.Errorf("更新TG交易员状态失败: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("获取影响行数失败: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("未找到匹配的TG交易员记录")
	}

	return nil
}

// UpdateTgTraderInitialBalance 更新TG交易员初始余额
func (d *Database) UpdateTgTraderInitialBalance(tgUserID int64, traderID string, newBalance float64) error {
	var query string
	var args []interface{}

	if d.usePostgreSQL {
		query = `
			UPDATE tg_traders
			SET initial_balance = $1, updated_at = NOW()
			WHERE tg_user_id = $2 AND id = $3
		`
		args = []interface{}{newBalance, tgUserID, traderID}
	} else {
		query = `
			UPDATE tg_traders
			SET initial_balance = ?, updated_at = CURRENT_TIMESTAMP
			WHERE tg_user_id = ? AND id = ?
		`
		args = []interface{}{newBalance, tgUserID, traderID}
	}

	result, err := d.db.Exec(query, args...)
	if err != nil {
		return fmt.Errorf("更新TG交易员初始余额失败: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("获取影响行数失败: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("未找到匹配的TG交易员记录")
	}

	return nil
}

// UpdateTgTraderAPIConfig 更新TG交易员AI配置
func (d *Database) UpdateTgTraderAPIConfig(tgUserID int64, traderID string, aiModelID string, apiKey string, aiModel string) error {
	encryptedAPIKey, err := d.encryptSecretValue(apiKey)
	if err != nil {
		log.Printf("🚨 CRITICAL: 无法加密AI模型API密钥，配置将不被更新: %v", err)
		return fmt.Errorf("无法加密AI模型API密钥: %w", err)
	}

	var query string
	var args []interface{}

	if d.usePostgreSQL {
		query = `
			UPDATE tg_traders
			SET ai_model_id = $1,
				ai_model_name = $2,
				ai_model_api_key = $3,
				updated_at = NOW()
			WHERE tg_user_id = $4 AND id = $5
		`
		args = []interface{}{aiModelID, aiModel, encryptedAPIKey, tgUserID, traderID}
	} else {
		query = `
			UPDATE tg_traders
			SET ai_model_id = ?,
				ai_model_name = ?,
				ai_model_api_key = ?,
				updated_at = CURRENT_TIMESTAMP
			WHERE tg_user_id = ? AND id = ?
		`
		args = []interface{}{aiModelID, aiModel, encryptedAPIKey, tgUserID, traderID}
	}

	result, err := d.db.Exec(query, args...)
	if err != nil {
		return fmt.Errorf("更新TG交易员AI配置失败: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("获取影响行数失败: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("未找到匹配的TG交易员记录")
	}

	return nil
}

// CreateTgGasSponsorship 记录 Gas 赞助
func (d *Database) CreateTgGasSponsorship(record *TgGasSponsorshipRecord) (int64, error) {
	if record.Status == "" {
		record.Status = "processing"
	}
	if record.AmountWei == "" {
		record.AmountWei = "0"
	}

	var query string
	var args []interface{}
	if d.usePostgreSQL {
		query = `
				INSERT INTO tg_gas_sponsorships (tg_user_id, wallet_address, amount_wei, usdc_amount, gas_tx_hash, bridge_tx_hash, status, created_at, updated_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, NOW(), NOW())
				RETURNING id
			`
		args = []interface{}{record.TgUserID, record.WalletAddress, record.AmountWei, record.USDCAmount, record.GasTxHash, record.BridgeTxHash, record.Status}
		var id int64
		if err := d.db.QueryRow(query, args...).Scan(&id); err != nil {
			return 0, fmt.Errorf("创建Gas赞助记录失败: %w", err)
		}
		return id, nil
	}

	query = `
			INSERT INTO tg_gas_sponsorships (tg_user_id, wallet_address, amount_wei, usdc_amount, gas_tx_hash, bridge_tx_hash, status, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		`
	args = []interface{}{record.TgUserID, record.WalletAddress, record.AmountWei, record.USDCAmount, record.GasTxHash, record.BridgeTxHash, record.Status}
	result, err := d.db.Exec(query, args...)
	if err != nil {
		return 0, fmt.Errorf("创建Gas赞助记录失败: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	return id, nil
}

// UpdateTgGasSponsorshipProgress 更新赞助或桥接的状态
func (d *Database) UpdateTgGasSponsorshipProgress(id int64, status string, gasTxHash string, bridgeTxHash string, usdcAmount string) error {
	var query string
	var args []interface{}
	if d.usePostgreSQL {
		query = `
			UPDATE tg_gas_sponsorships
			SET status = CASE WHEN $1 <> '' THEN $1 ELSE status END,
				gas_tx_hash = CASE WHEN $2 <> '' THEN $2 ELSE gas_tx_hash END,
				bridge_tx_hash = CASE WHEN $3 <> '' THEN $3 ELSE bridge_tx_hash END,
				usdc_amount = CASE WHEN $4 <> '' THEN $4 ELSE usdc_amount END,
				updated_at = NOW()
			WHERE id = $5
		`
		args = []interface{}{status, gasTxHash, bridgeTxHash, usdcAmount, id}
	} else {
		query = `
			UPDATE tg_gas_sponsorships
			SET status = CASE WHEN ? <> '' THEN ? ELSE status END,
				gas_tx_hash = CASE WHEN ? <> '' THEN ? ELSE gas_tx_hash END,
				bridge_tx_hash = CASE WHEN ? <> '' THEN ? ELSE bridge_tx_hash END,
				usdc_amount = CASE WHEN ? <> '' THEN ? ELSE usdc_amount END,
				updated_at = CURRENT_TIMESTAMP
			WHERE id = ?
		`
		args = []interface{}{status, status, gasTxHash, gasTxHash, bridgeTxHash, bridgeTxHash, usdcAmount, usdcAmount, id}
	}

	if _, err := d.db.Exec(query, args...); err != nil {
		return fmt.Errorf("更新Gas赞助状态失败: %w", err)
	}
	return nil
}

// HasRecentGasSponsorship 判断近期是否已经赞助过 gas
func (d *Database) HasRecentGasSponsorship(walletAddr string, withinHours int) (bool, error) {
	cutoff := time.Now().Add(-time.Duration(withinHours) * time.Hour)
	var query string
	var count int
	var err error

	if d.usePostgreSQL {
		query = `SELECT COUNT(*) FROM tg_gas_sponsorships WHERE wallet_address = $1 AND created_at >= $2 AND status IN ('gas_sent','completed')`
		err = d.db.QueryRow(query, walletAddr, cutoff).Scan(&count)
	} else {
		query = `SELECT COUNT(*) FROM tg_gas_sponsorships WHERE wallet_address = ? AND created_at >= ? AND status IN ('gas_sent','completed')`
		err = d.db.QueryRow(query, walletAddr, cutoff.Format("2006-01-02 15:04:05")).Scan(&count)
	}

	if err != nil {
		return false, fmt.Errorf("查询Gas赞助记录失败: %w", err)
	}
	return count > 0, nil
}

// GetActiveGasSponsorship 获取尚未完成的赞助记录
func (d *Database) GetActiveGasSponsorship(walletAddr string) (*TgGasSponsorshipRecord, error) {
	var query string
	var row *sql.Row
	if d.usePostgreSQL {
		query = `
			SELECT id, tg_user_id, wallet_address, amount_wei, usdc_amount, gas_tx_hash, bridge_tx_hash, status, created_at, updated_at
			FROM tg_gas_sponsorships
			WHERE wallet_address = $1 AND status NOT IN ('completed','failed')
			ORDER BY created_at DESC
			LIMIT 1
		`
		row = d.db.QueryRow(query, walletAddr)
	} else {
		query = `
			SELECT id, tg_user_id, wallet_address, amount_wei, usdc_amount, gas_tx_hash, bridge_tx_hash, status, created_at, updated_at
			FROM tg_gas_sponsorships
			WHERE wallet_address = ?
			  AND status NOT IN ('completed','failed')
			ORDER BY created_at DESC
			LIMIT 1
		`
		row = d.db.QueryRow(query, walletAddr)
	}

	var record TgGasSponsorshipRecord
	err := row.Scan(
		&record.ID,
		&record.TgUserID,
		&record.WalletAddress,
		&record.AmountWei,
		&record.USDCAmount,
		&record.GasTxHash,
		&record.BridgeTxHash,
		&record.Status,
		&record.CreatedAt,
		&record.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("查询Gas赞助记录失败: %w", err)
	}
	return &record, nil
}

// UpdateTgTraderConfig 根据配置向导结果更新交易员完整配置
func (d *Database) UpdateTgTraderConfig(tgUserID int64, traderID string, traderRecord *TgTraderRecord) error {
	encryptedAPIKey, err := d.encryptSecretValue(traderRecord.AIModelAPIKey)
	if err != nil {
		log.Printf("🚨 CRITICAL: 无法加密AI模型API密钥，配置将不被更新: %v", err)
		return fmt.Errorf("无法加密AI模型API密钥: %w", err)
	}

	var query string
	var args []interface{}

	if d.usePostgreSQL {
		query = `
			UPDATE tg_traders
			SET name = $1,
				ai_model_id = $2,
				ai_model_name = $3,
				exchange_id = $4,
				initial_balance = $5,
				scan_interval_minutes = $6,
				btc_eth_leverage = $7,
				altcoin_leverage = $8,
				trading_symbols = $9,
				use_coin_pool = $10,
				use_oi_top = $11,
				custom_prompt = $12,
				override_base_prompt = $13,
				is_cross_margin = $14,
				use_default_coins = $15,
				custom_coins = $16,
				system_prompt_template = $17,
				ai_model_api_key = $18,
				ai_model_api_url = $19,
				is_configured = $20,
				updated_at = NOW()
			WHERE tg_user_id = $21 AND id = $22
		`
		args = []interface{}{
			traderRecord.Name,
			traderRecord.AIModelID,
			traderRecord.AIModelName,
			traderRecord.ExchangeID,
			traderRecord.InitialBalance,
			traderRecord.ScanIntervalMinutes,
			traderRecord.BTCETHLeverage,
			traderRecord.AltcoinLeverage,
			traderRecord.TradingSymbols,
			traderRecord.UseCoinPool,
			traderRecord.UseOITop,
			traderRecord.CustomPrompt,
			traderRecord.OverrideBasePrompt,
			traderRecord.IsCrossMargin,
			traderRecord.UseDefaultCoins,
			traderRecord.CustomCoins,
			traderRecord.SystemPromptTemplate,
			encryptedAPIKey,
			traderRecord.AIModelAPIURL,
			traderRecord.IsConfigured,
			tgUserID,
			traderID,
		}
	} else {
		query = `
			UPDATE tg_traders
			SET name = ?,
				ai_model_id = ?,
				ai_model_name = ?,
				exchange_id = ?,
				initial_balance = ?,
				scan_interval_minutes = ?,
				btc_eth_leverage = ?,
				altcoin_leverage = ?,
				trading_symbols = ?,
				use_coin_pool = ?,
				use_oi_top = ?,
				custom_prompt = ?,
				override_base_prompt = ?,
				is_cross_margin = ?,
				use_default_coins = ?,
				custom_coins = ?,
				system_prompt_template = ?,
				ai_model_api_key = ?,
				ai_model_api_url = ?,
				is_configured = ?,
				updated_at = CURRENT_TIMESTAMP
			WHERE tg_user_id = ? AND id = ?
		`
		args = []interface{}{
			traderRecord.Name,
			traderRecord.AIModelID,
			traderRecord.AIModelName,
			traderRecord.ExchangeID,
			traderRecord.InitialBalance,
			traderRecord.ScanIntervalMinutes,
			traderRecord.BTCETHLeverage,
			traderRecord.AltcoinLeverage,
			traderRecord.TradingSymbols,
			traderRecord.UseCoinPool,
			traderRecord.UseOITop,
			traderRecord.CustomPrompt,
			traderRecord.OverrideBasePrompt,
			traderRecord.IsCrossMargin,
			traderRecord.UseDefaultCoins,
			traderRecord.CustomCoins,
			traderRecord.SystemPromptTemplate,
			encryptedAPIKey,
			traderRecord.AIModelAPIURL,
			traderRecord.IsConfigured,
			tgUserID,
			traderID,
		}
	}

	result, err := d.db.Exec(query, args...)
	if err != nil {
		return fmt.Errorf("更新TG交易员配置失败: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("获取影响行数失败: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("未找到匹配的TG交易员记录")
	}

	return nil
}

// DeleteTgTrader 删除TG交易员
func (d *Database) DeleteTgTrader(tgUserID int64, traderID string) error {
	var query string
	var args []interface{}

	if d.usePostgreSQL {
		query = `DELETE FROM tg_traders WHERE tg_user_id = $1 AND id = $2`
		args = []interface{}{tgUserID, traderID}
	} else {
		query = `DELETE FROM tg_traders WHERE tg_user_id = ? AND id = ?`
		args = []interface{}{tgUserID, traderID}
	}

	result, err := d.db.Exec(query, args...)
	if err != nil {
		return fmt.Errorf("删除TG交易员失败: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("获取影响行数失败: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("未找到匹配的TG交易员记录")
	}

	return nil
}

// GetTgTraderConfig 获取TG交易员配置
func (d *Database) GetTgTraderConfig(tgUserID int64, traderID string) (*TgTraderRecord, error) {
	var query string
	if d.usePostgreSQL {
		query = `
			SELECT id, tg_user_id, name, ai_model_id, COALESCE(ai_model_name, '') AS ai_model_name, exchange_id,
				   initial_balance, scan_interval_minutes, is_running,
				   btc_eth_leverage, altcoin_leverage, trading_symbols,
				   use_coin_pool, use_oi_top, custom_prompt, override_base_prompt,
				   is_cross_margin, use_default_coins, custom_coins,
				   system_prompt_template,
				   COALESCE(ai_model_api_key, '') AS ai_model_api_key,
				   COALESCE(ai_model_api_url, '') AS ai_model_api_url,
				   COALESCE(private_key, '') AS private_key,
				   COALESCE(wallet_address, '') AS wallet_address,
				   created_at, updated_at
			FROM tg_traders
			WHERE tg_user_id = $1 AND id = $2
		`
	} else {
		query = `
			SELECT id, tg_user_id, name, ai_model_id, COALESCE(ai_model_name, '') AS ai_model_name, exchange_id,
				   initial_balance, scan_interval_minutes, is_running,
				   btc_eth_leverage, altcoin_leverage, trading_symbols,
				   use_coin_pool, use_oi_top, custom_prompt, override_base_prompt,
				   is_cross_margin, use_default_coins, custom_coins,
				   system_prompt_template,
				   COALESCE(ai_model_api_key, '') AS ai_model_api_key,
				   COALESCE(ai_model_api_url, '') AS ai_model_api_url,
				   COALESCE(private_key, '') AS private_key,
				   COALESCE(wallet_address, '') AS wallet_address,
				   created_at, updated_at
			FROM tg_traders
			WHERE tg_user_id = ? AND id = ?
		`
	}

	var trader TgTraderRecord
	err := d.db.QueryRow(query, tgUserID, traderID).Scan(
		&trader.ID, &trader.TgUserID, &trader.Name, &trader.AIModelID, &trader.AIModelName,
		&trader.ExchangeID, &trader.InitialBalance, &trader.ScanIntervalMinutes,
		&trader.IsRunning, &trader.BTCETHLeverage, &trader.AltcoinLeverage,
		&trader.TradingSymbols, &trader.UseCoinPool, &trader.UseOITop,
		&trader.CustomPrompt, &trader.OverrideBasePrompt, &trader.IsCrossMargin,
		&trader.UseDefaultCoins, &trader.CustomCoins, &trader.SystemPromptTemplate,
		&trader.AIModelAPIKey, &trader.AIModelAPIURL, &trader.PrivateKey, &trader.WalletAddress, &trader.CreatedAt, &trader.UpdatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("未找到TG交易员记录")
		}
		return nil, fmt.Errorf("获取TG交易员配置失败: %w", err)
	}

	decryptedAPIKey, err := d.decryptSecretValue(trader.AIModelAPIKey)
	if err != nil {
		log.Printf("🚨 CRITICAL: 无法解密AI模型API密钥: %v", err)
		return nil, fmt.Errorf("无法解密AI模型API密钥: %w", err)
	}
	trader.AIModelAPIKey = decryptedAPIKey
	decryptedPrivateKey, err := d.decryptSecretValue(trader.PrivateKey)
	if err != nil {
		log.Printf("🚨 CRITICAL: 无法解密私钥: %v", err)
		return nil, fmt.Errorf("无法解密私钥: %w", err)
	}
	trader.PrivateKey = decryptedPrivateKey
	return &trader, nil
}

// GetTGUsers 获取所有TG用户
func (d *Database) GetTGUsers() ([]string, error) {
	var users []string
	query := "SELECT telegram_id FROM tg_users"

	rows, err := d.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("获取TG用户失败: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var telegramID int64
		if err := rows.Scan(&telegramID); err != nil {
			continue
		}
		users = append(users, fmt.Sprintf("%d", telegramID))
	}

	return users, nil
}
