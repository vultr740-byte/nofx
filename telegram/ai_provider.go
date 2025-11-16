package telegram

import "strings"

// normalizeAIProvider 统一AI提供商标识，默认返回deepseek
func normalizeAIProvider(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "qwen", "qianwen", "通义", "tongyi":
		return "qwen"
	default:
		return "deepseek"
	}
}

// aiProviderDisplayName 获取提供商用于展示的名称
func aiProviderDisplayName(provider string) string {
	switch normalizeAIProvider(provider) {
	case "qwen":
		return "Qwen"
	default:
		return "DeepSeek"
	}
}
