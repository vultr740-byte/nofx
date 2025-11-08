package crypto

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"time"

	_ "modernc.org/sqlite"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// SecureStorage 安全存儲層（自動加密/解密數據庫中的敏感字段）
type SecureStorage struct {
	db *sql.DB
	em *EncryptionManager
}

// NewSecureStorage 創建安全存儲實例
func NewSecureStorage(db *sql.DB) (*SecureStorage, error) {
	em, err := GetEncryptionManager()
	if err != nil {
		return nil, err
	}

	ss := &SecureStorage{
		db: db,
		em: em,
	}

	// 初始化審計日誌表
	if err := ss.initAuditLog(); err != nil {
		return nil, fmt.Errorf("初始化審計日誌失敗: %w", err)
	}

	return ss, nil
}

// ==================== 交易所配置加密存儲 ====================

// SaveEncryptedExchangeConfig 保存加密的交易所配置
func (ss *SecureStorage) SaveEncryptedExchangeConfig(userID, exchangeID, apiKey, secretKey, asterPrivateKey string) error {
	// 加密敏感字段
	encryptedAPIKey, err := ss.em.EncryptForDatabase(apiKey)
	if err != nil {
		return fmt.Errorf("加密 API Key 失敗: %w", err)
	}

	encryptedSecretKey, err := ss.em.EncryptForDatabase(secretKey)
	if err != nil {
		return fmt.Errorf("加密 Secret Key 失敗: %w", err)
	}

	encryptedPrivateKey := ""
	if asterPrivateKey != "" {
		encryptedPrivateKey, err = ss.em.EncryptForDatabase(asterPrivateKey)
		if err != nil {
			return fmt.Errorf("加密 Private Key 失敗: %w", err)
		}
	}

	// 更新數據庫
	_, err = ss.db.Exec(`
		UPDATE exchanges
		SET api_key = ?, secret_key = ?, aster_private_key = ?, updated_at = datetime('now')
		WHERE user_id = ? AND id = ?
	`, encryptedAPIKey, encryptedSecretKey, encryptedPrivateKey, userID, exchangeID)

	if err != nil {
		return err
	}

	// 記錄審計日誌
	ss.logAudit(userID, "exchange_config_update", exchangeID, "密鑰已更新")

	log.Printf("🔐 [%s] 交易所 %s 的密鑰已加密保存", userID, exchangeID)
	return nil
}

// LoadDecryptedExchangeConfig 加載並解密交易所配置
func (ss *SecureStorage) LoadDecryptedExchangeConfig(userID, exchangeID string) (apiKey, secretKey, asterPrivateKey string, err error) {
	var encryptedAPIKey, encryptedSecretKey, encryptedPrivateKey sql.NullString

	err = ss.db.QueryRow(`
		SELECT api_key, secret_key, aster_private_key
		FROM exchanges
		WHERE user_id = ? AND id = ?
	`, userID, exchangeID).Scan(&encryptedAPIKey, &encryptedSecretKey, &encryptedPrivateKey)

	if err != nil {
		return "", "", "", err
	}

	// 解密 API Key
	if encryptedAPIKey.Valid && encryptedAPIKey.String != "" {
		apiKey, err = ss.em.DecryptFromDatabase(encryptedAPIKey.String)
		if err != nil {
			return "", "", "", fmt.Errorf("解密 API Key 失敗: %w", err)
		}
	}

	// 解密 Secret Key
	if encryptedSecretKey.Valid && encryptedSecretKey.String != "" {
		secretKey, err = ss.em.DecryptFromDatabase(encryptedSecretKey.String)
		if err != nil {
			return "", "", "", fmt.Errorf("解密 Secret Key 失敗: %w", err)
		}
	}

	// 解密 Private Key
	if encryptedPrivateKey.Valid && encryptedPrivateKey.String != "" {
		asterPrivateKey, err = ss.em.DecryptFromDatabase(encryptedPrivateKey.String)
		if err != nil {
			return "", "", "", fmt.Errorf("解密 Private Key 失敗: %w", err)
		}
	}

	// 記錄審計日誌
	ss.logAudit(userID, "exchange_config_read", exchangeID, "密鑰已讀取")

	return apiKey, secretKey, asterPrivateKey, nil
}

// ==================== AI 模型配置加密存儲 ====================

// SaveEncryptedAIModelConfig 保存加密的 AI 模型 API Key
func (ss *SecureStorage) SaveEncryptedAIModelConfig(userID, modelID, apiKey string) error {
	encryptedAPIKey, err := ss.em.EncryptForDatabase(apiKey)
	if err != nil {
		return fmt.Errorf("加密 API Key 失敗: %w", err)
	}

	_, err = ss.db.Exec(`
		UPDATE ai_models
		SET api_key = ?, updated_at = datetime('now')
		WHERE user_id = ? AND id = ?
	`, encryptedAPIKey, userID, modelID)

	if err != nil {
		return err
	}

	ss.logAudit(userID, "ai_model_config_update", modelID, "API Key 已更新")
	log.Printf("🔐 [%s] AI 模型 %s 的 API Key 已加密保存", userID, modelID)
	return nil
}

// LoadDecryptedAIModelConfig 加載並解密 AI 模型配置
func (ss *SecureStorage) LoadDecryptedAIModelConfig(userID, modelID string) (string, error) {
	var encryptedAPIKey sql.NullString

	err := ss.db.QueryRow(`
		SELECT api_key FROM ai_models WHERE user_id = ? AND id = ?
	`, userID, modelID).Scan(&encryptedAPIKey)

	if err != nil {
		return "", err
	}

	if !encryptedAPIKey.Valid || encryptedAPIKey.String == "" {
		return "", nil
	}

	apiKey, err := ss.em.DecryptFromDatabase(encryptedAPIKey.String)
	if err != nil {
		return "", fmt.Errorf("解密 API Key 失敗: %w", err)
	}

	ss.logAudit(userID, "ai_model_config_read", modelID, "API Key 已讀取")
	return apiKey, nil
}

// ==================== 審計日誌 ====================

// initAuditLog 初始化審計日誌表
func (ss *SecureStorage) initAuditLog() error {
	// 检查是否使用 PostgreSQL
	if ss.usePostgreSQL() {
		_, err := ss.db.Exec(`
			CREATE TABLE IF NOT EXISTS audit_logs (
				id TEXT PRIMARY KEY DEFAULT uuid_generate_v4(),
				user_id TEXT NOT NULL,
				action TEXT NOT NULL,
				resource TEXT NOT NULL,
				details TEXT,
				ip_address TEXT,
				user_agent TEXT,
				timestamp TIMESTAMPTZ DEFAULT NOW()
			)
		`)
		if err != nil {
			return err
		}

		// 创建索引
		indexQueries := []string{
			`CREATE INDEX IF NOT EXISTS idx_audit_logs_user_time ON audit_logs(user_id, timestamp)`,
			`CREATE INDEX IF NOT EXISTS idx_audit_logs_action ON audit_logs(action)`,
		}
		for _, query := range indexQueries {
			if _, err := ss.db.Exec(query); err != nil {
				return err
			}
		}
		return nil
	}

	// SQLite 版本
	_, err := ss.db.Exec(`
		CREATE TABLE IF NOT EXISTS audit_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id TEXT NOT NULL,
			action TEXT NOT NULL,
			resource TEXT NOT NULL,
			details TEXT,
			ip_address TEXT,
			user_agent TEXT,
			timestamp DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return err
	}

	// 创建索引
	indexQueries := []string{
		`CREATE INDEX IF NOT EXISTS idx_user_time ON audit_logs(user_id, timestamp)`,
		`CREATE INDEX IF NOT EXISTS idx_action ON audit_logs(action)`,
	}
	for _, query := range indexQueries {
		if _, err := ss.db.Exec(query); err != nil {
			return err
		}
	}

	return nil
}

// usePostgreSQL 检查是否使用 PostgreSQL
func (ss *SecureStorage) usePostgreSQL() bool {
	// 通过检查环境变量判断
	return os.Getenv("SUPABASE_URL") != ""
}

// logAudit 記錄審計日誌
func (ss *SecureStorage) logAudit(userID, action, resource, details string) {
	var err error
	if ss.usePostgreSQL() {
		_, err = ss.db.Exec(`
			INSERT INTO audit_logs (user_id, action, resource, details)
			VALUES ($1, $2, $3, $4)
		`, userID, action, resource, details)
	} else {
		_, err = ss.db.Exec(`
			INSERT INTO audit_logs (user_id, action, resource, details)
			VALUES (?, ?, ?, ?)
		`, userID, action, resource, details)
	}

	if err != nil {
		log.Printf("⚠️ 審計日誌記錄失敗: %v", err)
	}
}

// GetAuditLogs 查詢審計日誌
func (ss *SecureStorage) GetAuditLogs(userID string, limit int) ([]AuditLog, error) {
	rows, err := ss.db.Query(`
		SELECT id, user_id, action, resource, details, timestamp
		FROM audit_logs
		WHERE user_id = ?
		ORDER BY timestamp DESC
		LIMIT ?
	`, userID, limit)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []AuditLog
	for rows.Next() {
		var log AuditLog
		err := rows.Scan(&log.ID, &log.UserID, &log.Action, &log.Resource, &log.Details, &log.Timestamp)
		if err != nil {
			return nil, err
		}
		logs = append(logs, log)
	}

	return logs, nil
}

// AuditLog 審計日誌結構
type AuditLog struct {
	ID        int64     `json:"id"`
	UserID    string    `json:"user_id"`
	Action    string    `json:"action"`
	Resource  string    `json:"resource"`
	Details   string    `json:"details"`
	Timestamp time.Time `json:"timestamp"`
}

// ==================== 數據遷移工具 ====================

// MigrateToEncrypted 將舊的明文數據遷移到加密格式
func (ss *SecureStorage) MigrateToEncrypted() error {
	log.Println("🔄 開始遷移明文數據到加密格式...")

	tx, err := ss.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 遷移交易所配置
	rows, err := tx.Query(`
		SELECT user_id, id, api_key, secret_key, aster_private_key
		FROM exchanges
		WHERE api_key != '' AND api_key NOT LIKE '%==%' -- 過濾已加密數據
	`)
	if err != nil {
		return err
	}

	var count int
	for rows.Next() {
		var userID, exchangeID, apiKey, secretKey string
		var asterPrivateKey sql.NullString
		if err := rows.Scan(&userID, &exchangeID, &apiKey, &secretKey, &asterPrivateKey); err != nil {
			rows.Close()
			return err
		}

		// 加密
		encAPIKey, _ := ss.em.EncryptForDatabase(apiKey)
		encSecretKey, _ := ss.em.EncryptForDatabase(secretKey)
		encPrivateKey := ""
		if asterPrivateKey.Valid && asterPrivateKey.String != "" {
			encPrivateKey, _ = ss.em.EncryptForDatabase(asterPrivateKey.String)
		}

		// 更新
		_, err = tx.Exec(`
			UPDATE exchanges
			SET api_key = ?, secret_key = ?, aster_private_key = ?
			WHERE user_id = ? AND id = ?
		`, encAPIKey, encSecretKey, encPrivateKey, userID, exchangeID)

		if err != nil {
			rows.Close()
			return err
		}

		count++
	}
	rows.Close()

	if err := tx.Commit(); err != nil {
		return err
	}

	log.Printf("✅ 已遷移 %d 個交易所配置到加密格式", count)
	return nil
}
