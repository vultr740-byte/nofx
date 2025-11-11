package telegram

import (
	"fmt"
	"log"
	"strconv"
	"strings"
)

// ConfigWizard 配置向导
type ConfigWizard struct {
	ttm *TelegramTraderManager
}

// NewConfigWizard 创建配置向导
func NewConfigWizard(ttm *TelegramTraderManager) *ConfigWizard {
	return &ConfigWizard{
		ttm: ttm,
	}
}

// StartWizard 开始配置向导（简化版 - 直接进入快速配置）
func (cw *ConfigWizard) StartWizard(telegramID int64) (string, error) {
	log.Printf("🔍 StartWizard: Starting wizard for telegramID=%d", telegramID)
	session := cw.ttm.GetSessionManager().GetOrCreateSession(telegramID)
	log.Printf("🔍 StartWizard: Retrieved session, current state=%s", session.State)
	session.State = StateQuickSetup  // 直接进入快速配置模式
	session.TraderConfig = &TraderConfig{}
	log.Printf("🔍 StartWizard: Set session state to %s", session.State)

	return cw.getQuickSetupMessage(), nil
}

// ProcessInput 处理用户输入
func (cw *ConfigWizard) ProcessInput(telegramID int64, input string) (string, bool, error) {
	session := cw.ttm.GetSessionManager().GetOrCreateSession(telegramID)

	// Debug logging
	log.Printf("🔍 ProcessInput: telegramID=%d, input=%s, currentState=%s", telegramID, input, session.State)

	// 检查是否想要取消
	if strings.ToLower(input) == "cancel" || input == "取消" {
		log.Printf("🔍 User cancelled configuration for telegramID=%d", telegramID)
		cw.ttm.GetSessionManager().ClearSession(telegramID)
		return "❌ 配置已取消", true, nil
	}

	switch session.State {
	case StateQuickSetup:
		log.Printf("🔍 Processing quick setup selection for telegramID=%d", telegramID)
		return cw.processQuickSetupInput(telegramID, input)
	case StateChoosingPrompt:
		log.Printf("🔍 Processing prompt template selection for telegramID=%d", telegramID)
		return cw.processPromptTemplateSelection(telegramID, input)
	case StateSettingBalance:
		log.Printf("🔍 Processing balance input for telegramID=%d", telegramID)
		return cw.processBalanceInput(telegramID, input)
	case StateSettingRisk:
		log.Printf("🔍 Processing risk level input for telegramID=%d", telegramID)
		return cw.processRiskLevelInput(telegramID, input)
	case StateSettingLeverage:
		log.Printf("🔍 Processing leverage input for telegramID=%d", telegramID)
		return cw.processLeverageInput(telegramID, input)
	case StateSettingInterval:
		log.Printf("🔍 Processing interval input for telegramID=%d", telegramID)
		return cw.processIntervalInput(telegramID, input)
	case StateConfirm:
		log.Printf("🔍 Processing confirmation for telegramID=%d", telegramID)
		return cw.processConfirmation(telegramID, input)
	default:
		log.Printf("❌ Unknown state '%s' for telegramID=%d, input='%s'", session.State, telegramID, input)
		return "❌ 未知状态，请重新开始配置", true, nil
	}
}

// processPromptTemplateSelection 处理提示词模板选择
func (cw *ConfigWizard) processPromptTemplateSelection(telegramID int64, input string) (string, bool, error) {
	session := cw.ttm.GetSessionManager().GetOrCreateSession(telegramID)
	templates := GetAvailablePromptTemplates()

	choice, err := cw.ttm.ParseIntInput(input)
	if err != nil {
		return cw.getPromptTemplateMessage() + "\n\n❌ 请输入有效的数字 (1-" + strconv.Itoa(len(templates)) + ")", false, nil
	}

	if choice < 1 || choice > len(templates) {
		return cw.getPromptTemplateMessage() + "\n\n❌ 请选择有效的选项 (1-" + strconv.Itoa(len(templates)) + ")", false, nil
	}

	selectedTemplate := templates[choice-1]
	session.TraderConfig.PromptTemplate = selectedTemplate.Name
	session.State = StateSettingBalance
	cw.ttm.GetSessionManager().UpdateTraderConfig(telegramID, session.TraderConfig)

	return cw.getBalanceMessage(), false, nil
}

// processBalanceInput 处理初始资金输入
func (cw *ConfigWizard) processBalanceInput(telegramID int64, input string) (string, bool, error) {
	session := cw.ttm.GetSessionManager().GetOrCreateSession(telegramID)

	// 检查是否选择了预设选项
	choice, err := cw.ttm.ParseIntInput(input)
	if err == nil {
		balanceOptions := GetBalanceOptions()
		if choice >= 1 && choice <= len(balanceOptions) {
			session.TraderConfig.InitialBalance = balanceOptions[choice-1]
			session.State = StateSettingRisk
			cw.ttm.GetSessionManager().UpdateTraderConfig(telegramID, session.TraderConfig)
			return cw.getRiskMessage(), false, nil
		}
	}

	// 尝试解析为自定义金额
	balance, err := cw.ttm.ParseNumberInput(input)
	if err != nil {
		return cw.getBalanceMessage() + "\n\n❌ 请输入有效的数字或选择预设选项", false, nil
	}

	if balance < 100 || balance > 100000 {
		return cw.getBalanceMessage() + "\n\n❌ 初始资金应在 100-100000 USDC 之间", false, nil
	}

	session.TraderConfig.InitialBalance = balance
	session.State = StateSettingRisk
	cw.ttm.GetSessionManager().UpdateTraderConfig(telegramID, session.TraderConfig)

	return cw.getRiskMessage(), false, nil
}

// processRiskLevelInput 处理风险级别输入
func (cw *ConfigWizard) processRiskLevelInput(telegramID int64, input string) (string, bool, error) {
	session := cw.ttm.GetSessionManager().GetOrCreateSession(telegramID)
	riskLevels := GetRiskLevels()

	choice, err := cw.ttm.ParseIntInput(input)
	if err != nil {
		return cw.getRiskMessage() + "\n\n❌ 请输入有效的数字 (1-" + strconv.Itoa(len(riskLevels)) + ")", false, nil
	}

	if choice < 1 || choice > len(riskLevels) {
		return cw.getRiskMessage() + "\n\n❌ 请选择有效的选项 (1-" + strconv.Itoa(len(riskLevels)) + ")", false, nil
	}

	selectedRisk := riskLevels[choice-1]
	session.TraderConfig.RiskLevel = selectedRisk
	session.State = StateSettingLeverage
	cw.ttm.GetSessionManager().UpdateTraderConfig(telegramID, session.TraderConfig)

	return cw.getLeverageMessage(), false, nil
}

// processLeverageInput 处理杠杆倍数输入
func (cw *ConfigWizard) processLeverageInput(telegramID int64, input string) (string, bool, error) {
	session := cw.ttm.GetSessionManager().GetOrCreateSession(telegramID)

	// 检查是否使用默认设置
	if strings.ToLower(input) == "default" || input == "默认" {
		// 根据风险级别设置默认杠杆
		btcETHLeverage, altcoinLeverage := cw.ttm.getLeverageByRiskLevel(session.TraderConfig.RiskLevel)
		session.TraderConfig.BTCETHLeverage = btcETHLeverage
		session.TraderConfig.AltcoinLeverage = altcoinLeverage
		session.State = StateSettingInterval
		cw.ttm.GetSessionManager().UpdateTraderConfig(telegramID, session.TraderConfig)
		return cw.getIntervalMessage(), false, nil
	}

	// 解析自定义杠杆设置
	parts := strings.Split(input, ",")
	if len(parts) != 2 {
		return cw.getLeverageMessage() + "\n\n❌ 请输入正确的格式，如: 5,3 或输入 'default' 使用默认值", false, nil
	}

	btcETHLeverage, err := cw.ttm.ParseIntInput(strings.TrimSpace(parts[0]))
	if err != nil {
		return cw.getLeverageMessage() + "\n\n❌ BTC/ETH杠杆格式错误", false, nil
	}

	altcoinLeverage, err := cw.ttm.ParseIntInput(strings.TrimSpace(parts[1]))
	if err != nil {
		return cw.getLeverageMessage() + "\n\n❌ 山寨币杠杆格式错误", false, nil
	}

	// 验证杠杆范围
	if btcETHLeverage < 1 || btcETHLeverage > 20 {
		return cw.getLeverageMessage() + "\n\n❌ BTC/ETH杠杆应在 1-20 倍之间", false, nil
	}

	if altcoinLeverage < 1 || altcoinLeverage > 10 {
		return cw.getLeverageMessage() + "\n\n❌ 山寨币杠杆应在 1-10 倍之间", false, nil
	}

	session.TraderConfig.BTCETHLeverage = btcETHLeverage
	session.TraderConfig.AltcoinLeverage = altcoinLeverage
	session.State = StateSettingInterval
	cw.ttm.GetSessionManager().UpdateTraderConfig(telegramID, session.TraderConfig)

	return cw.getIntervalMessage(), false, nil
}

// processIntervalInput 处理扫描间隔输入
func (cw *ConfigWizard) processIntervalInput(telegramID int64, input string) (string, bool, error) {
	session := cw.ttm.GetSessionManager().GetOrCreateSession(telegramID)

	// 检查是否选择了预设选项
	choice, err := cw.ttm.ParseIntInput(input)
	if err == nil {
		intervalOptions := GetIntervalOptions()
		if choice >= 1 && choice <= len(intervalOptions) {
			session.TraderConfig.ScanIntervalMinutes = intervalOptions[choice-1]
			session.State = StateConfirm
			cw.ttm.GetSessionManager().UpdateTraderConfig(telegramID, session.TraderConfig)
			return cw.getConfirmMessage(telegramID), false, nil
		}
	}

	// 尝试解析为自定义间隔
	interval, err := cw.ttm.ParseIntInput(input)
	if err != nil {
		return cw.getIntervalMessage() + "\n\n❌ 请输入有效的数字或选择预设选项", false, nil
	}

	if interval < 1 || interval > 60 {
		return cw.getIntervalMessage() + "\n\n❌ 扫描间隔应在 1-60 分钟之间", false, nil
	}

	session.TraderConfig.ScanIntervalMinutes = interval
	session.State = StateConfirm
	cw.ttm.GetSessionManager().UpdateTraderConfig(telegramID, session.TraderConfig)

	return cw.getConfirmMessage(telegramID), false, nil
}

// processConfirmation 处理确认
func (cw *ConfigWizard) processConfirmation(telegramID int64, input string) (string, bool, error) {
	session := cw.ttm.GetSessionManager().GetOrCreateSession(telegramID)

	if strings.ToLower(input) == "y" || input == "是" || input == "yes" {
		// 创建交易员
		traderName, err := cw.ttm.CreateTrader(telegramID, session.TraderConfig)
		if err != nil {
			cw.ttm.GetSessionManager().ClearSession(telegramID)
			return fmt.Sprintf("❌ 创建交易员失败: %v", err), true, nil
		}

		cw.ttm.GetSessionManager().ClearSession(telegramID)
		return fmt.Sprintf("交易员创建成功！\n\n交易员名称: %s\n初始资金: %.0f USDC\n策略: %s\n\n现在可以使用 /start_trader 启动交易员",
			traderName, session.TraderConfig.InitialBalance, cw.getPromptDisplayNameClean(session.TraderConfig.PromptTemplate)), true, nil
	}

	if strings.ToLower(input) == "n" || input == "否" || input == "no" {
		cw.ttm.GetSessionManager().ClearSession(telegramID)
		return "❌ 配置已取消", true, nil
	}

	return cw.getConfirmMessage(telegramID) + "\n\n❌ 请输入 'y' 或 'n'", false, nil
}

// 消息生成方法
func (cw *ConfigWizard) getPromptTemplateMessage() string {
	templates := GetAvailablePromptTemplates()
	var message strings.Builder

	message.WriteString("🤖 **创建 AI 交易员 - 第1步**\n\n")
	message.WriteString("🎯 **选择交易策略**\n\n")

	for i, template := range templates {
		message.WriteString(fmt.Sprintf("%d. %s\n%s\n\n", i+1, template.DisplayName, template.Description))
	}

	message.WriteString("请回复数字选择 (1-" + strconv.Itoa(len(templates)) + ")：")
	message.WriteString("\n\n💡 *输入 'cancel' 可随时取消配置*")

	return message.String()
}

func (cw *ConfigWizard) getBalanceMessage() string {
	balanceOptions := GetBalanceOptions()
	var message strings.Builder

	message.WriteString("💰 **创建 AI 交易员 - 第2步**\n\n")
	message.WriteString("💵 **设置初始资金**\n\n")

	for i, balance := range balanceOptions {
		message.WriteString(fmt.Sprintf("%d. %.0f USDC\n", i+1, balance))
	}

	message.WriteString(fmt.Sprintf("%d. 自定义金额\n\n", len(balanceOptions)+1))
	message.WriteString("请选择预设金额或输入自定义金额 (100-100000 USDC)：")
	message.WriteString("\n\n💡 *建议新手从 1000 USDC 开始*")

	return message.String()
}

func (cw *ConfigWizard) getRiskMessage() string {
	riskLevels := GetRiskLevels()
	var message strings.Builder

	message.WriteString("⚖️ **创建 AI 交易员 - 第3步**\n\n")
	message.WriteString("🎲 **选择风险级别**\n\n")

	riskDescriptions := map[string]string{
		"保守": "注重资金安全，低风险低收益",
		"标准": "平衡风险和收益，适合大多数用户",
		"激进": "追求高收益，承担较高风险",
	}

	for i, risk := range riskLevels {
		message.WriteString(fmt.Sprintf("%d. %s - %s\n", i+1, risk, riskDescriptions[risk]))
	}

	message.WriteString(fmt.Sprintf("\n请回复数字选择 (1-%d)：", len(riskLevels)))

	return message.String()
}

func (cw *ConfigWizard) getLeverageMessage() string {
	var message strings.Builder

	message.WriteString("⚙️ **创建 AI 交易员 - 第4步**\n\n")
	message.WriteString("🎚️ **设置杠杆倍数**\n\n")
	message.WriteString("格式: BTC/ETH杠杆,山寨币杠杆\n")
	message.WriteString("例如: 5,3 (BTC/ETH 5倍, 山寨币 3倍)\n\n")
	message.WriteString("建议范围:\n")
	message.WriteString("• BTC/ETH: 1-20倍\n")
	message.WriteString("• 山寨币: 1-10倍\n\n")
	message.WriteString("请输入杠杆倍数 (如: 5,3) 或输入 'default' 使用默认值：")

	return message.String()
}

func (cw *ConfigWizard) getIntervalMessage() string {
	intervalOptions := GetIntervalOptions()
	var message strings.Builder

	message.WriteString("⏱️ **创建 AI 交易员 - 第5步**\n\n")
	message.WriteString("🕐 **设置扫描间隔**\n\n")

	for i, interval := range intervalOptions {
		message.WriteString(fmt.Sprintf("%d. %d 分钟", i+1, interval))
		if i == 0 {
			message.WriteString(" (高频)")
		} else if i == len(intervalOptions)-1 {
			message.WriteString(" (低频)")
		}
		message.WriteString("\n")
	}

	message.WriteString(fmt.Sprintf("%d. 自定义间隔\n\n", len(intervalOptions)+1))
	message.WriteString("请选择扫描间隔 (1-60 分钟)：")

	return message.String()
}

func (cw *ConfigWizard) getConfirmMessage(telegramID int64) string {
	session := cw.ttm.GetSessionManager().GetOrCreateSession(telegramID)
	config := session.TraderConfig

	var message strings.Builder

	message.WriteString("**创建 AI 交易员 - 确认配置**\n\n")
	message.WriteString("**配置摘要：**\n\n")
	message.WriteString(fmt.Sprintf("交易策略: %s\n", cw.getPromptDisplayNameClean(config.PromptTemplate)))
	message.WriteString(fmt.Sprintf("初始资金: %.0f USDC\n", config.InitialBalance))
	message.WriteString(fmt.Sprintf("风险级别: %s\n", config.RiskLevel))
	message.WriteString(fmt.Sprintf("BTC/ETH杠杆: %d倍\n", config.BTCETHLeverage))
	message.WriteString(fmt.Sprintf("山寨币杠杆: %d倍\n", config.AltcoinLeverage))
	message.WriteString(fmt.Sprintf("扫描间隔: %d 分钟\n", config.ScanIntervalMinutes))

	message.WriteString("\n确认创建交易员吗？ (y/n)")

	return message.String()
}

// getPromptDisplayName 获取提示词显示名称
func (cw *ConfigWizard) getPromptDisplayName(templateName string) string {
	templates := GetAvailablePromptTemplates()
	for _, template := range templates {
		if template.Name == templateName {
			return template.DisplayName
		}
	}
	return templateName
}

// getPromptDisplayNameClean 获取不含emoji的提示词显示名称
func (cw *ConfigWizard) getPromptDisplayNameClean(templateName string) string {
	templates := GetAvailablePromptTemplates()
	for _, template := range templates {
		if template.Name == templateName {
			// 直接返回纯文字名称，避免任何特殊字符
			switch template.Name {
			case "default":
				return "默认策略"
			case "Hansen":
				return "激进策略"
			case "nof1":
				return "保守策略"
			case "taro_long_prompts":
				return "高级策略"
			default:
				return template.Name
			}
		}
	}
	return templateName
}

// getLeverageByRiskLevel 根据风险级别获取杠杆倍数 (复用方法)
func (cw *ConfigWizard) getLeverageByRiskLevel(riskLevel string) (int, int) {
	return cw.ttm.getLeverageByRiskLevel(riskLevel)
}

// getQuickSetupMessage 获取快速配置的消息
func (cw *ConfigWizard) getQuickSetupMessage() string {
	return `🤖 创建AI交易员 - 选择您的交易风格：

1. 🛡️ 保守策略（新手推荐）
   • 资金：1000 USDC • 杠杆：BTC/ETH 2x，山寨币 1x • 扫描：15分钟
   • 特点：资金安全第一，低风险低收益

2. 🔄 默认策略（平衡选择）
   • 资金：1000 USDC • 杠杆：BTC/ETH 5x，山寨币 3x • 扫描：5分钟
   • 特点：平衡风险和收益，适合大多数用户

3. 🔥 激进策略（高级用户）
   • 资金：500 USDC  • 杠杆：BTC/ETH 8x，山寨币 5x • 扫描：3分钟
   • 特点：追求高收益，承担较高风险

请选择 1-3：`
}

// processQuickSetupInput 处理快速配置输入
func (cw *ConfigWizard) processQuickSetupInput(telegramID int64, input string) (string, bool, error) {
	session := cw.ttm.GetSessionManager().GetOrCreateSession(telegramID)

	choice, err := cw.ttm.ParseIntInput(input)
	if err != nil {
		return cw.getQuickSetupMessage() + "\n\n❌ 请输入有效的数字 (1-3)", false, nil
	}

	if choice < 1 || choice > 3 {
		return cw.getQuickSetupMessage() + "\n\n❌ 请选择有效的选项 (1-3)", false, nil
	}

	// 根据选择设置智能默认配置
	switch choice {
	case 1: // 保守策略
		session.TraderConfig.PromptTemplate = "nof1"           // 保守策略
		session.TraderConfig.InitialBalance = 1000.0
		session.TraderConfig.RiskLevel = "保守"
		session.TraderConfig.BTCETHLeverage = 2
		session.TraderConfig.AltcoinLeverage = 1
		session.TraderConfig.ScanIntervalMinutes = 15
	case 2: // 默认策略
		session.TraderConfig.PromptTemplate = "default"        // 默认策略
		session.TraderConfig.InitialBalance = 1000.0
		session.TraderConfig.RiskLevel = "标准"
		session.TraderConfig.BTCETHLeverage = 5
		session.TraderConfig.AltcoinLeverage = 3
		session.TraderConfig.ScanIntervalMinutes = 5
	case 3: // 激进策略
		session.TraderConfig.PromptTemplate = "Hansen"         // 激进策略
		session.TraderConfig.InitialBalance = 500.0
		session.TraderConfig.RiskLevel = "激进"
		session.TraderConfig.BTCETHLeverage = 8
		session.TraderConfig.AltcoinLeverage = 5
		session.TraderConfig.ScanIntervalMinutes = 3
	}

	session.State = StateConfirm
	cw.ttm.GetSessionManager().UpdateTraderConfig(telegramID, session.TraderConfig)

	return cw.getConfirmMessage(telegramID), false, nil
}