import { Bot, Context } from 'grammy';
import { ApiClient } from '../api/client';

export async function handleStatus(ctx: Context, apiClient: ApiClient) {
  const user = ctx.from;
  if (!user) return;

  await ctx.reply('🔄 Checking your traders status...');

  try {
    const result = await apiClient.getTraders(user.id);

    if (!result.success || !result.data) {
      await ctx.reply(`❌ Failed to fetch traders: ${result.error || 'Unknown error'}`);
      return;
    }

    const traders = result.data?.traders || [];

    if (traders.length === 0) {
      const noTradersMessage = `
📭 **No Traders Found**

You don't have any traders yet.

*What would you like to do?*
🚀 Create your first trader
📊 Learn more about AI trading

Click the button below to get started!
`;

      await ctx.reply(noTradersMessage, {
        parse_mode: 'Markdown',
        reply_markup: {
          inline_keyboard: [
            [
              { text: "🚀 Create First Trader", callback_data: "create_trader" },
              { text: "📖 Help", callback_data: "help" }
            ]
          ]
        }
      });
      return;
    }

    // Build status message
    let statusMessage = `📊 **Your Traders Status**\n\n`;

    const runningCount = traders.filter(t => t.is_running).length;
    const stoppedCount = traders.length - runningCount;

    statusMessage += `📈 *Summary:* ${runningCount} running, ${stoppedCount} stopped\n\n`;

    for (const trader of traders) {
      const status = trader.is_running ? '🟢 Running' : '🔴 Stopped';
      const pnl = trader.total_pnl >= 0 ? `+${trader.total_pnl.toFixed(2)}` : trader.total_pnl.toFixed(2);
      const pnlEmoji = trader.total_pnl >= 0 ? '📈' : '📉';

      statusMessage += `${status} **${trader.display_name || trader.trader_name}**\n`;
      statusMessage += `${pnlEmoji} PnL: ${pnl} (${trader.total_pnl_pct.toFixed(2)}%)\n`;
      statusMessage += `💰 Equity: $${trader.total_equity.toFixed(2)}\n`;
      statusMessage += `📍 Positions: ${trader.position_count}\n\n`;
    }

    await ctx.reply(statusMessage, {
      parse_mode: 'Markdown',
      reply_markup: {
        inline_keyboard: [
          [
            { text: "🔄 Refresh", callback_data: "refresh_status" },
            { text: "📋 Full List", callback_data: "list_traders" }
          ],
          [
            { text: "🚀 Create Trader", callback_data: "create_trader" }
          ]
        ]
      }
    });

  } catch (error) {
    console.error('Error fetching trader status:', error);
    await ctx.reply('❌ An error occurred while fetching trader status. Please try again later.');
  }
}