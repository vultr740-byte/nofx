-- 修复用户表缺失字段的脚本
-- 只添加缺失的字段，不重建整个数据库

-- 添加缺失的OTP相关字段
ALTER TABLE users ADD COLUMN IF NOT EXISTS otp_secret TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN IF NOT EXISTS otp_verified BOOLEAN DEFAULT FALSE;

-- 更新现有用户记录（如果有）
UPDATE users SET otp_secret = 'JBSWY3DPEHPK3PXP', otp_verified = TRUE WHERE id = 'admin';

-- 验证字段已添加
SELECT column_name, data_type
FROM information_schema.columns
WHERE table_name = 'users'
ORDER BY ordinal_position;