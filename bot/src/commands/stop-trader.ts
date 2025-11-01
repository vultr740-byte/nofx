import { Bot, Context } from 'grammy';
import { ApiClient } from '../api/client';

export async function handleStopTrader(ctx: Context, apiClient: ApiClient, traderId?: string) {
  const user = ctx.from;
  if (!user) return;

  if (!traderId) {
    await ctx.reply('🔍 Please specify which trader to stop:\n\n' +
      'Use: `/stop_trader <trader_id>`\n\n' +
      'Or check your trader list with `/list` to see available traders.',
      { parse_mode: 'Markdown' }
    );
    return;
  }

  await ctx.reply(`⏹ Stopping trader: \`${traderId.slice(0, 15)}...\``, {
    parse_mode: 'Markdown',
  });

  try {
    const result = await apiClient.stopTrader(traderId, user.id);

    if (result.success) {
      await ctx.reply(
        `✅ **Trader Stopped Successfully!**\n\n` +
        `Trader ID: \`${traderId}\`\n` +
        `Status: 🔴 Stopped\n\n` +
        `💡 The trader has been safely stopped. You can start it again anytime.`,
        { parse_mode: 'Markdown' }
      );
    } else {
      await ctx.reply(
        `❌ **Failed to Stop Trader**\n\n` +
        `Error: ${result.error}\n` +
        `Trader ID: \`${traderId}\`\n\n` +
        `Please check the trader status and try again.`,
        { parse_mode: 'Markdown' }
      );
    }
  } catch (error) {
    console.error('Error stopping trader:', error);
    await ctx.reply('❌ An error occurred while stopping the trader. Please try again later.');
  }
}