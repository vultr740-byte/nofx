package config

import (
	"crypto/rand"
	"database/sql"
	"encoding/base32"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

		_ "github.com/lib/pq"  // PostgreSQL 驱动
	// _ "github.com/mattn/go-sqlite3"  // 不再使用 SQLite
)

// Database 配置数据库
type Database struct {
	db *sql.DB
	isPostgreSQL bool
}

// 辅助函数：根据数据库类型选择参数占位符
func (d *Database) getPlaceholder(index int) string {
	if d.isPostgreSQL {
		return fmt.Sprintf("$%d", index)
	}
	return "?"
}

// 辅助函数：转换查询语句中的占位符
func (d *Database) convertQuery(sqliteQuery string) string {
	if !d.isPostgreSQL {
		return sqliteQuery
	}

	// 将 ? 转换为 $1, $2, $3...
	result := sqliteQuery
	placeholderCount := 0

	for strings.Contains(result, "?") {
		placeholderCount++
		oldPlaceholder := "?"
		newPlaceholder := fmt.Sprintf("$%d", placeholderCount)
		result = strings.Replace(result, oldPlaceholder, newPlaceholder, 1)
	}

	return result
}

// NewDatabase 创建配置数据库
func NewDatabase(dbPath string) (*Database, error) {
	var db *sql.DB
	var err error

	// 强制使用 Supabase PostgreSQL
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		return nil, fmt.Errorf("DATABASE_URL 环境变量未设置，必须配置 Supabase 连接字符串")
	}

	if !strings.Contains(dbURL, "supabase") {
		return nil, fmt.Errorf("DATABASE_URL 必须是 Supabase 连接字符串")
	}

	log.Printf("📋 连接到 Supabase PostgreSQL 数据库")

	// 解析并修改连接字符串以添加必要的参数
	if strings.HasPrefix(dbURL, "postgresql://") {
		// 将 postgresql:// 转换为 postgres:// 以便正确解析
		dbURL = strings.Replace(dbURL, "postgresql://", "postgres://", 1)
	}

	// 添加连接参数
	if !strings.Contains(dbURL, "?") {
		dbURL += "?sslmode=require&connect_timeout=30"
	} else {
		dbURL += "&sslmode=require&connect_timeout=30"
	}

	// 隐藏敏感信息的连接字符串日志
	safeURL := dbURL
	if strings.Contains(safeURL, "@") {
		parts := strings.Split(safeURL, "@")
		if len(parts) >= 2 {
			// 隐藏密码部分
			hostPart := strings.Join(parts[1:], "@")
			safeURL = strings.Split(parts[0], ":")[0] + ":***@***" + hostPart
		}
	}
	log.Printf("🔗 使用连接字符串: %s", safeURL)

	db, err = sql.Open("postgres", dbURL)
	if err != nil {
		return nil, fmt.Errorf("连接 Supabase 失败: %w", err)
	}

	// 测试连接
	if err := db.Ping(); err != nil {
		// 提供更详细的错误诊断
		if strings.Contains(err.Error(), "network is unreachable") {
			return nil, fmt.Errorf("Supabase 连接测试失败: 网络不可达。请检查:\n1. DATABASE_URL 是否正确配置\n2. Supabase 项目是否正常运行\n3. 网络连接是否正常\n详细错误: %w", err)
		} else if strings.Contains(err.Error(), "connection refused") {
			return nil, fmt.Errorf("Supabase 连接测试失败: 连接被拒绝。请检查:\n1. Supabase 项目状态\n2. 连接字符串中的端口和主机\n3. 防火墙设置\n详细错误: %w", err)
		} else if strings.Contains(err.Error(), "password authentication failed") {
			return nil, fmt.Errorf("Supabase 连接测试失败: 密码认证失败。请检查 DATABASE_URL 中的密码是否正确\n详细错误: %w", err)
		} else {
			return nil, fmt.Errorf("Supabase 连接测试失败: %w", err)
		}
	}

	log.Printf("✅ Supabase 数据库连接成功")

	database := &Database{
		db:          db,
		isPostgreSQL: true, // 强制使用 PostgreSQL
	}

	// Supabase 数据库不需要创建表（通过 SQL 迁移脚本创建）
	if dbURL != "" && strings.Contains(dbURL, "supabase") {
		log.Printf("✅ Supabase 数据库连接成功，跳过表创建")
	} else {
		if err := database.createTables(); err != nil {
			return nil, fmt.Errorf("创建表失败: %w", err)
		}
	}

	if err := database.initDefaultData(); err != nil {
		return nil, fmt.Errorf("初始化默认数据失败: %w", err)
	}

	return database, nil
}

// createTables 创建数据库表 (仅用于 SQLite)
func (d *Database) createTables() error {
	// 检查是否使用 PostgreSQL
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL != "" && strings.Contains(dbURL, "supabase") {
		// PostgreSQL 使用迁移脚本，不需要在这里创建表
		return nil
	}
	queries := []string{
		// AI模型配置表
		`CREATE TABLE IF NOT EXISTS ai_models (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL DEFAULT 'default',
			name TEXT NOT NULL,
			provider TEXT NOT NULL,
			enabled BOOLEAN DEFAULT 0,
			api_key TEXT DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
		)`,

		// 交易所配置表
		`CREATE TABLE IF NOT EXISTS exchanges (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL DEFAULT 'default',
			name TEXT NOT NULL,
			type TEXT NOT NULL, -- 'cex' or 'dex'
			enabled BOOLEAN DEFAULT 0,
			api_key TEXT DEFAULT '',
			secret_key TEXT DEFAULT '',
			testnet BOOLEAN DEFAULT 0,
			-- Hyperliquid 特定字段
			hyperliquid_wallet_addr TEXT DEFAULT '',
			-- Aster 特定字段
			aster_user TEXT DEFAULT '',
			aster_signer TEXT DEFAULT '',
			aster_private_key TEXT DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
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

		`CREATE TRIGGER IF NOT EXISTS update_system_config_updated_at
			AFTER UPDATE ON system_config
			BEGIN
				UPDATE system_config SET updated_at = CURRENT_TIMESTAMP WHERE key = NEW.key;
			END`,
	}

	for _, query := range queries {
		if _, err := d.db.Exec(query); err != nil {
			return fmt.Errorf("执行SQL失败 [%s]: %w", query, err)
		}
	}

	// 为现有数据库添加新字段（向后兼容）
	alterQueries := []string{
		`ALTER TABLE exchanges ADD COLUMN hyperliquid_wallet_addr TEXT DEFAULT ''`,
		`ALTER TABLE exchanges ADD COLUMN aster_user TEXT DEFAULT ''`,
		`ALTER TABLE exchanges ADD COLUMN aster_signer TEXT DEFAULT ''`,
		`ALTER TABLE exchanges ADD COLUMN aster_private_key TEXT DEFAULT ''`,
		`ALTER TABLE traders ADD COLUMN custom_prompt TEXT DEFAULT ''`,
		`ALTER TABLE traders ADD COLUMN override_base_prompt BOOLEAN DEFAULT 0`,
		`ALTER TABLE traders ADD COLUMN is_cross_margin BOOLEAN DEFAULT 1`, // 默认为全仓模式
	}

	for _, query := range alterQueries {
		// 忽略已存在字段的错误
		d.db.Exec(query)
	}

	// 检查是否需要迁移exchanges表的主键结构
	err := d.migrateExchangesTable()
	if err != nil {
		log.Printf("⚠️ 迁移exchanges表失败: %v", err)
	}

	return nil
}

// initDefaultData 初始化默认数据 (仅 PostgreSQL)
func (d *Database) initDefaultData() error {
	// 不再初始化默认的AI模型和交易所配置
	// 用户需要自己创建模型和交易所配置
	// 只保留系统配置
	systemConfigs := map[string]string{
		"admin_mode":            "true",
		"api_server_port":       "8080",
		"use_default_coins":     "true",
		"default_coins":         `["BTCUSDT","ETHUSDT","SOLUSDT","BNBUSDT","XRPUSDT","DOGEUSDT","ADAUSDT","HYPEUSDT"]`,
		"coin_pool_api_url":     "",
		"oi_top_api_url":        "",
		"max_daily_loss":        "10.0",
		"max_drawdown":          "20.0",
		"stop_trading_minutes":  "60",
		"btc_eth_leverage":      "5",
		"altcoin_leverage":      "3",
		"trading_enabled":       "true",
	}

	// 初始化系统配置
	for key, value := range systemConfigs {
		query := d.convertQuery(`
			INSERT INTO system_config (key, value)
			VALUES (?, ?)
			ON CONFLICT (key) DO NOTHING
		`)
		_, err := d.db.Exec(query, key, value)
		if err != nil {
			return fmt.Errorf("初始化系统配置失败: %w", err)
		}
	}

	return nil
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
	ID          string    `json:"id"`
	Email       string    `json:"email"`
	PasswordHash string   `json:"-"` // 不返回到前端
	OTPSecret   string    `json:"-"` // 不返回到前端
	OTPVerified bool      `json:"otp_verified"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// AIModelConfig AI模型配置
type AIModelConfig struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	Name      string    `json:"name"`
	Provider  string    `json:"provider"`
	Enabled   bool      `json:"enabled"`
	APIKey    string    `json:"apiKey"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ExchangeConfig 交易所配置
type ExchangeConfig struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	Enabled   bool      `json:"enabled"`
	APIKey    string    `json:"apiKey"`
	SecretKey string    `json:"secretKey"`
	Testnet   bool      `json:"testnet"`
	// Hyperliquid 特定字段
	HyperliquidWalletAddr string `json:"hyperliquidWalletAddr"`
	// Aster 特定字段
	AsterUser       string `json:"asterUser"`
	AsterSigner     string `json:"asterSigner"`
	AsterPrivateKey string `json:"asterPrivateKey"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// TraderRecord 交易员配置（数据库实体）
type TraderRecord struct {
	ID                 string    `json:"id"`
	UserID             string    `json:"user_id"`
	Name               string    `json:"name"`
	AIModelID          string    `json:"ai_model_id"`
	ExchangeID         string    `json:"exchange_id"`
	Description        string    `json:"description"`
	Enabled            bool      `json:"enabled"`
	InitialBalance     float64   `json:"initial_balance"`
	ScanIntervalMinutes int      `json:"scan_interval_minutes"`
	IsRunning          bool      `json:"is_running"`
	CustomPrompt       string    `json:"custom_prompt"`       // 自定义交易策略prompt
	OverrideBasePrompt bool      `json:"override_base_prompt"` // 是否覆盖基础prompt
	IsCrossMargin      bool      `json:"is_cross_margin"`      // 是否为全仓模式
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
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
	query := d.convertQuery(`
		INSERT INTO users (id, email, password_hash, otp_secret, otp_verified)
		VALUES (?, ?, ?, ?, ?)
	`)
	_, err := d.db.Exec(query, user.ID, user.Email, user.PasswordHash, user.OTPSecret, user.OTPVerified)
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
	query := d.convertQuery(`
		SELECT id, email, password_hash, otp_secret, otp_verified, created_at, updated_at
		FROM users WHERE email = ?
	`)
	err := d.db.QueryRow(query, email).Scan(
		&user.ID, &user.Email, &user.PasswordHash, &user.OTPSecret,
		&user.OTPVerified, &user.CreatedAt, &user.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &user, nil
}

// GetUserByID 通过ID获取用户
func (d *Database) GetUserByID(userID string) (*User, error) {
	var user User
	query := d.convertQuery(`
		SELECT id, email, password_hash, otp_secret, otp_verified, created_at, updated_at
		FROM users WHERE id = ?
	`)
	err := d.db.QueryRow(query, userID).Scan(
		&user.ID, &user.Email, &user.PasswordHash, &user.OTPSecret,
		&user.OTPVerified, &user.CreatedAt, &user.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &user, nil
}

// UpdateUserOTPVerified 更新用户OTP验证状态
func (d *Database) UpdateUserOTPVerified(userID string, verified bool) error {
	query := d.convertQuery(`UPDATE users SET otp_verified = ? WHERE id = ?`)
	_, err := d.db.Exec(query, verified, userID)
	return err
}

// GetAIModels 获取用户的AI模型配置
func (d *Database) GetAIModels(userID string) ([]*AIModelConfig, error) {
	query := d.convertQuery(`
		SELECT id, user_id, name, provider, enabled, api_key, created_at, updated_at
		FROM ai_models WHERE user_id = ? ORDER BY id
	`)
	rows, err := d.db.Query(query, userID)
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
			&model.Enabled, &model.APIKey,
			&model.CreatedAt, &model.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		models = append(models, &model)
	}

	return models, nil
}

// CreateAIModel 创建用户自定义AI模型
func (d *Database) CreateAIModel(userID, name, provider string, enabled bool, apiKey string) (*AIModelConfig, error) {
	// 生成唯一的模型ID
	modelID := fmt.Sprintf("%s_%d", userID, time.Now().UnixNano())

	query := d.convertQuery(`
		INSERT INTO ai_models (id, user_id, name, provider, enabled, api_key, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, NOW(), NOW())
		RETURNING id, user_id, name, provider, enabled, api_key, created_at, updated_at
	`)

	var model AIModelConfig
	err := d.db.QueryRow(query, modelID, userID, name, provider, enabled, apiKey).Scan(
		&model.ID, &model.UserID, &model.Name, &model.Provider,
		&model.Enabled, &model.APIKey, &model.CreatedAt, &model.UpdatedAt,
	)

	if err != nil {
		return nil, fmt.Errorf("创建AI模型失败: %w", err)
	}

	return &model, nil
}

// UpdateAIModel 更新用户AI模型配置
func (d *Database) UpdateAIModel(userID, id string, name string, enabled bool, apiKey string) error {
	// 检查模型是否属于当前用户
	var exists bool
	query := d.convertQuery(`
		SELECT EXISTS(SELECT 1 FROM ai_models WHERE id = ? AND user_id = ?)
	`)
	err := d.db.QueryRow(query, id, userID).Scan(&exists)
	if err != nil {
		return fmt.Errorf("检查模型所有权失败: %w", err)
	}

	if !exists {
		return fmt.Errorf("模型不存在或不属于当前用户")
	}

	// 更新模型
	query = d.convertQuery(`
		UPDATE ai_models SET name = ?, enabled = ?, api_key = ?, updated_at = NOW()
		WHERE id = ? AND user_id = ?
	`)
	_, err = d.db.Exec(query, name, enabled, apiKey, id, userID)
	return err
}

// DeleteAIModel 删除用户AI模型
func (d *Database) DeleteAIModel(userID, id string) error {
	query := d.convertQuery(`
		DELETE FROM ai_models WHERE id = ? AND user_id = ?
	`)
	result, err := d.db.Exec(query, id, userID)
	if err != nil {
		return fmt.Errorf("删除AI模型失败: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("获取删除结果失败: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("模型不存在或不属于当前用户")
	}

	return nil
}

// GetExchanges 获取用户的交易所配置
func (d *Database) GetExchanges(userID string) ([]*ExchangeConfig, error) {
	query := d.convertQuery(`
		SELECT id, user_id, name, exchange_type, enabled, api_key, secret_key, testnet,
		       COALESCE(hyperliquid_wallet_addr, '') as hyperliquid_wallet_addr,
		       COALESCE(aster_user, '') as aster_user,
		       COALESCE(aster_signer, '') as aster_signer,
		       COALESCE(aster_private_key, '') as aster_private_key,
		       created_at, updated_at
		FROM exchanges WHERE user_id = ? ORDER BY id
	`)
	rows, err := d.db.Query(query, userID)
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
			&exchange.CreatedAt, &exchange.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		exchanges = append(exchanges, &exchange)
	}

	return exchanges, nil
}


// CreateExchange 创建用户自定义交易所配置
func (d *Database) CreateExchange(userID, name, exchangeType string, enabled bool, apiKey, secretKey string, testnet bool, hyperliquidWalletAddr, asterUser, asterSigner, asterPrivateKey string) (*ExchangeConfig, error) {
	// 生成唯一的交易所ID
	exchangeID := fmt.Sprintf("%s_%d", userID, time.Now().UnixNano())

	query := d.convertQuery(`
		INSERT INTO exchanges (id, user_id, name, exchange_type, enabled, api_key, secret_key, testnet,
		                      hyperliquid_wallet_addr, aster_user, aster_signer, aster_private_key, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NOW(), NOW())
		RETURNING id, user_id, name, exchange_type, enabled, api_key, secret_key, testnet,
		           hyperliquid_wallet_addr, aster_user, aster_signer, aster_private_key, created_at, updated_at
	`)

	var exchange ExchangeConfig
	err := d.db.QueryRow(query, exchangeID, userID, name, exchangeType, enabled, apiKey, secretKey, testnet,
		hyperliquidWalletAddr, asterUser, asterSigner, asterPrivateKey).Scan(
		&exchange.ID, &exchange.UserID, &exchange.Name, &exchange.Type, &exchange.Enabled,
		&exchange.APIKey, &exchange.SecretKey, &exchange.Testnet,
		&exchange.HyperliquidWalletAddr, &exchange.AsterUser, &exchange.AsterSigner, &exchange.AsterPrivateKey,
		&exchange.CreatedAt, &exchange.UpdatedAt,
	)

	if err != nil {
		return nil, fmt.Errorf("创建交易所失败: %w", err)
	}

	return &exchange, nil
}

// UpdateExchange 更新用户交易所配置
func (d *Database) UpdateExchange(userID, id string, name string, enabled bool, apiKey, secretKey string, testnet bool, hyperliquidWalletAddr, asterUser, asterSigner, asterPrivateKey string) error {
	// 检查交易所是否属于当前用户
	var exists bool
	query := d.convertQuery(`
		SELECT EXISTS(SELECT 1 FROM exchanges WHERE id = ? AND user_id = ?)
	`)
	err := d.db.QueryRow(query, id, userID).Scan(&exists)
	if err != nil {
		return fmt.Errorf("检查交易所所有权失败: %w", err)
	}

	if !exists {
		return fmt.Errorf("交易所不存在或不属于当前用户")
	}

	// 更新交易所
	query = d.convertQuery(`
		UPDATE exchanges SET name = ?, enabled = ?, api_key = ?, secret_key = ?, testnet = ?,
		                     hyperliquid_wallet_addr = ?, aster_user = ?, aster_signer = ?, aster_private_key = ?, updated_at = NOW()
		WHERE id = ? AND user_id = ?
	`)
	_, err = d.db.Exec(query, name, enabled, apiKey, secretKey, testnet,
		hyperliquidWalletAddr, asterUser, asterSigner, asterPrivateKey, id, userID)
	return err
}

// DeleteExchange 删除用户交易所
func (d *Database) DeleteExchange(userID, id string) error {
	query := d.convertQuery(`
		DELETE FROM exchanges WHERE id = ? AND user_id = ?
	`)
	result, err := d.db.Exec(query, id, userID)
	if err != nil {
		return fmt.Errorf("删除交易所失败: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("获取删除结果失败: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("交易所不存在或不属于当前用户")
	}

	return nil
}


// CreateTrader 创建交易员 - 支持自由组合且保留所有配置
func (d *Database) CreateTrader(trader *TraderRecord) error {
	// 验证 AI 模型是否属于当前用户且已启用
	var modelCount int
	err := d.db.QueryRow(`
		SELECT COUNT(*) FROM ai_models
		WHERE id = $1 AND user_id = $2 AND enabled = TRUE
	`, trader.AIModelID, trader.UserID).Scan(&modelCount)
	if err != nil {
		return fmt.Errorf("验证 AI 模型失败: %w", err)
	}
	if modelCount == 0 {
		return fmt.Errorf("AI 模型不存在或未启用")
	}

	// 验证交易所是否属于当前用户且已启用
	var exchangeCount int
	err = d.db.QueryRow(`
		SELECT COUNT(*) FROM exchanges
		WHERE id = $1 AND user_id = $2 AND enabled = TRUE
	`, trader.ExchangeID, trader.UserID).Scan(&exchangeCount)
	if err != nil {
		return fmt.Errorf("验证交易所失败: %w", err)
	}
	if exchangeCount == 0 {
		return fmt.Errorf("交易所不存在或未启用")
	}

	// 检查用户是否已达到最大交易员数量限制
	var traderCount int
	err = d.db.QueryRow(`
		SELECT COUNT(*) FROM traders WHERE user_id = $1
	`, trader.UserID).Scan(&traderCount)
	if err != nil {
		return fmt.Errorf("检查交易员数量失败: %w", err)
	}

	// 获取最大交易员数量限制
	var maxTradersStr string
	err = d.db.QueryRow(`
		SELECT value FROM system_config WHERE key = 'max_traders_per_user'
	`).Scan(&maxTradersStr)
	if err != nil {
		maxTradersStr = "10" // 默认值
	}

	maxTraders := 10
	if val, err := strconv.Atoi(maxTradersStr); err == nil {
		maxTraders = val
	}

	if traderCount >= maxTraders {
		return fmt.Errorf("已达到最大交易员数量限制 (%d)", maxTraders)
	}

	// 创建交易员，包含所有配置字段
	query := `
		INSERT INTO traders (id, user_id, name, ai_model_id, exchange_id, description, enabled,
		                   initial_balance, scan_interval_minutes, is_running, custom_prompt,
		                   override_base_prompt, is_cross_margin, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
	`
	_, err = d.db.Exec(query, trader.ID, trader.UserID, trader.Name, trader.AIModelID, trader.ExchangeID,
		trader.Description, trader.Enabled, trader.InitialBalance, trader.ScanIntervalMinutes,
		trader.IsRunning, trader.CustomPrompt, trader.OverrideBasePrompt, trader.IsCrossMargin,
		trader.CreatedAt, trader.UpdatedAt)
	return err
}


// GetTraders 获取用户的交易员
func (d *Database) GetTraders(userID string) ([]*TraderRecord, error) {
	query := `
		SELECT id, user_id, name, ai_model_id, exchange_id,
		       COALESCE(description, '') as description, enabled,
		       COALESCE(initial_balance, 1000.0) as initial_balance,
		       COALESCE(scan_interval_minutes, 3) as scan_interval_minutes,
		       COALESCE(is_running, FALSE) as is_running,
		       COALESCE(custom_prompt, '') as custom_prompt,
		       COALESCE(override_base_prompt, FALSE) as override_base_prompt,
		       COALESCE(is_cross_margin, TRUE) as is_cross_margin,
		       created_at, updated_at
		FROM traders
		WHERE user_id = $1
		ORDER BY created_at DESC
	`
	rows, err := d.db.Query(query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var traders []*TraderRecord
	for rows.Next() {
		var trader TraderRecord
		err := rows.Scan(&trader.ID, &trader.UserID, &trader.Name, &trader.AIModelID, &trader.ExchangeID,
			&trader.Description, &trader.Enabled, &trader.InitialBalance, &trader.ScanIntervalMinutes,
			&trader.IsRunning, &trader.CustomPrompt, &trader.OverrideBasePrompt, &trader.IsCrossMargin,
			&trader.CreatedAt, &trader.UpdatedAt)
		if err != nil {
			return nil, err
		}
		traders = append(traders, &trader)
	}

	return traders, nil
}

// UpdateTraderStatus 更新交易员状态
func (d *Database) UpdateTraderStatus(userID, id string, isRunning bool) error {
	query := d.convertQuery(`UPDATE traders SET is_running = ? WHERE id = ? AND user_id = ?`)
	_, err := d.db.Exec(query, isRunning, id, userID)
	return err
}

// UpdateTraderCustomPrompt 更新交易员自定义Prompt
func (d *Database) UpdateTraderCustomPrompt(userID, id string, customPrompt string, overrideBase bool) error {
	query := d.convertQuery(`UPDATE traders SET custom_prompt = ?, override_base_prompt = ? WHERE id = ? AND user_id = ?`)
	_, err := d.db.Exec(query, customPrompt, overrideBase, id, userID)
	return err
}

// DeleteTrader 删除交易员
func (d *Database) DeleteTrader(userID, id string) error {
	query := d.convertQuery(`DELETE FROM traders WHERE id = ? AND user_id = ?`)
	_, err := d.db.Exec(query, id, userID)
	return err
}

// GetTraderConfig 获取交易员完整配置（包含AI模型和交易所信息）
func (d *Database) GetTraderConfig(userID, traderID string) (*TraderRecord, *AIModelConfig, *ExchangeConfig, error) {
	var trader TraderRecord
	var aiModel AIModelConfig
	var exchange ExchangeConfig

	query := `
		SELECT
			t.id, t.user_id, t.name, t.ai_model_id, t.exchange_id, t.description, t.enabled, t.created_at, t.updated_at,
			a.id, a.user_id, a.name, a.provider, a.enabled, a.api_key, a.created_at, a.updated_at,
			e.id, e.user_id, e.name, e.exchange_type, e.enabled, e.api_key, e.secret_key, e.testnet,
			COALESCE(e.hyperliquid_wallet_addr, '') as hyperliquid_wallet_addr,
			COALESCE(e.aster_user, '') as aster_user,
			COALESCE(e.aster_signer, '') as aster_signer,
			COALESCE(e.aster_private_key, '') as aster_private_key,
			e.created_at, e.updated_at
		FROM traders t
		JOIN ai_models a ON t.ai_model_id = a.id AND t.user_id = a.user_id
		JOIN exchanges e ON t.exchange_id = e.id AND t.user_id = e.user_id
		WHERE t.id = $1 AND t.user_id = $2
	`

	err := d.db.QueryRow(query, traderID, userID).Scan(
		&trader.ID, &trader.UserID, &trader.Name, &trader.AIModelID, &trader.ExchangeID,
		&trader.Description, &trader.Enabled, &trader.CreatedAt, &trader.UpdatedAt,
		&aiModel.ID, &aiModel.UserID, &aiModel.Name, &aiModel.Provider, &aiModel.Enabled, &aiModel.APIKey,
		&aiModel.CreatedAt, &aiModel.UpdatedAt,
		&exchange.ID, &exchange.UserID, &exchange.Name, &exchange.Type, &exchange.Enabled,
		&exchange.APIKey, &exchange.SecretKey, &exchange.Testnet,
		&exchange.HyperliquidWalletAddr, &exchange.AsterUser, &exchange.AsterSigner, &exchange.AsterPrivateKey,
		&exchange.CreatedAt, &exchange.UpdatedAt,
	)

	if err != nil {
		return nil, nil, nil, err
	}

	return &trader, &aiModel, &exchange, nil
}

// GetSystemConfig 获取系统配置
func (d *Database) GetSystemConfig(key string) (string, error) {
	var value string
	query := d.convertQuery(`SELECT value FROM system_config WHERE key = ?`)
	err := d.db.QueryRow(query, key).Scan(&value)
	return value, err
}

// SetSystemConfig 设置系统配置
func (d *Database) SetSystemConfig(key, value string) error {
	query := d.convertQuery(`
		INSERT INTO system_config (key, value) VALUES (?, ?)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value
	`)
	_, err := d.db.Exec(query, key, value)
	return err
}

// Close 关闭数据库连接
func (d *Database) Close() error {
	return d.db.Close()
}