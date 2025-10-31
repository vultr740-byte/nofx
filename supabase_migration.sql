-- Supabase 数据库迁移脚本 (修复外键约束问题)
-- 将 SQLite 表结构转换为 PostgreSQL

-- 1. 用户表
CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    email TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,
    otp_secret TEXT,
    otp_verified BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- 2. AI模型配置表 - 使用复合主键支持多用户
CREATE TABLE IF NOT EXISTS ai_models (
    id TEXT NOT NULL,
    user_id TEXT NOT NULL DEFAULT 'default',
    name TEXT NOT NULL,
    provider TEXT NOT NULL,
    enabled BOOLEAN DEFAULT FALSE,
    api_key TEXT DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    PRIMARY KEY (id, user_id),
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

-- 3. 交易所配置表 - 使用复合主键支持多用户
CREATE TABLE IF NOT EXISTS exchanges (
    id TEXT NOT NULL,
    user_id TEXT NOT NULL DEFAULT 'default',
    name TEXT NOT NULL,
    type TEXT NOT NULL, -- 'cex' or 'dex'
    enabled BOOLEAN DEFAULT FALSE,
    api_key TEXT DEFAULT '',
    secret_key TEXT DEFAULT '',
    testnet BOOLEAN DEFAULT FALSE,
    -- Hyperliquid 特定字段
    hyperliquid_wallet_addr TEXT DEFAULT '',
    -- Aster 特定字段
    aster_user TEXT DEFAULT '',
    aster_signer TEXT DEFAULT '',
    aster_private_key TEXT DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    PRIMARY KEY (id, user_id),
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

-- 4. 交易员配置表
CREATE TABLE IF NOT EXISTS traders (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL DEFAULT 'default',
    name TEXT NOT NULL,
    ai_model_id TEXT NOT NULL,
    exchange_id TEXT NOT NULL,
    initial_balance DECIMAL(20,8) NOT NULL,
    scan_interval_minutes INTEGER DEFAULT 3,
    is_running BOOLEAN DEFAULT FALSE,
    custom_prompt TEXT DEFAULT '',
    override_base_prompt BOOLEAN DEFAULT FALSE,
    is_cross_margin BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
    -- 注意：ai_model_id 和 exchange_id 的外键关系在应用层面处理
    -- 因为它们引用的是复合主键表
);

-- 5. 系统配置表
CREATE TABLE IF NOT EXISTS system_config (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- 创建索引以提高查询性能
CREATE INDEX IF NOT EXISTS idx_users_email ON users(email);
CREATE INDEX IF NOT EXISTS idx_ai_models_user_id ON ai_models(user_id);
CREATE INDEX IF NOT EXISTS idx_exchanges_user_id ON exchanges(user_id);
CREATE INDEX IF NOT EXISTS idx_traders_user_id ON traders(user_id);
CREATE INDEX IF NOT EXISTS idx_traders_ai_model_id ON traders(ai_model_id);
CREATE INDEX IF NOT EXISTS idx_traders_exchange_id ON traders(exchange_id);

-- 创建自动更新 updated_at 的函数
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ language 'plpgsql';

-- 为每个表创建触发器
CREATE TRIGGER update_users_updated_at BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_ai_models_updated_at BEFORE UPDATE ON ai_models
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_exchanges_updated_at BEFORE UPDATE ON exchanges
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_traders_updated_at BEFORE UPDATE ON traders
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_system_config_updated_at BEFORE UPDATE ON system_config
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- 初始化默认数据
-- 首先插入默认用户
INSERT INTO users (id, email, password_hash, otp_verified) VALUES
    ('default', 'default@localhost', '', TRUE)
ON CONFLICT (id) DO NOTHING;

-- 插入默认 AI 模型
INSERT INTO ai_models (id, user_id, name, provider, enabled) VALUES
    ('deepseek', 'default', 'DeepSeek', 'deepseek', FALSE),
    ('qwen', 'default', 'Qwen', 'qwen', FALSE)
ON CONFLICT (id, user_id) DO NOTHING;

-- 插入默认交易所
INSERT INTO exchanges (id, user_id, name, type, enabled) VALUES
    ('binance', 'default', 'Binance Futures', 'binance', FALSE),
    ('hyperliquid', 'default', 'Hyperliquid', 'hyperliquid', FALSE),
    ('aster', 'default', 'Aster DEX', 'aster', FALSE)
ON CONFLICT (id, user_id) DO NOTHING;

-- 插入系统配置
INSERT INTO system_config (key, value) VALUES
    ('admin_mode', 'true'),
    ('api_server_port', '8080'),
    ('use_default_coins', 'true'),
    ('default_coins', '["BTCUSDT","ETHUSDT","SOLUSDT","BNBUSDT","XRPUSDT","DOGEUSDT","ADAUSDT","HYPEUSDT"]'),
    ('coin_pool_api_url', ''),
    ('oi_top_api_url', ''),
    ('max_daily_loss', '10.0'),
    ('max_drawdown', '20.0'),
    ('stop_trading_minutes', '60'),
    ('btc_eth_leverage', '5'),
    ('altcoin_leverage', '5'),
    ('jwt_secret', '')
ON CONFLICT (key) DO NOTHING;

-- 创建检查约束确保数据完整性
ALTER TABLE ai_models ADD CONSTRAINT check_ai_models_user_id
    CHECK (user_id IS NOT NULL);

ALTER TABLE exchanges ADD CONSTRAINT check_exchanges_user_id
    CHECK (user_id IS NOT NULL);

ALTER TABLE traders ADD CONSTRAINT check_traders_user_id
    CHECK (user_id IS NOT NULL);

-- 注释说明
COMMENT ON TABLE users IS '用户表';
COMMENT ON TABLE ai_models IS 'AI模型配置表，支持多用户';
COMMENT ON TABLE exchanges IS '交易所配置表，支持多用户';
COMMENT ON TABLE traders IS '交易员配置表';
COMMENT ON TABLE system_config IS '系统配置表';

COMMENT ON COLUMN ai_models.id IS '模型标识符';
COMMENT ON COLUMN ai_models.user_id IS '用户标识符，复合主键的一部分';
COMMENT ON COLUMN exchanges.id IS '交易所标识符';
COMMENT ON COLUMN exchanges.user_id IS '用户标识符，复合主键的一部分';
COMMENT ON COLUMN traders.ai_model_id IS 'AI模型标识符，引用 ai_models(id, user_id)';
COMMENT ON COLUMN traders.exchange_id IS '交易所标识符，引用 exchanges(id, user_id)';