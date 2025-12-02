package config

import "fmt"

// NLTradingConfig 自然语言交易配置
type NLTradingConfig struct {
	// 是否启用自然语言交易
	Enabled bool `json:"enabled" yaml:"enabled"`

	// 大额交易确认阈值（美元）
	ConfirmationThreshold float64 `json:"confirmation_threshold" yaml:"confirmation_threshold"`

	// 最小置信度阈值
	MinConfidence float64 `json:"min_confidence" yaml:"min_confidence"`

	// 速率限制（秒）
	RateLimitSeconds int `json:"rate_limit_seconds" yaml:"rate_limit_seconds"`

	// 是否需要AI解析（如果false，仅使用正则表达式）
	UseAIParsing bool `json:"use_ai_parsing" yaml:"use_ai_parsing"`
}

// DefaultNLTradingConfig 返回默认的自然语言交易配置
func DefaultNLTradingConfig() *NLTradingConfig {
	return &NLTradingConfig{
		Enabled:              true,
		ConfirmationThreshold: 500.0,
		MinConfidence:         0.8,
		RateLimitSeconds:      10,
		UseAIParsing:          true,
	}
}

// Validate 验证配置
func (c *NLTradingConfig) Validate() error {
	if c.ConfirmationThreshold < 0 {
		return fmt.Errorf("确认阈值不能为负数")
	}

	if c.MinConfidence < 0 || c.MinConfidence > 1 {
		return fmt.Errorf("置信度阈值必须在0-1之间")
	}

	if c.RateLimitSeconds < 1 {
		return fmt.Errorf("速率限制至少为1秒")
	}

	return nil
}