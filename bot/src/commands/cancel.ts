import { Bot, Context } from 'grammy';
import { stateManager } from '../utils/state-manager';

export async function handleCancel(ctx: Context) {
  const user = ctx.from;
  if (!user) return;

  const userState = stateManager.getState(user.id);
  if (!userState) {
    await ctx.reply('ℹ️ 当前没有正在进行的操作。\n\n' +
      '您可以开始新的操作：\n' +
      '• /create - 创建新交易员\n' +
      '• /status - 查看状态\n' +
      '• /help - 获取帮助',
      {
        reply_markup: {
          inline_keyboard: [
            [
              { text: "📊 查看状态", callback_data: "refresh_status" },
              { text: "🚀 创建交易员", callback_data: "create_trader" }
            ]
          ]
        }
      }
    );
    return;
  }

  // 清除用户状态
  stateManager.clearState(user.id);

  await ctx.reply('✅ 操作已取消。\n\n' +
    '您可以重新开始：\n' +
    '• /create - 创建新交易员\n' +
    '• /status - 查看状态\n' +
    '• /list - 查看交易员列表\n' +
    '• /help - 获取帮助',
    {
      reply_markup: {
        inline_keyboard: [
          [
            { text: "📊 查看状态", callback_data: "refresh_status" },
            { text: "📋 交易员列表", callback_data: "list_traders" }
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