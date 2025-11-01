import { Hono } from 'hono';
import { serve } from '@hono/node-server';
import { Bot } from 'grammy';
import 'dotenv/config';

import { ApiClient } from './api/client';
import { setupCommands } from './commands';

const app = new Hono();
const port = parseInt(process.env.PORT || '3000');

// 初始化Telegram Bot
const botToken = process.env.BOT_TOKEN;
const webhookSecret = process.env.WEBHOOK_SECRET;

if (!botToken) {
  console.error('❌ BOT_TOKEN environment variable is required');
  process.exit(1);
}

if (!webhookSecret) {
  console.error('❌ WEBHOOK_SECRET environment variable is required');
  process.exit(1);
}

const bot = new Bot(botToken);

// 初始化API客户端
const apiClient = new ApiClient();

// 设置Bot命令和处理器
setupCommands(bot, apiClient);

// 设置错误处理器
bot.catch((err) => {
  console.error('❌ Bot error:', err);
  console.error('Context:', err.ctx);

  // 尝试向用户发送错误消息
  if (err.ctx && err.ctx.chat) {
    err.ctx.reply('❌ 抱歉，处理过程中发生了错误。请稍后再试，或联系管理员。').catch(() => {
      console.error('Failed to send error message to user');
    });
  }
});

// Webhook端点
app.post('/webhook', async (c) => {
  const secret = c.req.header('x-telegram-bot-api-secret-token');

  if (secret !== webhookSecret) {
    console.warn('⚠️ Invalid webhook secret');
    return c.text('Unauthorized', 403);
  }

  try {
    const update = await c.req.json();
    console.log('📨 Received update:', update.update_id);

    // 处理更新
    await bot.handleUpdate(update);

    return c.text('OK');
  } catch (error) {
    console.error('❌ Error handling update:', error);
    return c.text('Internal Server Error', 500);
  }
});

// 健康检查端点
app.get('/health', (c) => {
  return c.json({
    status: 'ok',
    timestamp: new Date().toISOString(),
    bot: 'running',
  });
});

// 启动服务器
console.log(`🚀 Starting Telegram Bot server on port ${port}`);
console.log(`📡 Webhook URL: ${process.env.WEBHOOK_URL || 'http://localhost:' + port + '/webhook'}`);
console.log(`🤖 Bot Token: ${botToken.substring(0, 10)}...`);

serve({
  fetch: app.fetch,
  port,
}, (info) => {
  console.log(`✅ Server is running on http://localhost:${info.port}`);

  // 设置webhook（如果配置了webhook URL）
  if (process.env.WEBHOOK_URL) {
    bot.api.setWebhook(process.env.WEBHOOK_URL, {
      secret_token: webhookSecret,
    }).then(() => {
      console.log(`🔗 Webhook set to: ${process.env.WEBHOOK_URL}`);
    }).catch((error) => {
      console.error('❌ Failed to set webhook:', error);
    });
  } else {
    // 开发模式下，删除webhook并启用polling
    bot.api.deleteWebhook().then(() => {
      console.log('🔄 Webhook deleted, starting polling mode');
      bot.start();
    }).catch((error) => {
      console.error('❌ Failed to delete webhook:', error);
    });
  }
});

// 优雅关闭
process.on('SIGINT', () => {
  console.log('\n🛑 Shutting down gracefully...');
  bot.stop();
  process.exit(0);
});

process.on('SIGTERM', () => {
  console.log('\n🛑 Received SIGTERM, shutting down gracefully...');
  bot.stop();
  process.exit(0);
});