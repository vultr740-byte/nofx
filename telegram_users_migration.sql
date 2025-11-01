-- 添加 Telegram 用户支持
-- 为现有数据库添加 telegram_users 表以支持 Telegram Bot 用户关联

-- 创建 Telegram 用户表
CREATE TABLE telegram_users (
    id TEXT PRIMARY KEY,                        -- 系统生成的用户ID (UUID)
    telegram_user_id BIGINT UNIQUE NOT NULL,    -- Telegram 用户ID (唯一)
    telegram_username TEXT,                     -- Telegram 用户名
    telegram_first_name TEXT,                   -- Telegram 用户昵称
    telegram_last_name TEXT,                    -- Telegram 用户姓氏
    telegram_chat_id BIGINT,                    -- Telegram 聊天ID
    telegram_language_code TEXT DEFAULT 'zh',  -- 语言代码

    -- 系统关联字段
    system_user_id TEXT,                        -- 关联到 users 表的ID (可选，如果需要web访问)
    is_active BOOLEAN DEFAULT TRUE,             -- 是否活跃
    last_activity_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(), -- 最后活跃时间

    -- 权限和设置
    can_create_traders BOOLEAN DEFAULT TRUE,    -- 是否可以创建交易员
    max_traders INTEGER DEFAULT 5,              -- 最大交易员数量
    timezone TEXT DEFAULT 'UTC',                -- 时区设置

    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- 创建索引以提高查询性能
CREATE INDEX idx_telegram_users_telegram_id ON telegram_users(telegram_user_id);
CREATE INDEX idx_telegram_users_chat_id ON telegram_users(telegram_chat_id);
CREATE INDEX idx_telegram_users_active ON telegram_users(is_active);
CREATE INDEX idx_telegram_users_last_activity ON telegram_users(last_activity_at);

-- 如果系统需要与现有 users 表关联，添加外键约束
-- ALTER TABLE telegram_users ADD CONSTRAINT fk_telegram_users_system_user
-- FOREIGN KEY (system_user_id) REFERENCES users(id) ON DELETE SET NULL;

-- 插入默认的 Telegram 用户配置数据
INSERT INTO system_config (key, value, description) VALUES
('telegram_bot_enabled', 'true', '是否启用Telegram Bot'),
('telegram_max_traders_per_user', '10', 'Telegram用户最大交易员数量'),
('telegram_default_language', 'zh', 'Telegram Bot默认语言'),
('telegram_welcome_message', '欢迎使用 NOFX AI 交易助手！', 'Telegram Bot欢迎消息');

-- 为现有 Telegram 用户创建自动用户记录的函数（可选）
CREATE OR REPLACE FUNCTION ensure_telegram_user_exists(
    p_telegram_user_id BIGINT,
    p_telegram_username TEXT DEFAULT NULL,
    p_telegram_first_name TEXT DEFAULT NULL,
    p_telegram_last_name TEXT DEFAULT NULL,
    p_telegram_chat_id BIGINT DEFAULT NULL
) RETURNS TEXT AS $$
DECLARE
    user_record_id TEXT;
BEGIN
    -- 检查用户是否已存在
    SELECT id INTO user_record_id
    FROM telegram_users
    WHERE telegram_user_id = p_telegram_user_id;

    -- 如果用户不存在，创建新用户
    IF user_record_id IS NULL THEN
        user_record_id := 'tg_user_' || EXTRACT(EPOCH FROM NOW())::TEXT || '_' || (random()*1000000)::TEXT;

        INSERT INTO telegram_users (
            id,
            telegram_user_id,
            telegram_username,
            telegram_first_name,
            telegram_last_name,
            telegram_chat_id,
            last_activity_at
        ) VALUES (
            user_record_id,
            p_telegram_user_id,
            p_telegram_username,
            p_telegram_first_name,
            p_telegram_last_name,
            p_telegram_chat_id,
            NOW()
        );
    ELSE
        -- 更新最后活跃时间和其他信息
        UPDATE telegram_users
        SET
            telegram_username = COALESCE(p_telegram_username, telegram_username),
            telegram_first_name = COALESCE(p_telegram_first_name, telegram_first_name),
            telegram_last_name = COALESCE(p_telegram_last_name, telegram_last_name),
            telegram_chat_id = COALESCE(p_telegram_chat_id, telegram_chat_id),
            last_activity_at = NOW(),
            updated_at = NOW()
        WHERE telegram_user_id = p_telegram_user_id;
    END IF;

    RETURN user_record_id;
END;
$$ LANGUAGE plpgsql;

-- 创建更新用户活跃时间的函数
CREATE OR REPLACE FUNCTION update_telegram_user_activity(
    p_telegram_user_id BIGINT
) RETURNS BOOLEAN AS $$
BEGIN
    UPDATE telegram_users
    SET last_activity_at = NOW(),
        updated_at = NOW()
    WHERE telegram_user_id = p_telegram_user_id;

    RETURN FOUND;
END;
$$ LANGUAGE plpgsql;

-- 注释说明
COMMENT ON TABLE telegram_users IS 'Telegram Bot 用户表，用于关联 Telegram 用户和系统交易员';
COMMENT ON COLUMN telegram_users.id IS '系统内部用户ID，用于关联其他表';
COMMENT ON COLUMN telegram_users.telegram_user_id IS 'Telegram 平台用户ID，唯一标识符';
COMMENT ON COLUMN telegram_users.system_user_id IS '关联到系统web用户表的ID，如果需要跨平台访问';
COMMENT ON COLUMN telegram_users.max_traders IS '该用户允许创建的最大交易员数量';
COMMENT ON FUNCTION ensure_telegram_user_exists IS '确保Telegram用户存在，不存在则自动创建';
COMMENT ON FUNCTION update_telegram_user_activity IS '更新Telegram用户最后活跃时间';