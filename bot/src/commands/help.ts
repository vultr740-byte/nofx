import { Bot, Context } from 'grammy';

export async function handleHelp(ctx: Context) {
  const helpMessage = `
📖 **NOFX AI 交易助手帮助**

🚀 **快速开始流程：**
1. 🤖 创建AI模型 - 配置AI交易策略
2. 🏦 添加交易所 - 连接交易所账户
3. 📈 创建交易员 - 开始自动交易

🤖 **基础命令:**
/start - 启动机器人
/help - 显示此帮助信息
/status - 检查交易员状态
/cancel - 取消当前操作

📋 **交易员管理:**
/list - 查看所有交易员
/create - 创建新交易员（交互式）
/start_trader <ID> - 启动指定交易员
/stop_trader <ID> - 停止指定交易员

💡 **使用示例:**
• /status - 查看所有交易员状态
• /create - 创建新交易员（多步骤引导）
• /start_trader abc123 - 启动ID为abc123的交易员

✨ **主要功能:**
🤖 多种AI交易算法支持
📊 实时性能追踪统计
⚡ 快速启停控制
🔔 状态变化通知
💰 多交易所支持
🎯 交互式创建流程

🎯 **创建交易员完整流程:**
1. 使用 /create 或点击按钮开始
2. 输入交易员名称
3. 选择AI模型（需先创建）
4. 选择交易所（需先添加）
5. 设置初始资金
6. 确认创建

⚠️ **重要提示:**
• 请先创建AI模型和添加交易所账户
• AI自动交易存在风险，建议小额测试
• 密切监控交易员表现
• 使用测试网进行初期测试

💡 **提示: 使用下方按钮快速操作！**

🆘 **需要更多帮助?**
联系技术支持获取指导。
`;

  await ctx.reply(helpMessage, {
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
          { text: "🔙 返回主页", callback_data: "back_to_home" }
        ]
      ]
    }
  });
}