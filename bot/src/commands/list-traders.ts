import { Bot, Context } from 'grammy';
import { ApiClient } from '../api/client';

export async function handleListTraders(ctx: Context, apiClient: ApiClient) {
  const user = ctx.from;
  if (!user) return;

  await ctx.reply('📋 Fetching your traders list...');

  try {
    const result = await apiClient.getTraders(user.id);

    if (!result.success || !result.data) {
      await ctx.reply(`❌ Failed to fetch traders: ${result.error || 'Unknown error'}`);
      return;
    }

    const traders = result.data?.traders || [];

    if (traders.length === 0) {
      await ctx.reply('📭 You have no traders yet. Use /create to create your first trader!');
      return;
    }

    let listMessage = `📋 **Your Traders (${traders.length})**\n\n`;

    traders.forEach((trader, index) => {
      const status = trader.is_running ? '🟢' : '🔴';
      const pnl = trader.total_pnl >= 0 ? `+$${trader.total_pnl.toFixed(2)}` : `-$${Math.abs(trader.total_pnl).toFixed(2)}`;

      listMessage += `${index + 1}. ${status} **${trader.display_name || trader.trader_name}**\n`;
      listMessage += `   💰 ${pnl} (${trader.total_pnl_pct.toFixed(2)}%)\n`;
      listMessage += `   🤖 ${trader.ai_model} | 💱 ${trader.exchange_type || 'Unknown'}\n\n`;
    });

    await ctx.reply(listMessage, {
      parse_mode: 'Markdown',
      reply_markup: {
        inline_keyboard: [
          [
            { text: "🔄 Refresh", callback_data: "refresh_status" },
            { text: "📊 Detailed Status", callback_data: "refresh_status" }
          ]
        ]
      }
    });

  } catch (error) {
    console.error('Error listing traders:', error);
    await ctx.reply('❌ An error occurred while fetching traders. Please try again later.');
  }
}