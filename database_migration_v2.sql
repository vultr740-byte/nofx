-- 数据库结构改造 v2 - 支持用户自定义多个模型和交易所
-- 目标：支持用户创建多个同类型的AI模型和交易所

-- 1. 修改 AI 模型表 - 使用单一主键，支持用户创建多个模型
-- 备份现有数据
CREATE TABLE ai_models_backup AS SELECT * FROM ai_models;

-- 删除现有表
DROP TABLE IF EXISTS ai_models CASCADE;

-- 创建新的 AI 模型表
CREATE TABLE ai_models (
    id TEXT PRIMARY KEY,                    -- UUID，每个模型唯一
    user_id TEXT NOT NULL,                    -- 用户ID
    name TEXT NOT NULL,                        -- 用户自定义名称
    provider TEXT NOT NULL,                   -- 模型类型: deepseek, qwen 等
    enabled BOOLEAN DEFAULT FALSE,           -- 是否启用
    api_key TEXT DEFAULT '',                  -- API密钥
    description TEXT DEFAULT '',               -- 用户描述
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

-- 恢复数据
INSERT INTO ai_models (id, user_id, name, provider, enabled, api_key, description, created_at, updated_at)
SELECT
    id,
    user_id,
    CASE
        WHEN user_id = 'default' THEN CONCAT(name, ' (系统)')
        ELSE name
    END as name,
    provider,
    enabled,
    api_key,
    '' as description,
    created_at,
    updated_at
FROM ai_models_backup;

-- 删除备份表
DROP TABLE ai_models_backup;

-- 2. 修改交易所表 - 使用单一主键，支持用户创建多个交易所
-- 备份现有数据
CREATE TABLE exchanges_backup AS SELECT * FROM exchanges;

-- 删除现有表
DROP TABLE IF EXISTS exchanges CASCADE;

-- 创建新的交易所表
CREATE TABLE exchanges (
    id TEXT PRIMARY KEY,                    -- UUID，每个交易所唯一
    user_id TEXT NOT NULL,                    -- 用户ID
    name TEXT NOT NULL,                        -- 用户自定义名称
    exchange_type TEXT NOT NULL,              -- 交易所类型: binance, hyperliquid, aster 等
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

-- 恢复数据
INSERT INTO exchanges (id, user_id, name, exchange_type, enabled, api_key, secret_key, testnet,
                         hyperliquid_wallet_addr, aster_user, aster_signer, aster_private_key,
                         description, created_at, updated_at)
SELECT
    id,
    user_id,
    CASE
        WHEN user_id = 'default' THEN CONCAT(name, ' (系统)')
        ELSE name
    END as name,
    type as exchange_type,
    enabled,
    api_key,
    secret_key,
    testnet,
    hyperliquid_wallet_addr,
    aster_user,
    aster_signer,
    aster_private_key,
    '' as description,
    created_at,
    updated_at
FROM exchanges_backup;

-- 删除备份表
DROP TABLE exchanges_backup;

-- 3. 更新系统配置表 - 添加支持的新功能配置
ALTER TABLE system_config ADD COLUMN IF NOT EXISTS model_types TEXT DEFAULT '["deepseek","qwen"]';
ALTER TABLE system_config ADD COLUMN IF NOT EXISTS exchange_types TEXT DEFAULT '["binance","hyperliquid","aster"]';

-- 4. 添加索引以提高查询性能
CREATE INDEX IF NOT EXISTS idx_ai_models_user_id ON ai_models(user_id);
CREATE INDEX IF NOT EXISTS idx_ai_models_provider ON ai_models(provider);
CREATE INDEX IF NOT EXISTS idx_ai_models_enabled ON ai_models(enabled);
CREATE INDEX IF NOT EXISTS idx_exchanges_user_id ON exchanges(user_id);
CREATE INDEX IF NOT EXISTS idx_exchanges_type ON exchanges(exchange_type);
CREATE INDEX IF NOT EXISTS idx_exchanges_enabled ON exchanges(enabled);

-- 5. 更新现有配置数据
UPDATE system_config SET value = '["deepseek","qwen","claude","gpt4"]' WHERE key = 'model_types';
UPDATE system_config SET value = '["binance","hyperliquid","aster","okx","dydx"]' WHERE key = 'exchange_types';