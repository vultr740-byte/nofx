package telegram

import (
	"fmt"
	"log"
	"strings"

	"nofx/config"
)

// CommandValidator 交易命令验证器
type CommandValidator struct {
	db               config.DatabaseInterface
	confirmationThreshold float64 // 大额交易确认阈值
}

// NewCommandValidator 创建新的交易验证器
func NewCommandValidator(db config.DatabaseInterface) *CommandValidator {
	return &CommandValidator{
		db:                     db,
		confirmationThreshold: 500.0, // 默认 $500 需要确认
	}
}

// SetConfirmationThreshold 设置大额交易确认阈值
func (cv *CommandValidator) SetConfirmationThreshold(threshold float64) {
	cv.confirmationThreshold = threshold
}

// ValidateCommand 验证交易命令
func (cv *CommandValidator) ValidateCommand(telegramID int64, cmd *ParsedCommand) error {
	// 基本参数验证
	if err := cv.validateBasicParams(cmd); err != nil {
		return err
	}

	// 获取用户配置
	userConfig, err := cv.getUserConfig(telegramID)
	if err != nil {
		return fmt.Errorf("获取用户配置失败: %w", err)
	}

	// 验证杠杆限制
	if err := cv.validateLeverage(cmd, userConfig); err != nil {
		return err
	}

	// 验证交易对
	if err := cv.validateSymbol(cmd); err != nil {
		return err
	}

	// 验证金额
	if err := cv.validateAmount(cmd); err != nil {
		return err
	}

	// 检查持仓冲突
	if err := cv.validatePositionConflict(telegramID, cmd); err != nil {
		return err
	}

	return nil
}

// validateBasicParams 验证基本参数
func (cv *CommandValidator) validateBasicParams(cmd *ParsedCommand) error {
	if cmd.Action == "" {
		return fmt.Errorf("❌ 未指定操作类型")
	}

	validActions := []string{"long", "short", "close", "stop_loss", "take_profit", "close_all"}
	isValidAction := false
	for _, action := range validActions {
		if cmd.Action == action {
			isValidAction = true
			break
		}
	}
	if !isValidAction {
		return fmt.Errorf("❌ 无效的操作类型: %s", cmd.Action)
	}

	// 对于非全部平仓操作，必须有交易对
	if cmd.Action != "close_all" && cmd.Symbol == "" {
		return fmt.Errorf("❌ 未指定交易对")
	}

	// 验证杠杆
	if cmd.Leverage < 0 {
		return fmt.Errorf("❌ 杠杆不能为负数")
	}
	if cmd.Leverage > 0 && cmd.Leverage < 1 {
		return fmt.Errorf("❌ 杠杆不能小于1倍")
	}

	// 验证金额
	if cmd.Amount < 0 {
		return fmt.Errorf("❌ 交易金额不能为负数")
	}

	// 验证百分比
	if cmd.Percentage < 0 || cmd.Percentage > 1 {
		return fmt.Errorf("❌ 平仓百分比必须在0-1之间")
	}

	return nil
}

// validateLeverage 验证杠杆限制
func (cv *CommandValidator) validateLeverage(cmd *ParsedCommand, userConfig *config.TgTraderRecord) error {
	// 开仓操作需要检查杠杆
	if cmd.Action == "long" || cmd.Action == "short" {
		if cmd.Leverage == 0 {
			// 如果没有指定杠杆，使用用户默认杠杆
			if cv.isMainAsset(cmd.Symbol) {
				cmd.Leverage = userConfig.BTCETHLeverage
			} else {
				cmd.Leverage = userConfig.AltcoinLeverage
			}
			log.Printf("📊 使用默认杠杆: %dx", cmd.Leverage)
		}

		// 检查杠杆上限
		if cv.isMainAsset(cmd.Symbol) {
			// BTC/ETH 最大杠杆
			if cmd.Leverage > userConfig.BTCETHLeverage {
				return fmt.Errorf("❌ 杠杆超出限制: %d > %d (主币最大杠杆)",
					cmd.Leverage, userConfig.BTCETHLeverage)
			}
		} else {
			// 山寨币最大杠杆
			if cmd.Leverage > userConfig.AltcoinLeverage {
				return fmt.Errorf("❌ 杠杆超出限制: %d > %d (山寨币最大杠杆)",
					cmd.Leverage, userConfig.AltcoinLeverage)
			}
		}
	}

	return nil
}

// validateSymbol 验证交易对
func (cv *CommandValidator) validateSymbol(cmd *ParsedCommand) error {
	// 标准化交易对名称
	symbol := strings.ToUpper(cmd.Symbol)

	// 移除 USDT/USD 后缀进行比较
	baseSymbol := symbol
	if strings.HasSuffix(baseSymbol, "USDT") {
		baseSymbol = strings.TrimSuffix(baseSymbol, "USDT")
	} else if strings.HasSuffix(baseSymbol, "USD") {
		baseSymbol = strings.TrimSuffix(baseSymbol, "USD")
	}

	// 支持的交易对列表
	supportedSymbols := []string{
		"BTC", "ETH", "SOL", "BNB", "DOGE", "ADA", "DOT", "LINK", "MATIC",
		"AVAX", "UNI", "ATOM", "FIL", "ICP", "VET", "THETA", "XLM", "AAVE",
	}

	isSupported := false
	for _, supported := range supportedSymbols {
		if baseSymbol == supported {
			isSupported = true
			break
		}
	}

	if !isSupported {
		return fmt.Errorf("❌ 不支持的交易对: %s", cmd.Symbol)
	}

	// 更新为标准格式
	if !strings.HasSuffix(symbol, "USDT") {
		cmd.Symbol = baseSymbol + "USDT"
	}

	return nil
}

// validateAmount 验证交易金额
func (cv *CommandValidator) validateAmount(cmd *ParsedCommand) error {
	// 检查最小交易金额
	if cmd.Action == "long" || cmd.Action == "short" {
		if cmd.Amount == 0 {
			return fmt.Errorf("❌ 开仓必须指定交易金额")
		}
		if cmd.Amount < 10 {
			return fmt.Errorf("❌ 交易金额过小: $%.2f (最小 $10)", cmd.Amount)
		}
		if cmd.Amount > 100000 {
			return fmt.Errorf("❌ 交易金额过大: $%.2f (最大 $100,000)", cmd.Amount)
		}
	}

	return nil
}

// validatePositionConflict 检查持仓冲突
func (cv *CommandValidator) validatePositionConflict(telegramID int64, cmd *ParsedCommand) error {
	// TODO: 实现持仓检查逻辑
	// 需要查询当前用户的持仓情况
	// 检查是否已有相反方向的持仓等
	return nil
}

// NeedsConfirmation 检查是否需要用户确认
func (cv *CommandValidator) NeedsConfirmation(cmd *ParsedCommand) bool {
	// 大额交易需要确认
	if cmd.Action == "long" || cmd.Action == "short" {
		return cmd.Amount >= cv.confirmationThreshold
	}

	// 全部平仓需要确认
	if cmd.Action == "close_all" {
		return true
	}

	// 设置止损止盈需要确认
	if cmd.Action == "stop_loss" || cmd.Action == "take_profit" {
		return true
	}

	return false
}

// isMainAsset 判断是否为主流资产
func (cv *CommandValidator) isMainAsset(symbol string) bool {
	symbol = strings.ToUpper(symbol)
	if strings.HasSuffix(symbol, "USDT") {
		symbol = strings.TrimSuffix(symbol, "USDT")
	} else if strings.HasSuffix(symbol, "USD") {
		symbol = strings.TrimSuffix(symbol, "USD")
	}

	mainAssets := []string{"BTC", "ETH"}
	for _, asset := range mainAssets {
		if symbol == asset {
			return true
		}
	}
	return false
}

// getUserConfig 获取用户配置
func (cv *CommandValidator) getUserConfig(telegramID int64) (*config.TgTraderRecord, error) {
	// 查询数据库获取用户配置
	records, err := cv.db.GetTgTraders(telegramID)
	if err != nil {
		return nil, err
	}

	if len(records) == 0 {
		return nil, fmt.Errorf("用户未配置交易员")
	}

	// 返回第一个配置（用户可能有多个交易员，这里简化处理）
	return &records[0], nil
}

// LogValidation 记录验证日志
func (cv *CommandValidator) LogValidation(telegramID int64, cmd *ParsedCommand, err error) {
	if err != nil {
		log.Printf("❌ 命令验证失败 [用户:%d]: %v", telegramID, err)
	} else {
		log.Printf("✅ 命令验证通过 [用户:%d]: %s %s", telegramID, cmd.Action, cmd.Symbol)
	}
}

// CheckRateLimit 检查频率限制
func (cv *CommandValidator) CheckRateLimit(telegramID int64) error {
	// TODO: 实现频率限制逻辑
	// 例如：10秒内只能执行一次交易
	// 使用 Redis 或内存存储最后交易时间
	return nil
}

// GetRiskWarnings 获取风险警告
func (cv *CommandValidator) GetRiskWarnings(cmd *ParsedCommand) []string {
	var warnings []string

	// 高杠杆警告
	if cmd.Leverage >= 10 {
		warnings = append(warnings, "⚠️ 高杠杆交易风险极大，请谨慎操作")
	}

	// 大额交易警告
	if cmd.Amount >= 10000 {
		warnings = append(warnings, "⚠️ 大额交易请确认风险可控")
	}

	// 山寨币警告
	if !cv.isMainAsset(cmd.Symbol) && cmd.Leverage >= 5 {
		warnings = append(warnings, "⚠️ 山寨币高杠杆波动巨大，请做好风险管理")
	}

	return warnings
}

// ValidateRiskLimits 验证系统风险限制
func (cv *CommandValidator) ValidateRiskLimits(telegramID int64, cmd *ParsedCommand) error {
	// TODO: 实现系统级风险检查
	// 例如：用户总持仓价值、单币种持仓限制等
	return nil
}

// ConfirmLargeTrade 确认大额交易
func (cv *CommandValidator) ConfirmLargeTrade(telegramID int64, cmd *ParsedCommand, confirmed bool) error {
	if !confirmed {
		return fmt.Errorf("用户取消了交易")
	}

	// 记录确认日志
	log.Printf("✅ 用户确认大额交易 [用户:%d]: %s %s $%.2f",
		telegramID, cmd.Action, cmd.Symbol, cmd.Amount)

	return nil
}

// GetTradingLimits 获取交易限制信息
func (cv *CommandValidator) GetTradingLimits(telegramID int64) map[string]interface{} {
	userConfig, err := cv.getUserConfig(telegramID)
	if err != nil {
		return map[string]interface{}{
			"error": "无法获取用户配置",
		}
	}

	return map[string]interface{}{
		"btc_eth_max_leverage": userConfig.BTCETHLeverage,
		"altcoin_max_leverage": userConfig.AltcoinLeverage,
		"min_trade_amount":    10.0,
		"max_trade_amount":    100000.0,
		"confirmation_threshold": cv.confirmationThreshold,
		"rate_limit_seconds":  10,
	}
}