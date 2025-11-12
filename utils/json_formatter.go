package utils

import (
	"encoding/json"
	"strings"
)

// FormatJSONError 格式化JSON错误响应，使其更易读
func FormatJSONError(jsonStr string) string {
	// 尝试解析JSON
	var jsonObj interface{}
	if err := json.Unmarshal([]byte(jsonStr), &jsonObj); err != nil {
		// 如果不是有效的JSON，直接返回原始字符串
		return jsonStr
	}

	// 重新格式化为缩进的JSON
	formatted, err := json.MarshalIndent(jsonObj, "", "  ")
	if err != nil {
		// 格式化失败，返回原始字符串
		return jsonStr
	}

	return string(formatted)
}

// FormatAPIErrorResponse 格式化API错误响应消息
func FormatAPIErrorResponse(symbol, action, errorDetails string) string {
	// 格式化JSON部分
	formattedJSON := FormatJSONError(errorDetails)

	// 构建格式化的错误消息
	var msg strings.Builder
	msg.WriteString("❌ ")

	// 根据action类型显示不同的标题
	switch {
	case strings.Contains(action, "open_long"):
		msg.WriteString("开多失败")
	case strings.Contains(action, "open_short"):
		msg.WriteString("开空失败")
	case strings.Contains(action, "close"):
		msg.WriteString("平仓失败")
	case strings.Contains(action, "reduce"):
		msg.WriteString("减仓失败")
	default:
		msg.WriteString("交易失败")
	}

	msg.WriteString("\n\n")
	msg.WriteString("交易对: ")
	msg.WriteString(symbol)
	msg.WriteString("\n")
	msg.WriteString("操作类型: ")

	// 美化操作类型显示
	switch {
	case strings.Contains(action, "open_long"):
		msg.WriteString("开多")
	case strings.Contains(action, "open_short"):
		msg.WriteString("开空")
	case strings.Contains(action, "close"):
		msg.WriteString("平仓")
	case strings.Contains(action, "reduce"):
		msg.WriteString("减仓")
	default:
		msg.WriteString(action)
	}

	msg.WriteString("\n\n")
	msg.WriteString("原始错误信息:\n")
	msg.WriteString("```json\n")
	msg.WriteString(formattedJSON)
	msg.WriteString("\n```")

	return msg.String()
}