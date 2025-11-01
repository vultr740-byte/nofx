-- 完全重新创建数据库表结构 - 支持用户自定义多个模型和交易所
-- 执行前请备份重要数据

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

-- 3. 重新创建用户表
CREATE TABLE users (
    id TEXT PRIMARY KEY,
    username TEXT UNIQUE NOT NULL,
    email TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,
    admin BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- 4. 重新创建 AI 模型表 - 支持用户创建多个同类型模型
CREATE TABLE ai_models (
    id TEXT PRIMARY KEY,                    -- UUID，每个模型唯一
    user_id TEXT NOT NULL,                    -- 用户ID
    name TEXT NOT NULL,                        -- 用户自定义名称
    provider TEXT NOT NULL,                   -- 模型类型: deepseek, qwen, claude, gpt4 等
    enabled BOOLEAN DEFAULT FALSE,           -- 是否启用
    api_key TEXT DEFAULT '',                  -- API密钥
    description TEXT DEFAULT '',               -- 用户描述
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

-- 5. 重新创建交易所表 - 支持用户创建多个同类型交易所
CREATE TABLE exchanges (
    id TEXT PRIMARY KEY,                    -- UUID，每个交易所唯一
    user_id TEXT NOT NULL,                    -- 用户ID
    name TEXT NOT NULL,                        -- 用户自定义名称
    exchange_type TEXT NOT NULL,              -- 交易所类型: binance, hyperliquid, aster, okx, dydx 等
    enabled BOOLEAN DEFAULT FALSE,           -- 是否启用
    api_key TEXT DEFAULT '',                  -- API密钥
    secret_key TEXT DEFAULT '',                -- API Secret
    testnet BOOLEAN DEFAULT FALSE,            -- 是否测试网
    -- Hyperliquid 特定字段
    hyperliquid_wallet_addr TEXT DEFAULT '',
    -- Aster DEX 特定字段
    aster_user TEXT DEFAULT '',
    aster_signer TEXT DEFAULT '',
    aster_private_key TEXT DEFAULT '',
    description TEXT DEFAULT '',               -- 用户描述
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

-- 6. 重新创建交易员表 - 支持自由组合 AI 模型和交易所
CREATE TABLE traders (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    name TEXT NOT NULL,                       -- 交易员名称
    ai_model_id TEXT NOT NULL,                -- 关联的 AI 模型 ID
    exchange_id TEXT NOT NULL,                -- 关联的交易所 ID
    description TEXT DEFAULT '',               -- 交易员描述
    enabled BOOLEAN DEFAULT FALSE,            -- 是否启用
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
INSERT INTO users (id, username, email, password_hash, admin) VALUES
('admin', 'admin', 'admin@example.com', '$2a$10$rKZyKyvwXwGeUr68X2E4KOeGYBn4GgY/3uEqXvMzPwBPk4e9JwX.G', TRUE);

-- 完成数据库重建