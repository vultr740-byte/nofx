package config

import (
	"log"
	"os"
	"strconv"
	"strings"
)

// TelegramBotConfig Telegram Bot 配置（用于交互式 Bot）
type TelegramBotConfig struct {
	BotToken           string  `json:"bot_token"`
	Debug              bool    `json:"debug"`
	Enabled            bool    `json:"enabled"`
	HyperliquidTestnet bool    `json:"hyperliquid_testnet"`
	ReferralCode       string  `json:"referral_code"`
	GasPayerPrivateKey string  `json:"gas_payer_private_key"`
	GasSponsorshipETH  float64 `json:"gas_sponsorship_eth"`
	ArbitrumRPCURL     string  `json:"arbitrum_rpc_url"`
	ArbitrumChainID    int64   `json:"arbitrum_chain_id"`
	ArbitrumUSDC       string  `json:"arbitrum_usdc"`

	OneClickBaseURL          string `json:"oneclick_base_url"`
	OneClickJWT              string `json:"oneclick_jwt"`
	OneClickSlippageBps      int    `json:"oneclick_slippage_bps"`
	OneClickDeadlineMinutes  int    `json:"oneclick_deadline_minutes"`
	OneClickQuoteWaitTimeMs  int    `json:"oneclick_quote_wait_time_ms"`
	OneClickUseFlexInputSwap bool   `json:"oneclick_use_flex_input_swap"`
}

// LoadTelegramBotConfig 加载 Telegram Bot 配置
func LoadTelegramBotConfig() *TelegramBotConfig {
	referralRaw := getEnvOrDefault("HYPERLIQUID_REFERRAL_CODE", "")
	if referralRaw == "" {
		referralRaw = getEnvOrDefault("HYPERLIQUID_REFERRAL_URL", "")
	}
	if referralRaw == "" {
		referralRaw = "KEYCODE"
	}

	config := &TelegramBotConfig{
		BotToken:           getEnvOrDefault("TELEGRAM_BOT_TOKEN", ""),
		Debug:              getEnvOrDefault("TELEGRAM_DEBUG", "false") == "true",
		Enabled:            getEnvOrDefault("TELEGRAM_ENABLED", "true") == "true",
		HyperliquidTestnet: getEnvOrDefault("HYPERLIQUID_TESTNET", "false") == "true",
		ReferralCode:       parseReferralCode(referralRaw),
		GasPayerPrivateKey: getEnvOrDefault("GAS_PAYER_PRIVATE_KEY", ""),
		GasSponsorshipETH:  getEnvOrDefaultFloat("GAS_SPONSORSHIP_ETH", 0),
		ArbitrumRPCURL:     getEnvOrDefault("ARBITRUM_RPC_URL", ""),
		ArbitrumChainID:    getEnvOrDefaultInt64("ARBITRUM_CHAIN_ID", 42161),
		ArbitrumUSDC:       getEnvOrDefault("ARBITRUM_USDC", ""),

		OneClickBaseURL:          getEnvOrDefault("ONECLICK_BASE_URL", "https://1click.chaindefuser.com"),
		OneClickJWT:              getEnvOrDefault("ONECLICK_JWT", ""),
		OneClickSlippageBps:      int(getEnvOrDefaultInt64("ONECLICK_SLIPPAGE_BPS", 100)),
		OneClickDeadlineMinutes:  int(getEnvOrDefaultInt64("ONECLICK_DEADLINE_MINUTES", 24*60)),
		OneClickQuoteWaitTimeMs:  int(getEnvOrDefaultInt64("ONECLICK_QUOTE_WAIT_TIME_MS", 3000)),
		OneClickUseFlexInputSwap: getEnvOrDefault("ONECLICK_USE_FLEX_INPUT_SWAP", "true") == "true",
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

func parseReferralCode(raw string) string {
	code := strings.TrimSpace(raw)
	if code == "" {
		return ""
	}

	if idx := strings.LastIndex(code, "/join/"); idx != -1 {
		code = code[idx+len("/join/"):]
	}
	if idx := strings.IndexAny(code, "?#"); idx != -1 {
		code = code[:idx]
	}
	code = strings.Trim(code, "/")
	return strings.TrimSpace(code)
}

// getEnvOrDefault 获取环境变量或返回默认值
func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvOrDefaultFloat(key string, defaultValue float64) float64 {
	if value := os.Getenv(key); value != "" {
		if f, err := strconv.ParseFloat(value, 64); err == nil {
			return f
		}
	}
	return defaultValue
}

func getEnvOrDefaultInt64(key string, defaultValue int64) int64 {
	if value := os.Getenv(key); value != "" {
		if v, err := strconv.ParseInt(value, 10, 64); err == nil {
			return v
		}
	}
	return defaultValue
}
