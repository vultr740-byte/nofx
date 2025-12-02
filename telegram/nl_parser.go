package telegram

import (
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"

	"nofx/mcp"
)

// ParsedCommand 表示解析后的交易命令
type ParsedCommand struct {
	Action     string  // "long", "short", "close", "stop_loss", "take_profit", "close_all"
	Symbol     string  // "ETH", "BTC", etc.
	Leverage   int     // 2, 3, 5, etc. (0 for close operations)
	Amount     float64 // 15.0, 20.0, etc. (0 for close all)
	Currency   string  // "USD", "USDT", "USDC"
	Price      float64 // 0 for market orders, specific price for stop/take
	Percentage float64 // For partial closes (0.5 = 50%)
	Confidence float64 // 0-1, parsing confidence (min 0.8)
}

// NLParser 自然语言交易命令解析器
type NLParser struct {
	mcpClient *mcp.Client
	enabled   bool
}

// NewNLParser 创建新的自然语言解析器
func NewNLParser(mcpClient *mcp.Client) *NLParser {
	return &NLParser{
		mcpClient: mcpClient,
		enabled:   true,
	}
}

// IsEnabled 检查解析器是否启用
func (p *NLParser) IsEnabled() bool {
	return p.enabled
}

// SetEnabled 设置解析器启用状态
func (p *NLParser) SetEnabled(enabled bool) {
	p.enabled = enabled
}

// ParseCommand 解析自然语言交易命令
func (p *NLParser) ParseCommand(message string) (*ParsedCommand, error) {
	if !p.IsEnabled() {
		return nil, fmt.Errorf("自然语言解析器未启用")
	}

	// 首先尝试快速检测是否可能是交易命令
	if !p.isTradingCommand(message) {
		return nil, nil // 不是交易命令，返回 nil
	}

	// 如果有 MCP 客户端，优先使用 AI；否则直接使用正则
	if p.mcpClient != nil {
		cmd, err := p.parseWithAI(message)
		if err == nil {
			return cmd, nil
		}
		log.Printf("❌ AI 解析失败: %v，改用正则后备", err)
	}

	// 尝试使用正则表达式作为后备
	return p.parseWithRegex(message)
}

// isTradingCommand 快速检测是否是交易相关的消息
func (p *NLParser) isTradingCommand(message string) bool {
	// 检查是否包含交易相关关键词
	tradingKeywords := []string{
		"做多", "做空", "开多", "开空", "买入", "卖出", "买", "卖",
		"long", "short", "buy", "sell",
		"倍", "x", "X",
		"平仓", "平", "close",
		"止损", "止盈", "stop", "take",
		"$", "U", "USD", "USDT", "USDC",
		"BTC", "ETH", "SOL", "BNB", "DOGE", "ADA", "DOT", "LINK", "MATIC",
	}

	msgLower := strings.ToLower(message)
	for _, keyword := range tradingKeywords {
		if strings.Contains(msgLower, strings.ToLower(keyword)) {
			return true
		}
	}

	return false
}

// parseWithAI 使用 AI 模型解析命令
func (p *NLParser) parseWithAI(message string) (*ParsedCommand, error) {
	prompt := fmt.Sprintf(`你是一个交易命令解析器。请从用户消息中提取交易参数，只返回 JSON 格式。

用户消息: "%s"

请返回以下 JSON 格式（仅返回 JSON，不要其他文字）:
{
  "action": "long|short|close|stop_loss|take_profit|close_all",
  "symbol": "BTC|ETH|SOL|BNB|DOGE|ADA|DOT|LINK|MATIC|...",
  "leverage": 数字或0,
  "amount": 数字或0,
  "currency": "USD|USDT|USDC",
  "price": 数字或0,
  "percentage": 数字或0,
  "confidence": 0.0-1.0
}

注意事项:
1. action: 开多用"long", 开空用"short", 平仓用"close", 全部平仓用"close_all"
2. leverage: 杠杆倍数，如果没有提到则为0
3. amount: 交易金额，如果没有提到则为0
4. confidence: 解析的置信度，0.0到1.0之间，如果不确定就降低置信度
5. 百分比平仓使用percentage字段（0.5表示50%%）`, message)

	// 调用 AI 模型
	systemPrompt := "你是一个专业的交易命令解析器，只返回JSON格式的结果。"
	response, err := p.mcpClient.CallWithMessages(systemPrompt, prompt)
	if err != nil {
		return nil, fmt.Errorf("AI 调用失败: %w", err)
	}

	// 提取 JSON 响应
	jsonStr := p.extractJSON(response)
	if jsonStr == "" {
		return nil, fmt.Errorf("无法从 AI 响应中提取 JSON")
	}

	// 解析 JSON
	var cmd ParsedCommand
	if err := json.Unmarshal([]byte(jsonStr), &cmd); err != nil {
		return nil, fmt.Errorf("JSON 解析失败: %w", err)
	}

	// 标准化处理
	cmd = p.normalizeCommand(cmd)

	return &cmd, nil
}

// parseWithRegex 使用正则表达式作为后备解析方案
func (p *NLParser) parseWithRegex(message string) (*ParsedCommand, error) {
	cmd := &ParsedCommand{
		Confidence: 0.6, // 正则解析置信度较低
	}

	// 提取交易对
	symbolPattern := regexp.MustCompile(`(?i)(BTC|ETH|SOL|BNB|DOGE|ADA|DOT|LINK|MATIC)`)
	if match := symbolPattern.FindString(message); match != "" {
		cmd.Symbol = strings.ToUpper(match)
	}

	// 判断操作类型
	if strings.Contains(message, "做多") || strings.Contains(message, "long") || strings.Contains(message, "买入") {
		cmd.Action = "long"
	} else if strings.Contains(message, "做空") || strings.Contains(message, "short") || strings.Contains(message, "卖出") {
		cmd.Action = "short"
	} else if strings.Contains(message, "平仓") || strings.Contains(message, "close") {
		cmd.Action = "close"
	}

	// 提取杠杆
	leveragePattern := regexp.MustCompile(`(\d+)\s*[倍xX]`)
	if matches := leveragePattern.FindStringSubmatch(message); len(matches) > 1 {
		if leverage, err := strconv.Atoi(matches[1]); err == nil {
			cmd.Leverage = leverage
		}
	}

	// 提取金额
	amountPatterns := []string{
		`(\d+(?:\.\d+)?)\s*\$`,             // $100
		`(\d+(?:\.\d+)?)\s*U`,              // 100U
		`(\d+(?:\.\d+)?)\s*USD`,            // 100USD
		`(\d+(?:\.\d+)?)\s*USDT`,           // 100USDT
		`(\d+(?:\.\d+)?)\s*USDC`,           // 100USDC
		`(\d+(?:\.\d+)?)\s*(美元|美金|美刀|刀|元)`, // 100美元/元
	}

	for _, pattern := range amountPatterns {
		if matches := regexp.MustCompile(pattern).FindStringSubmatch(message); len(matches) > 1 {
			if amount, err := strconv.ParseFloat(matches[1], 64); err == nil {
				cmd.Amount = amount
				cmd.Currency = "USD"
				break
			}
		}
	}

	// 如果没有提取到必要信息，降低置信度
	if cmd.Action == "" || cmd.Symbol == "" {
		cmd.Confidence = 0.3
		return nil, fmt.Errorf("无法提取必要的交易参数")
	}

	return cmd, nil
}

// extractJSON 从 AI 响应中提取 JSON 字符串
func (p *NLParser) extractJSON(response string) string {
	// 查找 JSON 对象的开始和结束
	startIdx := strings.Index(response, "{")
	if startIdx == -1 {
		return ""
	}

	// 找到匹配的右括号
	braceCount := 0
	for i := startIdx; i < len(response); i++ {
		switch response[i] {
		case '{':
			braceCount++
		case '}':
			braceCount--
			if braceCount == 0 {
				return response[startIdx : i+1]
			}
		}
	}

	return ""
}

// normalizeCommand 标准化解析后的命令
func (p *NLParser) normalizeCommand(cmd ParsedCommand) ParsedCommand {
	// 标准化交易对名称
	if cmd.Symbol != "" {
		if !strings.HasSuffix(cmd.Symbol, "USDT") && !strings.HasSuffix(cmd.Symbol, "USD") {
			cmd.Symbol = cmd.Symbol + "USDT"
		}
	}

	// 标准化货币单位
	cmd.Currency = strings.ToUpper(cmd.Currency)
	if cmd.Currency == "" && cmd.Amount > 0 {
		cmd.Currency = "USD" // 默认使用 USD
	}

	// 验证操作类型
	validActions := map[string]bool{
		"long": true, "short": true, "close": true,
		"stop_loss": true, "take_profit": true, "close_all": true,
	}
	if !validActions[cmd.Action] {
		cmd.Confidence = 0.0
	}

	return cmd
}

// ValidateCommand 验证解析后的命令
func (p *NLParser) ValidateCommand(cmd *ParsedCommand) error {
	if cmd == nil {
		return fmt.Errorf("命令为空")
	}

	if cmd.Confidence < 0.8 {
		return fmt.Errorf("解析置信度过低: %.2f", cmd.Confidence)
	}

	if cmd.Action == "" {
		return fmt.Errorf("未指定操作类型")
	}

	if cmd.Action != "close_all" && cmd.Symbol == "" {
		return fmt.Errorf("未指定交易对")
	}

	// 验证杠杆
	if cmd.Leverage > 100 {
		return fmt.Errorf("杠杆倍数过高: %d", cmd.Leverage)
	}

	// 验证金额
	if cmd.Amount < 0 {
		return fmt.Errorf("交易金额不能为负数: %.2f", cmd.Amount)
	}

	return nil
}

// GetConfirmationMessage 获取交易确认消息
func (p *NLParser) GetConfirmationMessage(cmd *ParsedCommand) string {
	switch cmd.Action {
	case "long":
		return fmt.Sprintf("确认开多 %s？\n杠杆: %dx\n金额: $%.2f", cmd.Symbol, cmd.Leverage, cmd.Amount)
	case "short":
		return fmt.Sprintf("确认开空 %s？\n杠杆: %dx\n金额: $%.2f", cmd.Symbol, cmd.Leverage, cmd.Amount)
	case "close":
		if cmd.Percentage > 0 {
			return fmt.Sprintf("确认平仓 %.0f%% 的 %s？", cmd.Percentage*100, cmd.Symbol)
		}
		return fmt.Sprintf("确认平仓 %s？", cmd.Symbol)
	case "close_all":
		return "确认平仓所有持仓？"
	case "stop_loss":
		return fmt.Sprintf("确认设置 %s 止损价 $%.2f？", cmd.Symbol, cmd.Price)
	case "take_profit":
		return fmt.Sprintf("确认设置 %s 止盈价 $%.2f？", cmd.Symbol, cmd.Price)
	default:
		return fmt.Sprintf("确认执行操作: %s", cmd.Action)
	}
}

// LogCommand 记录命令日志
func (p *NLParser) LogCommand(telegramID int64, originalMessage string, cmd *ParsedCommand) {
	log.Printf("🤖 自然语言命令 [用户:%d]: %s", telegramID, originalMessage)
	if cmd != nil {
		log.Printf("   解析结果: Action=%s, Symbol=%s, Leverage=%d, Amount=$%.2f, Confidence=%.2f",
			cmd.Action, cmd.Symbol, cmd.Leverage, cmd.Amount, cmd.Confidence)
	}
}
