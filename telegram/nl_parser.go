package telegram

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"regexp"
	"strconv"
	"strings"

	"nofx/mcp"
)

// ParsedCommand 表示解析后的交易命令
type ParsedCommand struct {
	Action     string  `json:"action"`     // "long", "short", "close", "stop_loss", "take_profit", "close_all"
	Symbol     string  `json:"symbol"`     // "ETH", "BTC", etc.
	AssetType  string  `json:"asset_type"` // "crypto", "stock", "forex", "commodity"
	Leverage   int     `json:"leverage"`   // 2, 3, 5, etc. (0 for close operations)
	Amount     float64 `json:"amount"`     // 15.0, 20.0, etc. (0 for close all)
	Currency   string  `json:"currency"`   // "USD", "USDT", "USDC"
	Price      float64 `json:"price"`      // 0 for market orders, specific price for stop/take
	Percentage float64 `json:"percentage"` // For partial closes (0.5 = 50%)
	Confidence float64 `json:"confidence"` // 0-1, parsing confidence (min 0.8)
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

	// 仅使用 MCP AI 解析，失败直接返回错误
	cmd, err := p.parseWithAI(message)
	if err != nil {
		return nil, err
	}

	return cmd, nil
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
	prompt := fmt.Sprintf(`你是一个专业的交易命令解析器。请从用户消息中提取交易参数并只返回纯净的JSON对象。

🚨 关键格式要求：
1. 直接返回JSON对象，不要任何markdown标记
2. 不要使用代码块标记，直接返回JSON
3. 不要添加任何解释文字或前言
4. 确保asset_type字段不为空

用户消息: "%s"

请直接返回JSON（无其他文字）:
{
  "action": "long|short|close|stop_loss|take_profit|close_all",
  "symbol": "交易代码(大写，无空格)",
  "asset_type": "crypto|stock|forex|commodity",
  "leverage": 数字或0,
  "amount": 数字或0,
  "currency": "USD|USDT|USDC",
  "price": 数字或0,
  "percentage": 数字或0,
  "confidence": 0.0-1.0
}

🚨 重要提醒 🚨
1. asset_type字段必须返回，绝对不能为空！
2. 资产类型判断是交易成功的关键，请务必准确识别
3. 如果AI返回空的asset_type，整个交易将失败！
4. 请基于上下文和符号特征进行最准确的判断

典型映射：
- "特斯拉"、"TSLA" → stock
- "苹果"、"AAPL" → stock
- "谷歌"、"GOOGL" → stock
- "微软"、"MSFT" → stock
- "英伟达"、"NVDA" → stock
- "比特币"、"BTC" → crypto
- "以太坊"、"ETH" → crypto
- "欧元美元"、"EURUSD" → forex
- "黄金"、"GOLD" → commodity
- "原油"、"OIL" → commodity

资产类型智能判断规则:
1. "crypto": 加密货币
   - 特征: 通常为3-5个大写字母，如BTC、ETH、SOL、BNB、DOGE、ADA、DOT、LINK、MATIC、AVAX、ATOM、UNI等
   - 上下文关键词: "币"、"加密货币"、"区块链"、"Web3"、"DeFi"等
   - 示例: "买入BTC"、"做空ETH"、"开多SOL"

2. "stock": 股票
   - 特征: 1-5个大写字母，知名公司缩写，如TSLA(特斯拉)、AAPL(苹果)、GOOGL(谷歌)、MSFT(微软)、NVDA(英伟达)、META、AMZN、NFLX、INTC等
   - 上下文关键词: "股票"、"公司"、"美股"、"财报"、"股价"等
   - 示例: "买入特斯拉股票"、"做空苹果"、"开多TSLA"

3. "forex": 外汇
   - 特征: 6个字符，通常为两个3字母货币代码组合，如EURUSD、GBPUSD、USDJPY、AUDUSD、USDCAD、NZDUSD、EURJPY、GBPJPY等
   - 上下文关键词: "外汇"、"货币对"、"汇率"等
   - 示例: "买入欧元美元"、"做空英镑美元"

4. "commodity": 商品
   - 特征: 通常为商品名称缩写，如GOLD、SILVER、OIL、GAS、WHEAT、CORN、COPPER、NATGAS等
   - 上下文关键词: "黄金"、"原油"、"白银"、"大宗商品"等
   - 示例: "买入黄金"、"做空原油"

智能判断策略:
- 优先根据交易代码的格式特征进行判断
- 结合用户输入中的上下文关键词和语言习惯
- 考虑交易代码的行业知名度和常见用法
- 如果有歧义，根据最可能的场景进行判断
- 置信度要反映对资产类型判断的把握程度`, message)

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

	// 验证提取的JSON质量
	if jsonStr == "{}" {
		log.Printf("❌ 警告：提取到空JSON对象")
		log.Printf("❌ AI完整响应: %s", response)
		return nil, fmt.Errorf("AI返回的JSON格式不正确")
	}

	// 解析 JSON
	var cmd ParsedCommand
	if err := json.Unmarshal([]byte(jsonStr), &cmd); err != nil {
		log.Printf("❌ JSON解析失败: %v", err)
		log.Printf("❌ 提取的JSON: %s", jsonStr)
		return nil, fmt.Errorf("JSON 解析失败: %w", err)
	}

	// 🔍 记录AI原始响应（新增日志）
	log.Printf("🎯 AI原始响应详情:")
	log.Printf("   用户输入: %s", message)
	log.Printf("   AI完整响应: %s", response)
	log.Printf("   提取的JSON: %s", jsonStr)

	// 验证关键字段
	if cmd.AssetType == "" {
		log.Printf("❌ AssetType字段为空，AI解析不完整")
		log.Printf("❌ 提取的JSON: %s", jsonStr)
		log.Printf("❌ 建议优化AI提示词以强调asset_type字段的重要性")
		return nil, fmt.Errorf("AI未返回有效的asset_type字段")
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

	// 提取交易对：放宽为 2-10 位字母（默认拼接 USDT）
	symbolPattern := regexp.MustCompile(`(?i)([A-Z]{2,10})`)
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

	// 提取金额：放开正则限制，捕获第一个数字作为金额（避开与杠杆相同的数字）
	numPattern := regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)`)
	allNums := numPattern.FindAllString(message, -1)
	for _, n := range allNums {
		val, err := strconv.ParseFloat(n, 64)
		if err != nil {
			continue
		}
		// 跳过与已识别杠杆相同的数字（如“2倍做多”中的 2）
		if cmd.Leverage > 0 && math.Abs(val-float64(cmd.Leverage)) < 1e-9 {
			continue
		}
		cmd.Amount = val
		cmd.Currency = "USD"
		break
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
	// 首先尝试查找纯净JSON对象
	if startIdx := strings.Index(response, "{"); startIdx != -1 {
		braceCount := 0
		for i := startIdx; i < len(response); i++ {
			switch response[i] {
			case '{':
				braceCount++
			case '}':
				braceCount--
				if braceCount == 0 {
					jsonCandidate := response[startIdx : i+1]
					// 简单验证是否包含必需字段
					if strings.Contains(jsonCandidate, "\"action\"") &&
					   strings.Contains(jsonCandidate, "\"symbol\"") &&
					   strings.Contains(jsonCandidate, "\"asset_type\"") {
						return jsonCandidate
					}
				}
			}
		}
	}

	return ""
}

// normalizeCommand 标准化解析后的命令
func (p *NLParser) normalizeCommand(cmd ParsedCommand) ParsedCommand {
	// 🔍 记录AI解析结果（保留日志用于验证）
	log.Printf("🤖 AI解析结果详情:")
	log.Printf("   Action: %s", cmd.Action)
	log.Printf("   Symbol: %s", cmd.Symbol)
	log.Printf("   AssetType: '%s'", cmd.AssetType)
	log.Printf("   Leverage: %d", cmd.Leverage)
	log.Printf("   Amount: %.2f", cmd.Amount)
	log.Printf("   Currency: %s", cmd.Currency)
	log.Printf("   Price: %.2f", cmd.Price)
	log.Printf("   Percentage: %.2f", cmd.Percentage)
	log.Printf("   Confidence: %.2f", cmd.Confidence)

	// 如果AssetType为空，记录错误但不进行后备推断
	if cmd.AssetType == "" && cmd.Symbol != "" {
		log.Printf("❌ AssetType为空！AI解析失败，交易将被拒绝")
		log.Printf("   要求：AI必须返回有效的asset_type字段")
		log.Printf("   符号: %s", cmd.Symbol)
	} else if cmd.AssetType != "" {
		log.Printf("✅ AssetType正常: %s (AI解析成功)", cmd.AssetType)
	}

	// 标准化交易对名称
	if cmd.Symbol != "" {
		switch cmd.AssetType {
		case "crypto":
			// 加密货币添加USDT后缀
			if !strings.HasSuffix(cmd.Symbol, "USDT") && !strings.HasSuffix(cmd.Symbol, "USD") {
				cmd.Symbol = cmd.Symbol + "USDT"
			}
		case "stock":
			// 股票保持原样，不添加USDT后缀
			// Hyperliquid.resolveCoin会处理HIP-3格式转换
		case "forex":
			// 外汇保持货币对格式，如EURUSD
		case "commodity":
			// 商品保持标准代码，如GOLD
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
