import { Bot, Context } from 'grammy';
import { ApiClient } from '../api/client';

export async function handleStartTrader(ctx: Context, apiClient: ApiClient, traderId?: string) {
  const user = ctx.from;
  if (!user) return;

  if (!traderId) {
    await ctx.reply('🔍 Please specify which trader to start:\n\n' +
      'Use: `/start_trader <trader_id>`\n\n' +
      'Or check your trader list with `/list` to see available traders.',
      { parse_mode: 'Markdown' }
    );
    return;
  }

  await ctx.reply(`🚀 Starting trader: \`${traderId.slice(0, 15)}...\``, {
    parse_mode: 'Markdown',
  });

  try {
    const result = await apiClient.startTrader(traderId, user.id);

    if (result.success) {
      await ctx.reply(
        `✅ **Trader Started Successfully!**\n\n` +
        `Trader ID: \`${traderId}\`\n` +
        `Status: 🟢 Running\n\n` +
        `📊 You can check the trader\'s performance with /status`,
        { parse_mode: 'Markdown' }
      );
    } else {
      await ctx.reply(
        `❌ **Failed to Start Trader**\n\n` +
        `Error: ${result.error}\n` +
        `Trader ID: \`${traderId}\`\n\n` +
        `Please check the trader status and try again.`,
        { parse_mode: 'Markdown' }
      );
    }
  } catch (error) {
    console.error('Error starting trader:', error);
    await ctx.reply('❌ An error occurred while starting the trader. Please try again later.');
  }
}