import { Context } from 'grammy';
import { ApiClient } from '../api/client';
import { stateManager } from '../utils/state-manager';

export class AIModelCreationHandler {
  constructor(private apiClient: ApiClient) {}

  // 开始AI模型创建流程
  async startModelCreation(ctx: Context, userId: number): Promise<void> {
    // 清除之前的状态
    stateManager.clearState(userId);

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
    stateManager.setState(userId, { action: 'enter_model_name', createdAt: Date.now() });
  }

  // 处理AI模型名称输入
  async handleModelName(ctx: Context, name: string): Promise<void> {
    const user = ctx.from;
    if (!user) return;

    // 验证名称
    if (!name || name.trim().length < 2) {
      await ctx.reply('❌ AI模型名称至少需要2个字符。请重新输入：');
      return;
    }

    if (name.trim().length > 30) {
      await ctx.reply('❌ AI模型名称不能超过30个字符。请重新输入：');
      return;
    }

    // 更新状态
    stateManager.updateState(user.id, { model_name: name.trim() });
    stateManager.setState(user.id, 'select_model_provider', { model_name: name.trim() });

    // 显示AI模型提供商选择
    await this.showModelProviderSelection(ctx, name.trim());
  }

  // 显示AI模型提供商选择
  async showModelProviderSelection(ctx: Context, modelName: string): Promise<void> {
    const keyboard = [
      [
        { text: "DeepSeek - 高性能中文模型", callback_data: "select_provider_deepseek" }
      ],
      [
        { text: "Qwen - 新手友好模型", callback_data: "select_provider_qwen" }
      ],
      [
        { text: "Claude - 高级分析模型", callback_data: "select_provider_claude" }
      ],
      [
        { text: "GPT-4 - 多功能模型", callback_data: "select_provider_gpt4" }
      ],
      [
        { text: "❌ 取消", callback_data: "cancel_create_model" }
      ]
    ];

    await ctx.reply(`🤖 **选择AI模型提供商**\n\n` +
      `为AI模型 "${modelName}" 选择提供商：\n\n` +
      `不同提供商的特点：\n` +
      `• DeepSeek - 中文理解能力强，适合中文交易策略\n` +
      `• Qwen - 新手友好，稳定可靠\n` +
      `• Claude - 高级分析能力，适合复杂策略\n` +
      `• GPT-4 - 多功能强大，全球通用\n\n` +
      `请选择一个提供商：`, {
      reply_markup: { inline_keyboard: keyboard }
    });
  }

  // 处理提供商选择
  async handleProviderSelection(ctx: Context, provider: string): Promise<void> {
    const user = ctx.from;
    if (!user) return;

    const currentState = stateManager.getState(user.id);
    if (!currentState || !currentState.data?.model_name) {
      await ctx.reply('❌ 会话已过期，请重新开始。');
      stateManager.clearState(user.id);
      return;
    }

    // 验证提供商
    const supportedProviders = ['deepseek', 'qwen', 'claude', 'gpt4'];
    if (!supportedProviders.includes(provider)) {
      await ctx.reply('❌ 不支持的提供商。请重新选择。');
      return;
    }

    // 提供商信息映射
    const providerInfo = {
      deepseek: { name: 'DeepSeek', description: '高性能中文大模型，擅长中文理解和生成' },
      qwen: { name: 'Qwen', description: '阿里巴巴通义千问模型，新手友好' },
      claude: { name: 'Claude', description: 'Anthropic高级AI模型，适合复杂分析' },
      gpt4: { name: 'GPT-4', description: 'OpenAI多功能模型，全球通用' }
    };

    const selectedProvider = providerInfo[provider as keyof typeof providerInfo];

    // 更新状态
    stateManager.updateState(user.id, {
      model_provider: provider,
      model_provider_name: selectedProvider.name,
      model_provider_description: selectedProvider.description
    });
    stateManager.setState(user.id, 'enter_api_key', {
      ...currentState.data,
      model_provider: provider,
      model_provider_name: selectedProvider.name,
      model_provider_description: selectedProvider.description
    });

    // 显示API密钥输入界面
    await this.showAPIKeyInput(ctx, selectedProvider);
  }

  // 显示API密钥输入界面
  async showAPIKeyInput(ctx: Context, providerInfo: { name: string; description: string }): Promise<void> {
    const keyboard = [
      [
        { text: "跳过（演示模式）", callback_data: "skip_api_key" }
      ],
      [
        { text: "❌ 取消", callback_data: "cancel_create_model" }
      ]
    ];

    await ctx.reply(`🔑 **输入${providerInfo.name} API密钥**\n\n` +
      `模型描述：${providerInfo.description}\n\n` +
      `🔒 **安全提示：**\n` +
      `• API密钥将被安全存储\n` +
      `• 仅用于调用${providerInfo.name} API\n` +
      `• 建议使用专用API密钥\n\n` +
      `请输入您的API密钥：\n\n` +
      `💡 提示：如果没有API密钥，可以选择"跳过"使用演示模式`, {
      reply_markup: { inline_keyboard: keyboard }
    });
  }

  // 处理API密钥输入
  async handleAPIKeyInput(ctx: Context, apiKey: string): Promise<void> {
    const user = ctx.from;
    if (!user) return;

    const currentState = stateManager.getState(user.id);
    if (!currentState || !currentState.data?.model_name) {
      await ctx.reply('❌ 会话已过期，请重新开始。');
      stateManager.clearState(user.id);
      return;
    }

    // 验证API密钥（简单验证）
    if (apiKey && apiKey.trim().length < 10) {
      await ctx.reply('❌ API密钥格式不正确，请重新输入：');
      return;
    }

    // 更新状态
    stateManager.updateState(user.id, { api_key: apiKey.trim() });
    stateManager.setState(user.id, 'enter_description', {
      ...currentState.data,
      api_key: apiKey.trim()
    });

    // 显示描述输入界面
    await this.showDescriptionInput(ctx);
  }

  // 显示描述输入界面
  async showDescriptionInput(ctx: Context): Promise<void> {
    const currentState = stateManager.getState(ctx.from!.id);
    if (!currentState) return;

    const keyboard = [
      [
        { text: "跳过描述", callback_data: "skip_description" }
      ],
      [
        { text: "❌ 取消", callback_data: "cancel_create_model" }
      ]
    ];

    await ctx.reply(`📝 **输入模型描述（可选）**\n\n` +
      `为AI模型 "${currentState.data.model_name}" 添加描述：\n\n` +
      `描述有助于：\n` +
      `• 明确模型的交易策略风格\n` +
      `• 设定风险偏好\n` +
      `• 指定交易品种偏好\n\n` +
      `💡 请输入简短描述（最多200字符）：\n\n` +
      `或选择"跳过描述"继续创建`, {
        reply_markup: { inline_keyboard: keyboard }
      });
  }

  // 处理描述输入
  async handleDescriptionInput(ctx: Context, description: string): Promise<void> {
    const user = ctx.from;
    if (!user) return;

    const currentState = stateManager.getState(user.id);
    if (!currentState || !currentState.data?.model_name) {
      await ctx.reply('❌ 会话已过期，请重新开始。');
      stateManager.clearState(user.id);
      return;
    }

    // 验证描述长度
    if (description && description.length > 200) {
      await ctx.reply('❌ 描述过长，请保持在200字符以内：');
      return;
    }

    // 更新状态
    stateManager.updateState(user.id, {
      description: description ? description.trim() : null
    });

    // 显示确认界面
    await this.showConfirmation(ctx);
  }

  // 显示确认界面
  async showConfirmation(ctx: Context): Promise<void> {
    const user = ctx.from;
    if (!user) return;

    const currentState = stateManager.getState(user.id);
    if (!currentState || !currentState.data?.model_name) {
      await ctx.reply('❌ 会话已过期，请重新开始。');
      stateManager.clearState(user.id);
      return;
    }

    const data = currentState.data;
    const hasAPIKey = !!data.api_key;

    let confirmMessage = `✅ **确认创建AI模型**\n\n`;
    confirmMessage += `请核对以下信息：\n\n`;
    confirmMessage += `📝 **模型信息：**\n`;
    confirmMessage += `• 名称：${data.model_name}\n`;
    confirmMessage += `• 提供商：${data.model_provider_name || '未知'}\n`;
    confirmMessage += `• API密钥：${hasAPIKey ? '已设置 ✓' : '未设置 ⚠️'}\n`;
    confirmMessage += `• 描述：${data.description || '无'}\n\n`;

    if (!hasAPIKey) {
      confirmMessage += `⚠️ **注意：**\n`;
      confirmMessage += `您选择跳过API密钥，这将创建一个演示模型。\n`;
      confirmMessage += `演示模型无法进行真实的AI推理。\n\n`;
    }

    confirmMessage += `🎯 **下一步：**\n`;
    confirmMessage += `创建后，您可以在创建交易员时选择此模型。\n\n`;
    confirmMessage += `确认要创建这个AI模型吗？`;

    const keyboard = [
      [
        { text: "✅ 确认创建", callback_data: "confirm_create_model" },
        { text: "❌ 取消", callback_data: "cancel_create_model" }
      ]
    ];

    await ctx.reply(confirmMessage, {
      reply_markup: { inline_keyboard: keyboard }
    });
  }

  // 确认创建模型
  async confirmCreate(ctx: Context): Promise<void> {
    const user = ctx.from;
    if (!user) return;

    const currentState = stateManager.getState(user.id);
    if (!currentState || !currentState.data?.model_name) {
      await ctx.reply('❌ 会话已过期，请重新开始。');
      stateManager.clearState(user.id);
      return;
    }

    const data = currentState.data;

    try {
      // 发送创建中消息
      await ctx.reply('🔄 正在创建AI模型，请稍候...');

      // 调用API创建模型
      const createResult = await this.apiClient.createAIModel(user.id, {
        name: data.model_name,
        provider: data.model_provider || 'unknown',
        api_key: data.api_key || '',
        description: data.description || '',
        enabled: true
      });

      if (createResult.success && createResult.data) {
        await ctx.reply(
          `🎉 **AI模型创建成功！**\n\n` +
          `✅ 模型信息：\n` +
          `• ID：\`${createResult.data.id}\`\n` +
          `• 名称：${createResult.data.name}\n` +
          `• 提供商：${createResult.data.provider}\n` +
          `• 状态：✅ 已启用\n\n` +
          `💡 **下一步操作：**\n` +
          `• 使用 /create 创建交易员\n` +
          `• 使用 /status 查看模型列表\n` +
          `• 使用 /help 获取帮助\n\n` +
          `🚀 现在可以开始创建交易员了！`,
          {
            parse_mode: 'Markdown',
            reply_markup: {
              inline_keyboard: [
                [
                  { text: "🚀 创建交易员", callback_data: "create_trader" },
                  { text: "📊 查看模型", callback_data: "list_ai_models" }
                ],
                [
                  { text: "📊 查看状态", callback_data: "refresh_status" },
                  { text: "📖 帮助", callback_data: "help" }
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
          `• API密钥是否正确\n` +
          `• 网络连接是否正常\n\n` +
          `如问题持续，请联系管理员。`
        );
      }
    } catch (error) {
      console.error('Error creating AI model:', error);
      await ctx.reply('❌ 创建过程中发生错误，请稍后再试。');
    }

    // 清除状态
    stateManager.clearState(user.id);
  }

  // 取消创建
  async cancelCreation(ctx: Context): Promise<void> {
    const user = ctx.from;
    if (!user) return;

    stateManager.clearState(user.id);

    await ctx.reply('❌ AI模型创建已取消。\n\n' +
      '如需重新创建，请使用 /create_ai_model 命令。\n\n' +
      '其他操作：\n' +
      '• /status - 查看AI模型状态\n' +
      '• /help - 获取帮助\n' +
      '• /create - 创建交易员',
      {
        reply_markup: {
          inline_keyboard: [
            [
              { text: "📊 查看状态", callback_data: "refresh_status" },
              { text: "📋 模型列表", callback_data: "list_ai_models" }
            ],
            [
              { text: "🚀 创建交易员", callback_data: "create_trader" },
              { text: "📖 帮助", callback_data: "help" }
            ]
          ]
        }
      }
    );
  }

  // 处理跳过API密钥
  async skipAPIKey(ctx: Context): Promise<void> {
    const user = ctx.from;
    if (!user) return;

    const currentState = stateManager.getState(user.id);
    if (!currentState || !currentState.data?.model_name) {
      await ctx.reply('❌ 会话已过期，请重新开始。');
      stateManager.clearState(user.id);
      return;
    }

    // 更新状态，跳过API密钥
    stateManager.updateState(user.id, { api_key: null, is_demo: true });
    stateManager.setState(user.id, 'enter_description', {
      ...currentState.data,
      api_key: null,
      is_demo: true
    });

    // 显示描述输入界面
    await this.showDescriptionInput(ctx);
  }

  // 处理跳过描述
  async skipDescription(ctx: Context): Promise<void> {
    const user = ctx.from;
    if (!user) return;

    const currentState = stateManager.getState(user.id);
    if (!currentState || !currentState.data?.model_name) {
      await ctx.reply('❌ 会话已过期，请重新开始。');
      stateManager.clearState(user.id);
      return;
    }

    // 更新状态，跳过描述
    stateManager.updateState(user.id, { description: null });

    // 显示确认界面
    await this.showConfirmation(ctx);
  }
}