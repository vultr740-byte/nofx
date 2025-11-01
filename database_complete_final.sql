-- 完整的数据库重建脚本 - 经过全面字段验证
-- 支持用户创建多个AI模型和交易所，自由组合，包含所有必要字段

-- 1. 删除所有现有表（按依赖关系倒序）
DROP TABLE IF EXISTS trader_performance CASCADE;
DROP TABLE IF EXISTS traders CASCADE;
DROP TABLE IF EXISTS ai_models CASCADE;
DROP TABLE IF EXISTS exchanges CASCADE;
DROP TABLE IF EXISTS users CASCADE;
DROP TABLE IF EXISTS system_config CASCADE;

-- 2. 重新创建系统配置表
CREATE TABLE system_config (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    description TEXT DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- 3. 重新创建用户表 - 包含所有认证相关字段
CREATE TABLE users (
    id TEXT PRIMARY KEY,
    email TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,
    otp_secret TEXT NOT NULL,                -- OTP密钥（用于双因素认证）
    otp_verified BOOLEAN DEFAULT FALSE,      -- OTP是否已验证
    admin BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- 4. 重新创建 AI 模型表 - 支持用户创建多个同类型模型
CREATE TABLE ai_models (
    id TEXT PRIMARY KEY,                    -- UUID，每个模型唯一
    user_id TEXT NOT NULL,                  -- 用户ID
    name TEXT NOT NULL,                     -- 用户自定义名称（如"DeepSeek-激进型"）
    provider TEXT NOT NULL,                 -- 模型类型: deepseek, qwen, claude, gpt4 等
    enabled BOOLEAN DEFAULT FALSE,          -- 是否启用
    api_key TEXT DEFAULT '',                 -- API密钥
    description TEXT DEFAULT '',             -- 用户描述
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

-- 5. 重新创建交易所表 - 支持用户创建多个同类型交易所
CREATE TABLE exchanges (
    id TEXT PRIMARY KEY,                    -- UUID，每个交易所唯一
    user_id TEXT NOT NULL,                  -- 用户ID
    name TEXT NOT NULL,                     -- 用户自定义名称（如"Binance-主账户"）
    exchange_type TEXT NOT NULL,            -- 交易所类型: binance, hyperliquid, aster, okx, dydx 等
    enabled BOOLEAN DEFAULT FALSE,          -- 是否启用
    api_key TEXT DEFAULT '',                 -- API密钥
    secret_key TEXT DEFAULT '',              -- API Secret
    testnet BOOLEAN DEFAULT FALSE,           -- 是否测试网
    -- Hyperliquid 特定字段
    hyperliquid_wallet_addr TEXT DEFAULT '',
    -- Aster DEX 特定字段
    aster_user TEXT DEFAULT '',
    aster_signer TEXT DEFAULT '',
    aster_private_key TEXT DEFAULT '',
    description TEXT DEFAULT '',             -- 用户描述
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

-- 6. 重新创建交易员表 - 支持自由组合且保留所有配置字段
CREATE TABLE traders (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    name TEXT NOT NULL,                     -- 交易员名称
    ai_model_id TEXT NOT NULL,              -- 关联的 AI 模型 ID
    exchange_id TEXT NOT NULL,              -- 关联的交易所 ID
    description TEXT DEFAULT '',            -- 交易员描述
    enabled BOOLEAN DEFAULT FALSE,          -- 是否启用

    -- 交易配置字段（保留原有功能）
    initial_balance DECIMAL(20,8) DEFAULT 1000.00000000,  -- 初始资金
    scan_interval_minutes INTEGER DEFAULT 3,              -- 扫描间隔（分钟）
    is_running BOOLEAN DEFAULT FALSE,                     -- 是否正在运行
    custom_prompt TEXT DEFAULT '',                        -- 自定义交易策略prompt
    override_base_prompt BOOLEAN DEFAULT FALSE,           -- 是否覆盖基础prompt
    is_cross_margin BOOLEAN DEFAULT TRUE,                 -- 是否为全仓模式

    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
    FOREIGN KEY (ai_model_id) REFERENCES ai_models(id) ON DELETE CASCADE,
    FOREIGN KEY (exchange_id) REFERENCES exchanges(id) ON DELETE CASCADE
);

-- 7. 重新创建交易员表现表
CREATE TABLE trader_performance (
    id TEXT PRIMARY KEY,
    trader_id TEXT NOT NULL,
    date DATE NOT NULL,
    total_trades INTEGER DEFAULT 0,
    winning_trades INTEGER DEFAULT 0,
    losing_trades INTEGER DEFAULT 0,
    profit_loss DECIMAL(20,8) DEFAULT 0.00000000,
    max_drawdown DECIMAL(20,8) DEFAULT 0.00000000,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    FOREIGN KEY (trader_id) REFERENCES traders(id) ON DELETE CASCADE
);

-- 8. 添加索引以提高查询性能
CREATE INDEX idx_ai_models_user_id ON ai_models(user_id);
CREATE INDEX idx_ai_models_provider ON ai_models(provider);
CREATE INDEX idx_ai_models_enabled ON ai_models(enabled);
CREATE INDEX idx_exchanges_user_id ON exchanges(user_id);
CREATE INDEX idx_exchanges_type ON exchanges(exchange_type);
CREATE INDEX idx_exchanges_enabled ON exchanges(enabled);
CREATE INDEX idx_traders_user_id ON traders(user_id);
CREATE INDEX idx_traders_ai_model_id ON traders(ai_model_id);
CREATE INDEX idx_traders_exchange_id ON traders(exchange_id);
CREATE INDEX idx_traders_enabled ON traders(enabled);
CREATE INDEX idx_trader_performance_trader_id ON trader_performance(trader_id);
CREATE INDEX idx_trader_performance_date ON trader_performance(date);

-- 9. 插入初始系统配置数据
INSERT INTO system_config (key, value, description) VALUES
('model_types', '["deepseek","qwen","claude","gpt4"]', '支持的 AI 模型类型'),
('exchange_types', '["binance","hyperliquid","aster","okx","dydx"]', '支持的交易所类型'),
('max_traders_per_user', '10', '每个用户最多可创建的交易员数量'),
('max_models_per_user', '5', '每个用户最多可创建的 AI 模型数量'),
('max_exchanges_per_user', '5', '每个用户最多可创建的交易所数量');

-- 10. 创建默认管理员用户
INSERT INTO users (id, email, password_hash, otp_secret, otp_verified, admin) VALUES
('admin', 'admin@example.com', '$2a$10$rKZyKyvwXwGeUr68X2E4KOeGYBn4GgY/3uEqXvMzPwBPk4e9JwX.G', 'JBSWY3DPEHPK3PXP', TRUE, TRUE);

-- 完成数据库重建
-- 现在支持：
-- 1. 用户创建多个同类型AI模型（如多个deepseek配置）
-- 2. 用户创建多个同类型交易所（如多个binance账户）
-- 3. 交易员创建时自由组合任意AI模型和交易所
-- 4. 保留所有必要的交易配置字段
-- 5. 支持OTP双因素认证
-- 6. 所有表结构都包含完整的描述字段和admin字段

-- 数据库字段验证脚本 - 确保所有字段匹配
-- 适用于现有的数据库，添加缺失的字段

-- 检查并添加缺失字段（向后兼容）
DO $$
BEGIN
    -- 检查users表是否有admin字段
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'users' AND column_name = 'admin'
    ) THEN
        ALTER TABLE users ADD COLUMN admin BOOLEAN DEFAULT FALSE;
        RAISE NOTICE '已添加users.admin字段';
    END IF;

    -- 检查ai_models表是否有description字段
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'ai_models' AND column_name = 'description'
    ) THEN
        ALTER TABLE ai_models ADD COLUMN description TEXT DEFAULT '';
        RAISE NOTICE '已添加ai_models.description字段';
    END IF;

    -- 检查exchanges表是否有description字段
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'exchanges' AND column_name = 'description'
    ) THEN
        ALTER TABLE exchanges ADD COLUMN description TEXT DEFAULT '';
        RAISE NOTICE '已添加exchanges.description字段';
    END IF;
END $$;