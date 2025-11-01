import { Bot, Context } from 'grammy';
import { ApiClient } from '../api/client';
import { stateManager } from '../utils/state-manager';
import { TraderCreationData } from '../types/api';

export class TraderCreationHandler {
  constructor(private apiClient: ApiClient) {}

  // 开始创建交易员流程
  async startCreation(ctx: Context): Promise<void> {
    const user = ctx.from;
    if (!user) return;

    // 设置用户状态
    stateManager.setState(user.id, 'enter_trader_name', {});

    await ctx.reply('🚀 **创建新的AI交易员**\n\n' +
      '让我们一步步来创建您的交易员。首先，请给您的交易员起个名字：\n\n' +
      '💡 *命名建议：*\n' +
      '• 使用描述性的名称，如 "BTC趋势交易员" 或 "量化套利器"\n' +
      '• 避免使用特殊字符\n' +
      '• 长度建议在3-20个字符之间\n\n' +
      '请输入交易员名称：',
      {
        parse_mode: 'Markdown',
        reply_markup: {
          inline_keyboard: [
            [
              { text: "❌ 取消", callback_data: "cancel_create_trader" }
            ]
          ]
        }
      }
    );
  }

  // 处理交易员名称输入
  async handleTraderName(ctx: Context, name: string): Promise<void> {
    const user = ctx.from;
    if (!user) return;

    // 验证名称
    if (!name || name.trim().length < 3) {
      await ctx.reply('❌ 交易员名称至少需要3个字符。请重新输入：');
      return;
    }

    if (name.trim().length > 50) {
      await ctx.reply('❌ 交易员名称不能超过50个字符。请重新输入：');
      return;
    }

    // 更新状态
    stateManager.updateState(user.id, { name: name.trim() });
    stateManager.setState(user.id, 'select_ai_model', { name: name.trim() });

    // 获取可用的AI模型
    const modelsResult = await this.apiClient.getAIModels(user.id);

    // 处理不同的API响应格式
    let models = [];
    if (modelsResult.success && modelsResult.data) {
      if (modelsResult.data.models && Array.isArray(modelsResult.data.models)) {
        // 如果data.models是数组 (当前API格式)
        models = modelsResult.data.models;
      } else if (Array.isArray(modelsResult.data)) {
        // 如果data直接是数组 (备用格式)
        models = modelsResult.data;
      }
    }

    if (!modelsResult.success || models.length === 0) {
      // 提供演示用的模拟AI模型
      await ctx.reply('⚠️ 系统中暂无配置的AI模型，为您提供演示选项：\n\n' +
        '🤖 **选择AI模型（演示）**\n\n' +
        `为交易员 "${name.trim()}" 选择一个AI模型：\n\n`);

      const keyboard = [
        [{
          text: "DeepSeek Trader (DeepSeek) - 推荐",
          callback_data: "select_model_demo_deepseek"
        }],
        [{
          text: "Qwen Master (Alibaba) - 新手友好",
          callback_data: "select_model_demo_qwen"
        }],
        [{
          text: "GPT Trader Pro (OpenAI) - 高级用户",
          callback_data: "select_model_demo_gpt"
        }],
        [{ text: "❌ 取消", callback_data: "cancel_create_trader" }]
      ];

      await ctx.reply('📝 *演示说明：*\n' +
        '这些是演示AI模型，用于展示创建流程。\n' +
        '实际使用时，请联系管理员配置真实的AI模型。',
        {
          parse_mode: 'Markdown',
          reply_markup: { inline_keyboard: keyboard }
        }
      );
      return;
    }

    // 显示AI模型选择
    let modelMessage = `🤖 **选择AI模型**\n\n`;
    modelMessage += `为交易员 "${name.trim()}" 选择一个AI模型：\n\n`;

    const keyboard = [];
    for (const model of models) {
      if (model.enabled) {
        keyboard.push([{
          text: `${model.name} (${model.provider})`,
          callback_data: `select_model_${model.id}`
        }]);
      }
    }

    // 添加取消按钮
    keyboard.push([{ text: "❌ 取消", callback_data: "cancel_create_trader" }]);

    await ctx.reply(modelMessage, {
      parse_mode: 'Markdown',
      reply_markup: { inline_keyboard: keyboard }
    });
  }

  // 处理AI模型选择
  async handleAIModelSelection(ctx: Context, modelId: string): Promise<void> {
    const user = ctx.from;
    if (!user) return;

    const currentState = stateManager.getState(user.id);
    if (!currentState || !currentState.data?.name) {
      await ctx.reply('❌ 会话已过期，请重新开始创建流程。');
      stateManager.clearState(user.id);
      return;
    }

    // 获取模型详情
    const modelsResult = await this.apiClient.getAIModels(user.id);

    // 处理不同的API响应格式
    let models = [];
    if (modelsResult.success && modelsResult.data) {
      if (Array.isArray(modelsResult.data)) {
        models = modelsResult.data;
      } else if (modelsResult.data.models && Array.isArray(modelsResult.data.models)) {
        models = modelsResult.data.models;
      }
    }

    if (!modelsResult.success || models.length === 0) {
      await ctx.reply('❌ 无法获取AI模型信息。请稍后再试。');
      return;
    }

    const selectedModel = models.find(m => m.id === modelId);
    if (!selectedModel) {
      await ctx.reply('❌ 选择的AI模型不存在。请重新选择。');
      return;
    }

    // 更新状态
    stateManager.updateState(user.id, { ai_model_id: modelId });
    stateManager.setState(user.id, 'select_exchange', {
      ...currentState.data,
      ai_model_id: modelId
    });

    // 获取可用的交易所
    const exchangesResult = await this.apiClient.getExchanges(user.id);

    // 处理不同的API响应格式
    let exchanges = [];
    if (exchangesResult.success && exchangesResult.data) {
      if (Array.isArray(exchangesResult.data)) {
        exchanges = exchangesResult.data;
      } else if (exchangesResult.data.exchanges && Array.isArray(exchangesResult.data.exchanges)) {
        exchanges = exchangesResult.data.exchanges;
      }
    }

    if (!exchangesResult.success || exchanges.length === 0) {
      await ctx.reply('❌ 无法获取可用的交易所。请稍后再试，或联系管理员。\n\n' +
        '可能的原因：\n' +
        '• 系统中还没有配置交易所\n' +
        '• 所有交易所都已禁用\n' +
        '• 网络连接问题\n\n' +
        '请联系管理员添加交易所配置。');
      stateManager.clearState(user.id);
      return;
    }

    // 显示交易所选择
    let exchangeMessage = `🏦 **选择交易所**\n\n`;
    exchangeMessage += `为交易员 "${currentState.data.name}" 选择交易所：\n\n`;
    exchangeMessage += `已选择AI模型：${selectedModel.name} (${selectedModel.provider})\n\n`;

    const keyboard = [];
    for (const exchange of exchanges) {
      if (exchange.enabled) {
        const testnetBadge = exchange.testnet ? ' (测试网)' : '';
        keyboard.push([{
          text: `${exchange.name}${testnetBadge}`,
          callback_data: `select_exchange_${exchange.id}`
        }]);
      }
    }

    // 添加取消按钮
    keyboard.push([{ text: "❌ 取消", callback_data: "cancel_create_trader" }]);

    await ctx.reply(exchangeMessage, {
      parse_mode: 'Markdown',
      reply_markup: { inline_keyboard: keyboard }
    });
  }

  // 处理交易所选择
  async handleExchangeSelection(ctx: Context, exchangeId: string): Promise<void> {
    const user = ctx.from;
    if (!user) return;

    const currentState = stateManager.getState(user.id);
    if (!currentState) {
      await ctx.reply('❌ 会话已过期，请重新开始创建流程。');
      stateManager.clearState(user.id);
      return;
    }

    // 获取交易所详情
    const exchangesResult = await this.apiClient.getExchanges(user.id);

    // 处理不同的API响应格式
    let exchanges = [];
    if (exchangesResult.success && exchangesResult.data) {
      if (Array.isArray(exchangesResult.data)) {
        exchanges = exchangesResult.data;
      } else if (exchangesResult.data.exchanges && Array.isArray(exchangesResult.data.exchanges)) {
        exchanges = exchangesResult.data.exchanges;
      }
    }

    if (!exchangesResult.success || exchanges.length === 0) {
      await ctx.reply('❌ 无法获取交易所信息。请稍后再试。');
      return;
    }

    const selectedExchange = exchanges.find(e => e.id === exchangeId);
    if (!selectedExchange) {
      await ctx.reply('❌ 选择的交易所不存在。请重新选择。');
      return;
    }

    // 更新状态
    stateManager.updateState(user.id, { exchange_id: exchangeId });
    stateManager.setState(user.id, 'enter_initial_balance', {
      ...currentState.data,
      exchange_id: exchangeId
    });

    // 询问初始资金
    let balanceMessage = `💰 **设置初始资金**\n\n`;
    balanceMessage += `请输入交易员的初始资金（USDT）：\n\n`;
    balanceMessage += `📊 **当前配置：**\n`;
    balanceMessage += `• 交易员名称：${currentState.data?.name}\n`;
    balanceMessage += `• AI模型：已选择\n`;
    balanceMessage += `• 交易所：${selectedExchange.name}${selectedExchange.testnet ? ' (测试网)' : ''}\n\n`;
    balanceMessage += `💡 *建议：*\n`;
    balanceMessage += `• 测试建议：10-100 USDT\n`;
    balanceMessage += `• 小额尝试：100-500 USDT\n`;
    balanceMessage += `• 正式交易：1000+ USDT\n\n`;
    balanceMessage += `请输入初始资金金额（数字）：`;

    await ctx.reply(balanceMessage, {
      parse_mode: 'Markdown',
      reply_markup: {
        inline_keyboard: [
          [
            { text: "100 USDT", callback_data: "set_balance_100" },
            { text: "500 USDT", callback_data: "set_balance_500" }
          ],
          [
            { text: "1000 USDT", callback_data: "set_balance_1000" },
            { text: "自定义金额", callback_data: "custom_balance" }
          ],
          [
            { text: "❌ 取消", callback_data: "cancel_create_trader" }
          ]
        ]
      }
    });
  }

  // 处理初始资金设置
  async handleInitialBalance(ctx: Context, balance: number | string): Promise<void> {
    const user = ctx.from;
    if (!user) return;

    let balanceAmount: number;

    if (typeof balance === 'string') {
      balanceAmount = parseFloat(balance);
      if (isNaN(balanceAmount) || balanceAmount <= 0) {
        await ctx.reply('❌ 请输入有效的金额（大于0的数字）。请重新输入：');
        return;
      }
    } else {
      balanceAmount = balance;
    }

    if (balanceAmount < 10) {
      await ctx.reply('❌ 初始资金不能少于10 USDT。请重新输入：');
      return;
    }

    if (balanceAmount > 100000) {
      await ctx.reply('⚠️ 大额交易风险较高，建议您：\n• 先用小额资金测试\n• 分批投入资金\n• 充分了解AI交易策略\n\n如仍要继续，请重新输入金额：');
      return;
    }

    const currentState = stateManager.getState(user.id);
    if (!currentState) {
      await ctx.reply('❌ 会话已过期，请重新开始创建流程。');
      stateManager.clearState(user.id);
      return;
    }

    // 更新状态
    stateManager.updateState(user.id, {
      initial_balance: balanceAmount,
      scan_interval_minutes: 5, // 默认5分钟
      is_cross_margin: true     // 默认全仓
    });
    stateManager.setState(user.id, 'confirm_create', {
      ...currentState.data,
      initial_balance: balanceAmount,
      scan_interval_minutes: 5,
      is_cross_margin: true
    });

    // 显示确认信息
    await this.showConfirmation(ctx, user.id);
  }

  // 显示创建确认
  private async showConfirmation(ctx: Context, userId: number): Promise<void> {
    const currentState = stateManager.getState(userId);
    if (!currentState || !currentState.data) {
      await ctx.reply('❌ 会话已过期，请重新开始创建流程。');
      stateManager.clearState(userId);
      return;
    }

    const data = currentState.data;

    // 获取模型和交易所名称（优先使用演示数据）
    let modelName = data.demo_model_name || '未知模型';
    let modelProvider = data.demo_model_provider || '未知';
    let exchangeName = data.demo_exchange_name || '未知交易所';
    let isTestnet = data.demo_exchange_testnet || false;

    // 如果没有演示数据，尝试从API获取
    if (!data.demo_model_name) {
      const modelsResult = await this.apiClient.getAIModels(userId);
      if (modelsResult.success && modelsResult.data) {
        let models = [];
        if (Array.isArray(modelsResult.data)) {
          models = modelsResult.data;
        } else if (modelsResult.data.models && Array.isArray(modelsResult.data.models)) {
          models = modelsResult.data.models;
        }
        const model = models.find(m => m.id === data.ai_model_id);
        if (model) {
          modelName = model.name;
          modelProvider = model.provider;
        }
      }
    }

    if (!data.demo_exchange_name) {
      const exchangesResult = await this.apiClient.getExchanges(userId);
      if (exchangesResult.success && exchangesResult.data) {
        let exchanges = [];
        if (Array.isArray(exchangesResult.data)) {
          exchanges = exchangesResult.data;
        } else if (exchangesResult.data.exchanges && Array.isArray(exchangesResult.data.exchanges)) {
          exchanges = exchangesResult.data.exchanges;
        }
        const exchange = exchanges.find(e => e.id === data.exchange_id);
        if (exchange) {
          exchangeName = exchange.name;
          isTestnet = exchange.testnet || false;
        }
      }
    }

    let confirmMessage = `✅ **确认创建交易员**\n\n`;
    confirmMessage += `请仔细核对以下信息：\n\n`;
    confirmMessage += `📝 **交易员信息：**\n`;
    confirmMessage += `• 名称：${data.name}\n`;
    confirmMessage += `• AI模型：${modelName} (${modelProvider})\n`;
    confirmMessage += `• 交易所：${exchangeName}${isTestnet ? ' (测试网)' : ''}\n`;
    confirmMessage += `• 初始资金：${data.initial_balance} USDT\n`;
    confirmMessage += `• 扫描间隔：${data.scan_interval_minutes} 分钟\n`;
    confirmMessage += `• 仓位模式：${data.is_cross_margin ? '全仓' : '逐仓'}\n\n`;

    // 如果是演示模式，添加提示
    if (data.demo_model_name && data.demo_exchange_name) {
      confirmMessage += `🎭 **演示模式提示：**\n`;
      confirmMessage += `这是一个演示配置，用于展示创建流程。\n`;
      confirmMessage += `实际交易需要配置真实的AI模型和交易所API。\n\n`;
    }
    confirmMessage += `⚠️ **风险提示：**\n`;
    confirmMessage += `• AI自动交易存在风险，可能导致资金损失\n`;
    confirmMessage += `• 建议先用小额资金测试\n`;
    confirmMessage += `• 请密切监控交易员表现\n\n`;
    confirmMessage += `确认创建这个交易员吗？`;

    await ctx.reply(confirmMessage, {
      parse_mode: 'Markdown',
      reply_markup: {
        inline_keyboard: [
          [
            { text: "✅ 确认创建", callback_data: "confirm_create_trader" },
            { text: "❌ 取消", callback_data: "cancel_create_trader" }
          ]
        ]
      }
    });
  }

  // 确认创建交易员
  async confirmCreate(ctx: Context): Promise<void> {
    const user = ctx.from;
    if (!user) return;

    const currentState = stateManager.getState(user.id);
    if (!currentState || !currentState.data) {
      await ctx.reply('❌ 会话已过期，请重新开始创建流程。');
      stateManager.clearState(user.id);
      return;
    }

    const data = currentState.data as TraderCreationData;

    // 验证必要数据
    if (!data.name || !data.ai_model_id || !data.exchange_id || !data.initial_balance) {
      await ctx.reply('❌ 创建信息不完整，请重新开始。');
      stateManager.clearState(user.id);
      return;
    }

    // 发送创建中消息
    await ctx.reply('🔄 正在创建交易员，请稍候...');

    try {
      // 检查是否是演示模式
      if (data.demo_model_name && data.demo_exchange_name) {
        // 演示模式 - 不实际调用API
        const demoTraderId = `demo_${Date.now()}_${Math.random().toString(36).substr(2, 9)}`;

        // 将演示交易员添加到状态管理器的演示列表中
        const existingDemoTraders = currentState.data?.demo_traders || [];
        const newDemoTrader = {
          trader_id: demoTraderId,
          trader_name: data.name,
          display_name: data.name,
          ai_model: data.demo_model_name,
          exchange_type: data.demo_exchange_name,
          total_equity: data.initial_balance,
          total_pnl: 0,
          total_pnl_pct: 0,
          position_count: 0,
          margin_used_pct: 0,
          is_running: false,
          created_at: new Date().toISOString(),
          is_demo: true
        };
        existingDemoTraders.push(newDemoTrader);
        stateManager.updateState(user.id, { demo_traders: existingDemoTraders });

        await ctx.reply(
          `🎉 **演示交易员创建成功！**\n\n` +
          `✅ 交易员信息：\n` +
          `• ID：\`${demoTraderId}\`\n` +
          `• 名称：${data.name}\n` +
          `• AI模型：${data.demo_model_name} (${data.demo_model_provider})\n` +
          `• 交易所：${data.demo_exchange_name} (测试网)\n` +
          `• 初始资金：${data.initial_balance} USDT\n` +
          `• 状态：🔴 已停止（演示模式）\n\n` +
          `🎭 **演示模式说明：**\n` +
          `这是一个演示交易员，展示了完整的创建流程。\n` +
          `实际交易需要：\n` +
          `• 配置真实的AI模型API\n` +
          `• 配置真实的交易所API密钥\n` +
          `• 完成身份验证和风险评估\n\n` +
          `💡 **下一步操作：**\n` +
          `• /status - 查看交易员状态（演示）\n` +
          `• /list - 查看交易员列表（包含演示交易员）\n` +
          `• /create - 创建真实交易员（需要配置）\n\n` +
          `📋 您的演示交易员：${existingDemoTraders.length} 个`,
          {
            parse_mode: 'Markdown',
            reply_markup: {
              inline_keyboard: [
                [
                  { text: "📊 查看状态", callback_data: "refresh_status" },
                  { text: "📋 查看演示列表", callback_data: "list_demo_traders" }
                ],
                [
                  { text: "🚀 再创建一个", callback_data: "create_trader" },
                  { text: "📖 帮助", callback_data: "help" }
                ]
              ]
            }
          }
        );
      } else {
        // 实际创建模式 - 调用API
        const createResult = await this.apiClient.createTrader(user.id, {
          name: data.name,
          ai_model_id: data.ai_model_id,
          exchange_id: data.exchange_id,
          initial_balance: data.initial_balance,
          scan_interval_minutes: data.scan_interval_minutes || 5,
          is_cross_margin: data.is_cross_margin !== false
        });

        if (createResult.success && createResult.data) {
          await ctx.reply(
            `🎉 **交易员创建成功！**\n\n` +
            `✅ 交易员信息：\n` +
            `• ID：\`${createResult.data.trader_id}\`\n` +
            `• 名称：${createResult.data.display_name || createResult.data.trader_name}\n` +
            `• 状态：🔴 已停止（可以启动）\n\n` +
            `💡 **下一步操作：**\n` +
            `• 使用 /start_trader ${createResult.data.trader_id} 启动交易员\n` +
            `• 使用 /status 查看所有交易员状态\n` +
            `• 使用 /list 查看交易员列表\n\n` +
            `⚠️ 记得在启动前配置好交易所的API密钥！`,
            {
              parse_mode: 'Markdown',
              reply_markup: {
                inline_keyboard: [
                  [
                    { text: "📊 查看状态", callback_data: "refresh_status" },
                    { text: "📋 交易员列表", callback_data: "list_traders" }
                  ]
                ]
              }
            }
          );
        } else {
          await ctx.reply(
            `❌ **创建失败**\n\n` +
            `错误信息：${createResult.error || '未知错误'}\n\n` +
            `请检查：\n` +
            `• 信息是否正确\n` +
            `• 交易所配置是否完整\n` +
            `• 网络连接是否正常\n\n` +
            `如问题持续，请联系管理员。`
          );
        }
      }
    } catch (error) {
      console.error('Error creating trader:', error);
      await ctx.reply('❌ 创建过程中发生错误，请稍后再试。');
    }

    // 清除状态
    stateManager.clearState(user.id);
  }

  // 处理演示AI模型选择
  async handleDemoAIModelSelection(ctx: Context, demoModel: string): Promise<void> {
    const user = ctx.from;
    if (!user) return;

    const currentState = stateManager.getState(user.id);
    if (!currentState || !currentState.data?.name) {
      await ctx.reply('❌ 会话已过期，请重新开始创建流程。');
      stateManager.clearState(user.id);
      return;
    }

    // 演示模型映射
    const demoModels = {
      'deepseek': { id: 'demo_deepseek', name: 'DeepSeek Trader', provider: 'DeepSeek' },
      'qwen': { id: 'demo_qwen', name: 'Qwen Master', provider: 'Alibaba' },
      'gpt': { id: 'demo_gpt', name: 'GPT Trader Pro', provider: 'OpenAI' }
    };

    const selectedModel = demoModels[demoModel as keyof typeof demoModels];
    if (!selectedModel) {
      await ctx.reply('❌ 选择的演示AI模型不存在。请重新选择。');
      return;
    }

    // 更新状态，使用演示模型ID
    stateManager.updateState(user.id, {
      ai_model_id: selectedModel.id,
      demo_model_name: selectedModel.name,
      demo_model_provider: selectedModel.provider
    });
    stateManager.setState(user.id, 'select_exchange', {
      ...currentState.data,
      ai_model_id: selectedModel.id,
      demo_model_name: selectedModel.name,
      demo_model_provider: selectedModel.provider
    });

    // 提供演示用的交易所选项
    await ctx.reply('⚠️ 系统中暂无配置的交易所，为您提供演示选项：\n\n' +
      '🏦 **选择交易所（演示）**\n\n' +
      `为交易员 "${currentState.data.name}" 选择交易所：\n\n` +
      `已选择AI模型：${selectedModel.name} (${selectedModel.provider})\n\n`);

    const keyboard = [
      [{
        text: "Hyperliquid (测试网) - 推荐新手",
        callback_data: "select_exchange_demo_hyperliquid_testnet"
      }],
      [{
        text: "Binance Futures (测试网) - 稳定可靠",
        callback_data: "select_exchange_demo_binance_testnet"
      }],
      [{
        text: "OKX (测试网) - 功能丰富",
        callback_data: "select_exchange_demo_okx_testnet"
      }],
      [{ text: "❌ 取消", callback_data: "cancel_create_trader" }]
    ];

    await ctx.reply('📝 *演示说明：*\n' +
      '这些是演示交易所，用于展示创建流程。\n' +
      '实际使用时，请联系管理员配置真实的交易所API。',
      {
        parse_mode: 'Markdown',
        reply_markup: { inline_keyboard: keyboard }
      }
    );
  }

  // 处理演示交易所选择
  async handleDemoExchangeSelection(ctx: Context, demoExchange: string): Promise<void> {
    const user = ctx.from;
    if (!user) return;

    const currentState = stateManager.getState(user.id);
    if (!currentState) {
      await ctx.reply('❌ 会话已过期，请重新开始创建流程。');
      stateManager.clearState(user.id);
      return;
    }

    // 演示交易所映射
    const demoExchanges = {
      'hyperliquid_testnet': { id: 'demo_hyperliquid_testnet', name: 'Hyperliquid', testnet: true },
      'binance_testnet': { id: 'demo_binance_testnet', name: 'Binance Futures', testnet: true },
      'okx_testnet': { id: 'demo_okx_testnet', name: 'OKX', testnet: true }
    };

    const selectedExchange = demoExchanges[demoExchange as keyof typeof demoExchanges];
    if (!selectedExchange) {
      await ctx.reply('❌ 选择的演示交易所不存在。请重新选择。');
      return;
    }

    // 更新状态
    stateManager.updateState(user.id, {
      exchange_id: selectedExchange.id,
      demo_exchange_name: selectedExchange.name,
      demo_exchange_testnet: selectedExchange.testnet
    });
    stateManager.setState(user.id, 'enter_initial_balance', {
      ...currentState.data,
      exchange_id: selectedExchange.id,
      demo_exchange_name: selectedExchange.name,
      demo_exchange_testnet: selectedExchange.testnet
    });

    // 询问初始资金
    let balanceMessage = `💰 **设置初始资金**\n\n`;
    balanceMessage += `请输入交易员的初始资金（USDT）：\n\n`;
    balanceMessage += `📊 **当前配置：**\n`;
    balanceMessage += `• 交易员名称：${currentState.data?.name}\n`;
    balanceMessage += `• AI模型：${currentState.data?.demo_model_name} (${currentState.data?.demo_model_provider})\n`;
    balanceMessage += `• 交易所：${selectedExchange.name}${selectedExchange.testnet ? ' (测试网)' : ''}\n\n`;
    balanceMessage += `💡 *建议：*\n`;
    balanceMessage += `• 测试建议：10-100 USDT\n`;
    balanceMessage += `• 小额尝试：100-500 USDT\n`;
    balanceMessage += `• 正式交易：1000+ USDT\n\n`;
    balanceMessage += `请输入初始资金金额（数字）：`;

    await ctx.reply(balanceMessage, {
      parse_mode: 'Markdown',
      reply_markup: {
        inline_keyboard: [
          [
            { text: "100 USDT", callback_data: "set_balance_100" },
            { text: "500 USDT", callback_data: "set_balance_500" }
          ],
          [
            { text: "1000 USDT", callback_data: "set_balance_1000" },
            { text: "自定义金额", callback_data: "custom_balance" }
          ],
          [
            { text: "❌ 取消", callback_data: "cancel_create_trader" }
          ]
        ]
      }
    });
  }

  // 处理演示交易员列表
  async handleDemoTradersList(ctx: Context): Promise<void> {
    const user = ctx.from;
    if (!user) return;

    const currentState = stateManager.getState(user.id);
    const demoTraders = currentState?.data?.demo_traders || [];

    if (demoTraders.length === 0) {
      await ctx.reply('📭 **暂无演示交易员**\n\n' +
        '您还没有创建任何演示交易员。\n\n' +
        '使用 /create 命令来创建您的第一个演示交易员！\n\n' +
        '💡 演示交易员用于展示AI交易功能，不会进行真实交易。',
        {
          parse_mode: 'Markdown',
          reply_markup: {
            inline_keyboard: [
              [
                { text: "🚀 创建演示交易员", callback_data: "create_trader" },
                { text: "📖 帮助", callback_data: "help" }
              ]
            ]
          }
        }
      );
      return;
    }

    let listMessage = `🎭 **演示交易员列表 (${demoTraders.length})**\n\n`;
    listMessage += '⚠️ *这些是演示交易员，仅用于展示功能*\n\n';

    demoTraders.forEach((trader: any, index: number) => {
      const status = trader.is_running ? '🟢 运行中' : '🔴 已停止';
      const pnl = trader.total_pnl >= 0 ? `+$${trader.total_pnl.toFixed(2)}` : `-$${Math.abs(trader.total_pnl).toFixed(2)}`;
      const pnlEmoji = trader.total_pnl >= 0 ? '📈' : '📉';

      listMessage += `${index + 1}. ${status} **${trader.trader_name}**\n`;
      listMessage += `   ${pnlEmoji} 模拟收益: ${pnl} (${trader.total_pnl_pct.toFixed(2)}%)\n`;
      listMessage += `   💰 模拟资金: $${trader.initial_balance} → $${trader.total_equity.toFixed(2)}\n`;
      listMessage += `   🤖 ${trader.ai_model} | 💱 ${trader.exchange_type}\n`;
      listMessage += `   📅 创建时间: ${new Date(trader.created_at).toLocaleDateString('zh-CN')}\n\n`;
    });

    listMessage += '💡 *提示：*\n' +
      '• 演示交易员不会进行真实交易\n' +
      '• 可以通过 /create 命令创建更多演示交易员\n' +
      '• 如需创建真实交易员，请联系管理员配置系统';

    await ctx.reply(listMessage, {
      parse_mode: 'Markdown',
      reply_markup: {
        inline_keyboard: [
          [
            { text: "🔄 刷新", callback_data: "list_demo_traders" },
            { text: "📊 查看状态", callback_data: "refresh_status" }
          ],
          [
            { text: "🚀 创建新演示交易员", callback_data: "create_trader" },
            { text: "📋 查看真实交易员", callback_data: "list_traders" }
          ]
        ]
      }
    });
  }

  // 取消创建流程
  async cancelCreation(ctx: Context): Promise<void> {
    const user = ctx.from;
    if (!user) return;

    stateManager.clearState(user.id);

    await ctx.reply('❌ 交易员创建已取消。\n\n' +
      '如需重新创建，请使用 /create 命令。\n\n' +
      '其他操作：\n' +
      '• /status - 查看交易员状态\n' +
      '• /list - 查看交易员列表\n' +
      '• /help - 获取帮助',
      {
        reply_markup: {
          inline_keyboard: [
            [
              { text: "📊 查看状态", callback_data: "refresh_status" },
              { text: "📋 交易员列表", callback_data: "list_traders" }
            ]
          ]
        }
      }
    );
  }
}