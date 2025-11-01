import { Bot, Context } from 'grammy';
import { ApiClient } from '../api/client';
import { TraderCreationHandler } from './trader-creation';
import { AIModelCreationHandler } from './ai-model-creation';
import { stateManager } from '../utils/state-manager';

export class MessageHandler {
  private traderCreationHandler: TraderCreationHandler;
  private aiModelCreationHandler: AIModelCreationHandler;

  constructor(private apiClient: ApiClient) {
    this.traderCreationHandler = new TraderCreationHandler(apiClient);
    this.aiModelCreationHandler = new AIModelCreationHandler(apiClient);
  }

  // 处理文本消息
  async handleTextMessage(ctx: Context): Promise<void> {
    const user = ctx.from;
    if (!user || !ctx.message || !ctx.message.text) return;

    const text = ctx.message.text.trim();
    const userState = stateManager.getState(user.id);

    // 如果用户正在创建流程中
    if (userState) {
      switch (userState.action) {
        // AI模型创建流程
        case 'enter_model_name':
          await this.aiModelCreationHandler.handleModelName(ctx, text);
          return;

        case 'enter_api_key':
          await this.aiModelCreationHandler.handleAPIKeyInput(ctx, text);
          return;

        case 'enter_description':
          await this.aiModelCreationHandler.handleDescriptionInput(ctx, text);
          return;

        // 交易员创建流程
        case 'enter_trader_name':
          await this.traderCreationHandler.handleTraderName(ctx, text);
          return;

        case 'enter_initial_balance':
          // 处理自定义金额输入
          if (!isNaN(parseFloat(text)) && parseFloat(text) > 0) {
            await this.traderCreationHandler.handleInitialBalance(ctx, parseFloat(text));
          } else {
            await ctx.reply('❌ 请输入有效的金额（数字）。例如：100、500、1000');
          }
          return;

        default:
          // 在其他状态下，忽略文本消息或提示
          await ctx.reply('💡 请使用下方按钮进行操作，或使用 /cancel 取消当前流程。');
          return;
      }
    }

    // 处理命令（如果没有在创建流程中）
    if (text.startsWith('/')) {
      // 命令已经由命令处理器处理，这里不需要处理
      return;
    }

    // 普通文本消息，提供帮助
    await ctx.reply('👋 您好！我是 NOFX AI 交易助手。\n\n' +
      '🚀 **快速开始：**\n' +
      '1. 创建AI模型\n' +
      '2. 添加交易所账户\n' +
      '3. 创建交易员\n\n' +
      '💡 **我可以帮助您：**\n' +
      '• 🤖 创建AI模型 - 配置您的AI交易策略\n' +
      '• 🏦 添加交易所 - 连接您的交易所账户\n' +
      '• 📈 创建交易员 - 开始AI自动交易\n' +
      '• 📊 查看状态 - 监控交易员表现\n' +
      '• 📋 查看列表 - 管理所有交易员\n\n' +
      '请选择下方操作开始：', {
        reply_markup: {
          inline_keyboard: [
            [
              { text: "🤖 创建AI模型", callback_data: "create_ai_model" },
              { text: "🏦 添加交易所", callback_data: "create_exchange" }
            ],
            [
              { text: "📈 创建交易员", callback_data: "create_trader" },
              { text: "📊 查看状态", callback_data: "refresh_status" }
            ],
            [
              { text: "📋 交易员列表", callback_data: "list_traders" },
              { text: "📖 帮助", callback_data: "help" }
            ]
          ]
        }
      });
  }

  // 处理回调查询
  async handleCallbackQuery(ctx: Context): Promise<void> {
    const user = ctx.from;
    if (!user || !ctx.callbackQuery || !ctx.callbackQuery.data) return;

    const callbackData = ctx.callbackQuery.data;

    // 回答回调查询以移除加载状态
    await ctx.answerCallbackQuery();

    console.log(`🎯 Callback query: ${callbackData} from user ${user.id}`);

    // 处理AI模型创建流程的回调（最高优先级）
    if (callbackData.startsWith('select_provider_')) {
      const provider = callbackData.replace('select_provider_', '');
      await this.aiModelCreationHandler.handleProviderSelection(ctx, provider);
      return;
    }

    if (callbackData === 'skip_api_key') {
      await this.aiModelCreationHandler.skipAPIKey(ctx);
      return;
    }

    if (callbackData === 'skip_description') {
      await this.aiModelCreationHandler.skipDescription(ctx);
      return;
    }

    if (callbackData === 'confirm_create_model') {
      await this.aiModelCreationHandler.confirmCreate(ctx);
      return;
    }

    if (callbackData === 'cancel_create_model') {
      await this.aiModelCreationHandler.cancelCreation(ctx);
      return;
    }

    // 处理演示AI模型选择（优先级更高）
    if (callbackData.startsWith('select_model_demo_')) {
      const demoModel = callbackData.replace('select_model_demo_', '');
      await this.traderCreationHandler.handleDemoAIModelSelection(ctx, demoModel);
      return;
    }

    // 处理创建交易员相关的回调（真实API）
    if (callbackData.startsWith('select_model_')) {
      const modelId = callbackData.replace('select_model_', '');
      await this.traderCreationHandler.handleAIModelSelection(ctx, modelId);
      return;
    }

    // 处理演示交易所选择（优先级更高）
    if (callbackData.startsWith('select_exchange_demo_')) {
      const demoExchange = callbackData.replace('select_exchange_demo_', '');
      await this.traderCreationHandler.handleDemoExchangeSelection(ctx, demoExchange);
      return;
    }

    // 处理创建交易员相关的回调（真实API）
    if (callbackData.startsWith('select_exchange_')) {
      const exchangeId = callbackData.replace('select_exchange_', '');
      await this.traderCreationHandler.handleExchangeSelection(ctx, exchangeId);
      return;
    }

    if (callbackData.startsWith('set_balance_')) {
      const balance = parseInt(callbackData.replace('set_balance_', ''));
      await this.traderCreationHandler.handleInitialBalance(ctx, balance);
      return;
    }

    if (callbackData === 'custom_balance') {
      await ctx.reply('💰 请输入自定义的初始资金金额（USDT）：\n\n' +
        '💡 建议金额：\n' +
        '• 测试：10-100 USDT\n' +
        '• 小额：100-500 USDT\n' +
        '• 正式：1000+ USDT');
      return;
    }

    if (callbackData === 'confirm_create_trader') {
      await this.traderCreationHandler.confirmCreate(ctx);
      return;
    }

    if (callbackData === 'cancel_create_trader') {
      await this.traderCreationHandler.cancelCreation(ctx);
      return;
    }

    // 处理其他通用回调
    switch (callbackData) {
      case 'create_ai_model':
        await this.handleCreateAIModel(ctx);
        break;

      case 'create_exchange':
        await this.handleCreateExchange(ctx);
        break;

      case 'refresh_status':
        const { handleStatus } = await import('../commands/status');
        await handleStatus(ctx, this.apiClient);
        break;

      case 'list_traders':
        const { handleListTraders } = await import('../commands/list-traders');
        await handleListTraders(ctx, this.apiClient);
        break;

      case 'list_demo_traders':
        await this.traderCreationHandler.handleDemoTradersList(ctx);
        break;

      case 'create_trader':
        const { handleCreateTrader } = await import('../commands/create-trader');
        await handleCreateTrader(ctx, this.apiClient);
        break;

      case 'help':
        const { handleHelp } = await import('../commands/help');
        await handleHelp(ctx);
        break;

      case 'back_to_home':
        await ctx.reply('👋 您好！我是 NOFX AI 交易助手。\n\n' +
          '🚀 **快速开始：**\n' +
          '1. 创建AI模型\n' +
          '2. 添加交易所账户\n' +
          '3. 创建交易员\n\n' +
          '💡 **我可以帮助您：**\n' +
          '• 🤖 创建AI模型 - 配置您的AI交易策略\n' +
          '• 🏦 添加交易所 - 连接您的交易所账户\n' +
          '• 📈 创建交易员 - 开始AI自动交易\n' +
          '• 📊 查看状态 - 监控交易员表现\n' +
          '• 📋 查看列表 - 管理所有交易员\n\n' +
          '请选择下方操作开始：', {
          reply_markup: {
            inline_keyboard: [
              [
                { text: "🤖 创建AI模型", callback_data: "create_ai_model" },
                { text: "🏦 添加交易所", callback_data: "create_exchange" }
              ],
              [
                { text: "📈 创建交易员", callback_data: "create_trader" },
                { text: "📊 查看状态", callback_data: "refresh_status" }
              ],
              [
                { text: "📋 交易员列表", callback_data: "list_traders" },
                { text: "📖 帮助", callback_data: "help" }
              ]
            ]
          }
        });
        break;

      default:
        await ctx.reply('❌ 未知的操作，请重试。');
        break;
    }
  }

  // 处理取消命令
  async handleCancel(ctx: Context): Promise<void> {
    const user = ctx.from;
    if (!user) return;

    const userState = stateManager.getState(user.id);
    if (!userState) {
      await ctx.reply('ℹ️ 当前没有正在进行的操作。');
      return;
    }

    // 根据不同的流程调用相应的取消方法
    switch (userState.action) {
      case 'enter_model_name':
      case 'select_model_provider':
      case 'enter_api_key':
      case 'enter_description':
        await this.aiModelCreationHandler.cancelCreation(ctx);
        break;
      case 'enter_trader_name':
      case 'select_ai_model':
      case 'select_exchange':
      case 'enter_initial_balance':
        await this.traderCreationHandler.cancelCreation(ctx);
        break;
      default:
        await ctx.reply('ℹ️ 当前没有正在进行的操作。');
        break;
    }
  }

  // 处理创建AI模型
  async handleCreateAIModel(ctx: Context): Promise<void> {
    const user = ctx.from;
    if (!user) return;

    // 清除之前的状态
    stateManager.clearState(user.id);

    // 开始AI模型创建流程
    await ctx.reply('🤖 **创建AI模型**\n\n' +
      'AI模型是交易员的核心决策引擎，负责分析市场并做出交易决策。\n\n' +
      '📝 **请输入AI模型名称：**\n\n' +
      '💡 **命名建议：**\n' +
      '• 使用描述性名称，如"深度分析师"、"量化大师"\n' +
      '• 长度：2-30个字符\n' +
      '• 可包含中文、英文、数字\n\n' +
      '⚠️ **请输入：**',
      {
        reply_markup: {
          inline_keyboard: [
            [
              { text: "❌ 取消", callback_data: "cancel_create_model" }
            ]
          ]
        }
      }
    );

    // 设置用户状态为等待模型名称输入
    stateManager.setState(user.id, 'enter_model_name');
  }

  // 处理创建交易所账户
  async handleCreateExchange(ctx: Context): Promise<void> {
    const user = ctx.from;
    if (!user) return;

    await ctx.reply('🏦 **添加交易所账户**\n\n' +
      '交易所账户用于执行AI模型的交易决策。\n\n' +
      '📋 **支持的交易所：**\n' +
      '• Binance - 全球最大的交易所\n' +
      '• Hyperliquid - 新兴去中心化交易所\n' +
      '• OKX - 综合性交易平台\n' +
      '• dYdX - 专业衍生品交易\n' +
      '• Aster DEX - 去中心化交易\n\n' +
      '🔧 **需要准备：**\n' +
      '• 交易所名称（自定义）\n' +
      '• API Key\n' +
      '• Secret Key\n' +
      '• 测试网/主网选择\n\n' +
      '⚠️ **重要安全提示：**\n' +
      '• 建议先使用测试网进行测试\n' +
      '• API密钥仅需要交易权限，不要开放提现\n' +
      '• 定期更换API密钥\n\n' +
      '⚠️ **注意：** 交易所账户添加功能正在开发中。\n' +
      '目前您可以通过Web界面或联系管理员来添加交易所账户。\n\n' +
      '📞 **需要帮助？**\n' +
      '联系管理员获取API配置指导。\n\n' +
      '💡 **下一步操作：**\n' +
      '添加交易所账户后，您就可以创建交易员开始交易了！', {
        reply_markup: {
          inline_keyboard: [
            [
              { text: "🤖 创建AI模型", callback_data: "create_ai_model" },
              { text: "📊 查看状态", callback_data: "refresh_status" }
            ],
            [
              { text: "📈 创建交易员", callback_data: "create_trader" },
              { text: "📋 交易员列表", callback_data: "list_traders" }
            ],
            [
              { text: "📖 帮助", callback_data: "help" },
              { text: "🔙 返回主页", callback_data: "back_to_home" }
            ]
          ]
        }
      });
  }
}