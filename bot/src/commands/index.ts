import { Bot } from 'grammy';
import { ApiClient } from '../api/client';
import { MessageHandler } from '../handlers/message-handler';
import { handleStart } from './start';
import { handleHelp } from './help';
import { handleCancel } from './cancel';

export function setupCommands(bot: Bot, apiClient: ApiClient) {
  // 创建消息处理器
  const messageHandler = new MessageHandler(apiClient);

  // Start command
  bot.command('start', async (ctx) => {
    await handleStart(ctx, apiClient);
  });

  // Help command
  bot.command('help', async (ctx) => {
    await handleHelp(ctx);
  });

  // Cancel command
  bot.command('cancel', async (ctx) => {
    await handleCancel(ctx);
  });

  // Status command
  bot.command('status', async (ctx) => {
    const { handleStatus } = await import('./status');
    await handleStatus(ctx, apiClient);
  });

  // List traders command
  bot.command('list', async (ctx) => {
    const { handleListTraders } = await import('./list-traders');
    await handleListTraders(ctx, apiClient);
  });

  // Create trader command
  bot.command('create', async (ctx) => {
    const { handleCreateTrader } = await import('./create-trader');
    await handleCreateTrader(ctx, apiClient);
  });

  // Start trader command
  bot.command('start_trader', async (ctx) => {
    const { handleStartTrader } = await import('./start-trader');
    const traderId = ctx.match; // 获取命令参数
    await handleStartTrader(ctx, apiClient, traderId);
  });

  // Stop trader command
  bot.command('stop_trader', async (ctx) => {
    const { handleStopTrader } = await import('./stop-trader');
    const traderId = ctx.match; // 获取命令参数
    await handleStopTrader(ctx, apiClient, traderId);
  });

  // Handle text messages (for trader creation flow)
  bot.on('message:text', async (ctx) => {
    await messageHandler.handleTextMessage(ctx);
  });

  // Handle callback queries (inline keyboard buttons)
  bot.on('callback_query', async (ctx) => {
    await messageHandler.handleCallbackQuery(ctx);
  });

  console.log('✅ Bot commands registered');
}