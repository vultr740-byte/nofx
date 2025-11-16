package config

import (
	"log"
	"os"
)

// TelegramBotConfig Telegram Bot 配置（用于交互式 Bot）
type TelegramBotConfig struct {
	BotToken           string `json:"bot_token"`
	Debug              bool   `json:"debug"`
	Enabled            bool   `json:"enabled"`
	HyperliquidTestnet bool   `json:"hyperliquid_testnet"`
}

// LoadTelegramBotConfig 加载 Telegram Bot 配置
func LoadTelegramBotConfig() *TelegramBotConfig {
	config := &TelegramBotConfig{
		BotToken:           getEnvOrDefault("TELEGRAM_BOT_TOKEN", ""),
		Debug:              getEnvOrDefault("TELEGRAM_DEBUG", "false") == "true",
		Enabled:            getEnvOrDefault("TELEGRAM_ENABLED", "true") == "true",
		HyperliquidTestnet: getEnvOrDefault("HYPERLIQUID_TESTNET", "false") == "true",
	}

	if config.Enabled && config.BotToken == "" {
		log.Printf("⚠️ Telegram Bot 已启用但未配置 Bot Token")
		config.Enabled = false
	}

	if config.Enabled {
		network := "主网"
		if config.HyperliquidTestnet {
			network = "测试网"
		}
		log.Printf("✅ Telegram Bot 配置已加载 (Debug: %v, Hyperliquid: %s)", config.Debug, network)
	}

	return config
}

// getEnvOrDefault 获取环境变量或返回默认值
func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
