import { Bot, Context } from 'grammy';
import { ApiClient } from '../api/client';
import { TraderCreationHandler } from '../handlers/trader-creation';
import { stateManager } from '../utils/state-manager';

export async function handleCreateTrader(ctx: Context, apiClient: ApiClient) {
  const user = ctx.from;
  if (!user) return;

  // 检查用户是否已经在创建流程中
  if (stateManager.isInFlow(user.id, 'create_trader') ||
      stateManager.isInFlow(user.id, 'enter_trader_name') ||
      stateManager.isInFlow(user.id, 'select_ai_model') ||
      stateManager.isInFlow(user.id, 'select_exchange') ||
      stateManager.isInFlow(user.id, 'enter_initial_balance') ||
      stateManager.isInFlow(user.id, 'confirm_create')) {

    await ctx.reply('⚠️ 您已经有一个交易员创建流程在进行中。\n\n' +
      '请完成当前的创建流程，或者使用 /cancel 取消当前流程。\n\n' +
      '💡 如果您想重新开始，请先发送 /cancel 取消当前流程。');
    return;
  }

  const handler = new TraderCreationHandler(apiClient);

  // 设置初始状态
  stateManager.setState(user.id, 'create_trader', {});

  // 开始创建流程
  await handler.startCreation(ctx);
}