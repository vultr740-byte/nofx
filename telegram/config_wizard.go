package telegram

import (
	"fmt"
	"html"
	"log"
	"strconv"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// ConfigWizard 配置向导
type ConfigWizard struct {
	ttm      *TelegramTraderManager
	tgBotMgr interface{} // TelegramBotManager引用，用于删除消息
}

// NewConfigWizard 创建配置向导
func NewConfigWizard(ttm *TelegramTraderManager) *ConfigWizard {
	return &ConfigWizard{
		ttm: ttm,
	}
}

// SetTelegramBotManager 设置TelegramBotManager引用
func (cw *ConfigWizard) SetTelegramBotManager(tgBotMgr interface{}) {
	cw.tgBotMgr = tgBotMgr
}

// StartWizard 开始配置向导（简化版 - 直接进入快速配置）
func (cw *ConfigWizard) StartWizard(telegramID int64) (string, error) {
	log.Printf("🔍 StartWizard: Starting wizard for telegramID=%d", telegramID)
	session := cw.ttm.GetSessionManager().GetOrCreateSession(telegramID)
	log.Printf("🔍 StartWizard: Retrieved session, current state=%s", session.State)
	session.State = StateQuickSetup // 直接进入快速配置模式
	session.TraderConfig = &TraderConfig{
		AIProvider: "deepseek",
	}
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
	case StateChoosingAIModel:
		log.Printf("🔍 Processing AI provider selection for telegramID=%d", telegramID)
		return cw.processAIProviderSelection(telegramID, input)
	case StateChoosingPrompt:
		log.Printf("🔍 Processing prompt template selection for telegramID=%d", telegramID)
		return cw.processPromptTemplateSelection(telegramID, input)
	case StateSettingAPIKey:
		log.Printf("🔍 Processing API KEY input for telegramID=%d", telegramID)
		return cw.processAPIKeyInput(telegramID, input)
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
	_ = input
	return cw.getPromptTemplateMessage() + "\n\n❌ 请点击下方按钮选择交易策略", false, nil
}

// processPromptTemplateSelectionByName 处理提示词模板选择（按钮）
func (cw *ConfigWizard) processPromptTemplateSelectionByName(telegramID int64, templateName string) (string, error) {
	session := cw.ttm.GetSessionManager().GetOrCreateSession(telegramID)
	if session.TraderConfig == nil {
		session.TraderConfig = &TraderConfig{}
	}

	templates := GetAvailablePromptTemplates()
	var selected *PromptTemplate
	for i := range templates {
		if templates[i].Name == templateName {
			selected = &templates[i]
			break
		}
	}
	if selected == nil {
		return "", fmt.Errorf("未找到该交易策略")
	}

	switch session.State {
	case StateQuickSetup:
		session.TraderConfig.PromptTemplate = selected.Name
		session.TraderConfig.RiskLevel = selected.RiskLevel
		session.TraderConfig.BTCETHLeverage = selected.BTCETHLeverage
		session.TraderConfig.AltcoinLeverage = selected.AltcoinLeverage
		session.TraderConfig.ScanIntervalMinutes = selected.ScanIntervalMinutes
		session.State = StateChoosingAIModel
		cw.ttm.GetSessionManager().UpdateTraderConfig(telegramID, session.TraderConfig)
		return cw.getAIProviderMessage(), nil
	case StateChoosingPrompt:
		session.TraderConfig.PromptTemplate = selected.Name
		session.State = StateSettingBalance
		cw.ttm.GetSessionManager().UpdateTraderConfig(telegramID, session.TraderConfig)
		return cw.getBalanceMessage(), nil
	default:
		return "", fmt.Errorf("当前不在策略选择步骤")
	}
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

// processAPIKeyInput 处理 API KEY 输入
func (cw *ConfigWizard) processAPIKeyInput(telegramID int64, input string) (string, bool, error) {
	session := cw.ttm.GetSessionManager().GetOrCreateSession(telegramID)
	provider := normalizeAIProvider(session.TraderConfig.AIProvider)

	if !cw.validateAPIKeyForProvider(provider, input) {
		return cw.getAPIKeyMessage(provider) + "\n\n" + cw.getAPIKeyFormatHint(provider), false, nil
	}

	// API KEY 验证成功，删除用户的消息以增强安全性
	if session.LastMessageID != 0 && cw.tgBotMgr != nil {
		if tgBotMgr, ok := cw.tgBotMgr.(*TelegramBotManager); ok {
			deleteConfig := tgbotapi.NewDeleteMessage(telegramID, session.LastMessageID)
			if _, err := tgBotMgr.bot.Request(deleteConfig); err != nil {
				log.Printf("⚠️ 删除API KEY消息失败 (ChatID: %d, MessageID: %d): %v", telegramID, session.LastMessageID, err)
			} else {
				log.Printf("✅ 成功删除API KEY消息 (ChatID: %d, MessageID: %d)", telegramID, session.LastMessageID)
			}
		}
	}

	// 保存 API KEY
	session.TraderConfig.AIProvider = provider
	session.TraderConfig.AIModelAPIKey = input
	session.State = StateConfirm
	cw.ttm.GetSessionManager().UpdateTraderConfig(telegramID, session.TraderConfig)

	return cw.getConfirmMessage(telegramID), false, nil
}

// getAPIKeyMessage 获取 API KEY 输入消息
func (cw *ConfigWizard) getAPIKeyMessage(provider string) string {
	switch normalizeAIProvider(provider) {
	case "qwen":
		return `🔐 创建 AI 交易员 - 输入 Qwen API KEY

🔗 获取 Qwen API KEY：
1. 访问 https://dashscope.aliyun.com/api-console
2. 登录阿里云账号，在「API-KEY 管理」中创建新的 Key
3. 复制完整的 AccessKey（区分大小写）

请直接粘贴您的 Qwen API KEY：`
	default:
		return `🔐 创建 AI 交易员 - 输入 DeepSeek API KEY

🔗 获取 DeepSeek API KEY：
1. 访问 https://platform.deepseek.com/api_keys
2. 登录并创建新的 API KEY
3. 复制完整的 API KEY

请直接粘贴您的 DeepSeek API KEY：`
	}
}

func (cw *ConfigWizard) getAPIKeyFormatHint(provider string) string {
	switch normalizeAIProvider(provider) {
	case "qwen":
		return "❌ API KEY 格式错误！\n• 请输入阿里云 DashScope 创建的 AccessKey\n• 至少 15 个字符，可包含字母、数字和符号"
	default:
		return "❌ API KEY 格式错误！\n• 请确认复制了完整的 DeepSeek API KEY\n• 支持任意字母、数字或符号组合"
	}
}

func (cw *ConfigWizard) validateAPIKeyForProvider(provider, apiKey string) bool {
	switch normalizeAIProvider(provider) {
	case "qwen":
		return len(apiKey) >= 15
	default:
		return cw.validateDeepSeekAPIKey(apiKey)
	}
}

// validateDeepSeekAPIKey 验证 DeepSeek API KEY 格式
func (cw *ConfigWizard) validateDeepSeekAPIKey(apiKey string) bool {
	trimmed := strings.TrimSpace(apiKey)
	return len(trimmed) >= 8
}

// maskAPIKey 隐藏 API KEY 的敏感部分用于显示
func (cw *ConfigWizard) maskAPIKey(apiKey string) string {
	if len(apiKey) <= 10 {
		return apiKey
	}

	// 显示前3位和后4位，中间用****替代
	prefix := apiKey[:3]
	suffix := apiKey[len(apiKey)-4:]
	return prefix + "****" + suffix
}

// processConfirmation 处理确认
func (cw *ConfigWizard) processConfirmation(telegramID int64, input string) (string, bool, error) {
	_ = input
	return cw.getConfirmMessage(telegramID) + "\n\n❌ 请点击下方按钮确认或取消", false, nil
}

// processConfirmationByChoice 处理确认选择（按钮）
func (cw *ConfigWizard) processConfirmationByChoice(telegramID int64, confirm bool) (string, bool, error) {
	session := cw.ttm.GetSessionManager().GetOrCreateSession(telegramID)

	if confirm {
		traderRecord, err := cw.ttm.CreateTrader(telegramID, session.TraderConfig)
		if err != nil {
			cw.ttm.GetSessionManager().ClearSession(telegramID)
			return fmt.Sprintf("❌ 创建交易员失败: %v", err), true, nil
		}

		log.Printf("✅ 成功创建交易员: %s, 钱包: %s, 私钥: %s",
			traderRecord.Name, traderRecord.WalletAddress, maskPrivateKey(traderRecord.PrivateKey))

		cw.ttm.GetSessionManager().ClearSession(telegramID)
		return fmt.Sprintf(`✅ 交易员创建成功！

📋 配置信息:
• 交易员名称: %s
• 交易策略: %s
• 钱包地址: <code>%s</code>

🔐 安全说明:
• 私钥已安全加密存储
• 系统将自动使用私钥进行交易

💡 下一步:
使用 /start_trader 启动交易员
使用 /balance 查看账户余额`,
			html.EscapeString(traderRecord.Name),
			html.EscapeString(cw.getPromptDisplayNameClean(session.TraderConfig.PromptTemplate)),
			html.EscapeString(traderRecord.WalletAddress)), true, nil
	}

	cw.ttm.GetSessionManager().ClearSession(telegramID)
	return "❌ 配置已取消", true, nil
}

// buildConfirmInlineKeyboard 构建确认按钮
func (cw *ConfigWizard) buildConfirmInlineKeyboard(telegramID int64) tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ 确认创建", fmt.Sprintf("confirm_trader|%d|yes", telegramID)),
			tgbotapi.NewInlineKeyboardButtonData("❌ 取消", fmt.Sprintf("confirm_trader|%d|no", telegramID)),
		),
	)
}

// 消息生成方法
func (cw *ConfigWizard) getPromptTemplateMessage() string {
	templates := GetAvailablePromptTemplates()
	var message strings.Builder

	message.WriteString("🤖 <b>创建 AI 交易员 - 第1步</b>\n\n")
	message.WriteString("🎯 <b>选择交易策略</b>\n\n")

	for _, template := range templates {
		displayName := template.DisplayName
		if template.PlainName != "" {
			displayName = template.PlainName
		}
		message.WriteString(fmt.Sprintf("<b>%s</b>\n%s\n\n", html.EscapeString(displayName), html.EscapeString(template.Description)))
	}

	message.WriteString("请点击下方按钮选择交易策略：")
	message.WriteString("\n\n💡 *输入 'cancel' 可随时取消配置*")

	return message.String()
}

func (cw *ConfigWizard) getBalanceMessage() string {
	balanceOptions := GetBalanceOptions()
	var message strings.Builder

	message.WriteString("💰 <b>创建 AI 交易员 - 第2步</b>\n\n")
	message.WriteString("💵 <b>设置初始资金</b>\n\n")

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

	message.WriteString("⚖️ <b>创建 AI 交易员 - 第3步</b>\n\n")
	message.WriteString("🎲 <b>选择风险级别</b>\n\n")

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

	message.WriteString("⚙️ <b>创建 AI 交易员 - 第4步</b>\n\n")
	message.WriteString("🎚️ <b>设置杠杆倍数</b>\n\n")
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

	message.WriteString("⏱️ <b>创建 AI 交易员 - 第5步</b>\n\n")
	message.WriteString("🕐 <b>设置扫描间隔</b>\n\n")

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
	providerName := aiProviderDisplayName(config.AIProvider)

	var message strings.Builder
	message.WriteString("🤖 创建AI交易员 - 确认配置\n\n")

	// 删除"配置摘要"标题，直接显示配置项
	message.WriteString(fmt.Sprintf("交易策略: %s\n", cw.getPromptDisplayNameClean(config.PromptTemplate)))
	message.WriteString(fmt.Sprintf("AI 模型: %s\n", providerName))
	// 删除初始资金行
	message.WriteString(fmt.Sprintf("风险级别: %s\n", config.RiskLevel))
	message.WriteString(fmt.Sprintf("BTC/ETH杠杆: %d倍\n", config.BTCETHLeverage))
	message.WriteString(fmt.Sprintf("山寨币杠杆: %d倍\n", config.AltcoinLeverage))
	message.WriteString(fmt.Sprintf("决策周期: %d分钟\n", config.ScanIntervalMinutes))

	// API KEY隐藏显示
	if config.AIModelAPIKey != "" {
		message.WriteString(fmt.Sprintf("%s API KEY: %s\n", providerName, cw.maskAPIKey(config.AIModelAPIKey)))
	}

	message.WriteString("\n请点击下方按钮确认创建交易员：")

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
			if template.PlainName != "" {
				return template.PlainName
			}
			return template.DisplayName
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
	templates := GetAvailablePromptTemplates()
	var builder strings.Builder
	builder.WriteString("🤖 选择交易策略：\n\n")

	for _, template := range templates {
		displayName := template.DisplayName
		if template.PlainName != "" {
			displayName = template.PlainName
		}
		builder.WriteString(fmt.Sprintf("<b>%s</b>\n", html.EscapeString(displayName)))
		builder.WriteString(fmt.Sprintf("   • 杠杆：BTC/ETH %dx，山寨币 %dx\n", template.BTCETHLeverage, template.AltcoinLeverage))
		builder.WriteString(fmt.Sprintf("   • 决策周期：%d分钟\n", template.ScanIntervalMinutes))
		if template.Description != "" {
			builder.WriteString(fmt.Sprintf("   • 特点：%s\n", html.EscapeString(template.Description)))
		}
		builder.WriteString(fmt.Sprintf("   • Prompt: %s\n\n", template.Name))
	}

	builder.WriteString("请点击下方按钮选择：")
	return builder.String()
}

// buildPromptTemplateInlineKeyboard 构建策略选择按钮
func (cw *ConfigWizard) buildPromptTemplateInlineKeyboard(telegramID int64) tgbotapi.InlineKeyboardMarkup {
	templates := GetAvailablePromptTemplates()
	rows := make([][]tgbotapi.InlineKeyboardButton, 0, len(templates))

	for _, template := range templates {
		displayName := template.DisplayName
		if template.PlainName != "" {
			displayName = template.PlainName
		}
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(
				displayName,
				fmt.Sprintf("prompt_select|%d|%s", telegramID, template.Name),
			),
		))
	}

	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

func (cw *ConfigWizard) getAIProviderMessage() string {
	return `🧠 创建 AI 交易员 - 选择大模型

请选择要使用的 AI 提供商：
DeepSeek（默认，性价比高，推理速度快）
Qwen / 通义千问（由阿里云 DashScope 提供）

请点击下方按钮选择：`
}

func (cw *ConfigWizard) processAIProviderSelection(telegramID int64, input string) (string, bool, error) {
	_ = input
	return cw.getAIProviderMessage() + "\n\n❌ 请点击下方按钮选择 AI 提供商", false, nil
}

// processQuickSetupInput 处理快速配置输入
func (cw *ConfigWizard) processQuickSetupInput(telegramID int64, input string) (string, bool, error) {
	_ = input
	return cw.getQuickSetupMessage() + "\n\n❌ 请点击下方按钮选择交易策略", false, nil
}

func detectAIProviderFromInput(input string) (string, bool) {
	normalized := strings.ToLower(strings.TrimSpace(input))
	switch normalized {
	case "1":
		return "deepseek", true
	case "2":
		return "qwen", true
	}

	// 去掉常见的符号和空格，方便匹配
	normalized = strings.ReplaceAll(normalized, "（", "(")
	normalized = strings.ReplaceAll(normalized, "）", ")")
	normalized = strings.ReplaceAll(normalized, " ", "")

	if strings.Contains(normalized, "deepseek") {
		return "deepseek", true
	}
	if strings.Contains(normalized, "qwen") || strings.Contains(normalized, "通义") || strings.Contains(normalized, "tongyi") {
		return "qwen", true
	}

	return "", false
}

// processAIProviderSelectionByName 处理AI提供商选择（按钮）
func (cw *ConfigWizard) processAIProviderSelectionByName(telegramID int64, provider string) (string, error) {
	session := cw.ttm.GetSessionManager().GetOrCreateSession(telegramID)
	if session.TraderConfig == nil {
		session.TraderConfig = &TraderConfig{}
	}

	normalized := normalizeAIProvider(provider)
	if normalized != "deepseek" && normalized != "qwen" {
		return "", fmt.Errorf("无效的 AI 提供商")
	}

	session.TraderConfig.AIProvider = normalized
	session.State = StateSettingAPIKey
	cw.ttm.GetSessionManager().UpdateTraderConfig(telegramID, session.TraderConfig)

	return cw.getAPIKeyMessage(normalized), nil
}

// buildAIProviderInlineKeyboard 构建AI提供商选择按钮
func (cw *ConfigWizard) buildAIProviderInlineKeyboard(telegramID int64) tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("DeepSeek", fmt.Sprintf("ai_provider|%d|deepseek", telegramID)),
			tgbotapi.NewInlineKeyboardButtonData("Qwen / 通义千问", fmt.Sprintf("ai_provider|%d|qwen", telegramID)),
		),
	)
}
