import { Bot, Context } from 'grammy';
import { ApiClient } from '../api/client';

export async function handleStart(ctx: Context, apiClient: ApiClient) {
  const user = ctx.from;
  if (!user) return;

  console.log(`👤 User ${user.id} (@${user.username}) started the bot`);

  const welcomeMessage = `
👋 您好！我是 NOFX AI 交易助手。

🚀 **快速开始：**
1. 创建AI模型
2. 添加交易所账户
3. 创建交易员

💡 **我可以帮助您：**
• 🤖 创建AI模型 - 配置您的AI交易策略
• 🏦 添加交易所 - 连接您的交易所账户
• 📈 创建交易员 - 开始AI自动交易
• 📊 查看状态 - 监控交易员表现
• 📋 查看列表 - 管理所有交易员

请选择下方操作开始：
`;

  await ctx.reply(welcomeMessage, {
    parse_mode: 'Markdown',
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