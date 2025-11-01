# NOFX Telegram Bot

Telegram bot for the NOFX AI Trading System, built with Hono and grammY.

## Features

- 🤖 **User Authentication**: Secure Telegram user identification
- 📊 **Trader Management**: Start, stop, and monitor AI traders
- 💰 **Real-time Data**: Live trading performance and status updates
- 🚀 **Quick Actions**: Fast start/stop controls with inline keyboards
- 📱 **User-friendly**: Simple command interface with helpful responses

## Architecture

```
Telegram Bot (Hono + grammY) ←→ HTTP API ←→ Go API Server ←→ Database
```

## Quick Start

### 1. Environment Setup

Copy the example environment file:
```bash
cp .env.example .env
```

Edit `.env` with your configuration:
```bash
# Telegram Bot Configuration
BOT_TOKEN=your_telegram_bot_token_here
WEBHOOK_SECRET=your_webhook_secret_here
WEBHOOK_URL=https://your-domain.com/webhook

# API Configuration
GO_API_BASE_URL=http://localhost:8080
BOT_API_TOKEN=your_bot_api_token_here
BOT_API_SECRET=your_bot_api_secret_here

# Server Configuration
PORT=3000
NODE_ENV=production
```

### 2. Local Development

Install dependencies:
```bash
npm install
```

Start in development mode:
```bash
npm run dev
```

### 3. Docker Deployment

Build and run with Docker:
```bash
docker build -t nofx-bot .
docker run -d --env-file .env -p 3000:3000 nofx-bot
```

Or with Docker Compose:
```bash
cp .env.example .env
# Edit .env with your configuration
docker-compose up -d
```

## Commands

- `/start` - Welcome message and introduction
- `/help` - Show all available commands
- `/status` - Check traders' status and performance
- `/list` - List all your traders
- `/create` - Create a new trader (coming soon)
- `/start_trader <id>` - Start a specific trader
- `/stop_trader <id>` - Stop a specific trader

## API Integration

The bot communicates with the Go API server through secured endpoints:

- **Authentication**: HMAC signature verification
- **User Isolation**: Each user can only access their own traders
- **Security**: Rate limiting and request validation

## Environment Variables

### Required
- `BOT_TOKEN` - Telegram bot token from @BotFather
- `WEBHOOK_SECRET` - Secret for webhook verification
- `GO_API_BASE_URL` - URL of the Go API server
- `BOT_API_TOKEN` - API token for Go API authentication
- `BOT_API_SECRET` - Secret for HMAC signature generation

### Optional
- `PORT` - Server port (default: 3000)
- `WEBHOOK_URL` - Full webhook URL for production
- `NODE_ENV` - Environment (development/production)
- `LOG_LEVEL` - Logging level (info/debug/error)

## Security Features

- **HMAC Signature Verification**: All API requests are signed
- **Timestamp Validation**: Prevents replay attacks
- **User Isolation**: Telegram users can only access their own data
- **Request Validation**: All inputs are validated and sanitized

## Monitoring

### Health Check
```bash
curl http://localhost:3000/health
```

### Logs
```bash
docker logs -f nofx-bot
```

## Development

### Project Structure
```
bot/
├── src/
│   ├── api/           # API client
│   ├── commands/      # Bot command handlers
│   ├── handlers/      # Message handlers
│   └── index.ts       # Main application
├── types/             # TypeScript type definitions
├── Dockerfile
├── docker-compose.yml
└── README.md
```

### Adding New Commands

1. Create a new file in `src/commands/`
2. Export a handler function
3. Import and register in `src/commands/index.ts`

Example:
```typescript
// src/commands/mycommand.ts
export async function handleMyCommand(ctx: Context, apiClient: ApiClient) {
  await ctx.reply('Hello from my command!');
}
```

## Deployment

### Production Deployment

1. Set all required environment variables
2. Configure webhook URL
3. Deploy with Docker Compose:
```bash
docker-compose -f docker-compose.yml up -d
```

### Webhook Setup

The bot supports both webhook and polling modes:
- **Production**: Webhook mode (recommended)
- **Development**: Polling mode (automatic)

## Troubleshooting

### Common Issues

1. **Bot token invalid**: Check your `BOT_TOKEN` from @BotFather
2. **API connection failed**: Verify `GO_API_BASE_URL` and API tokens
3. **Webhook not working**: Check `WEBHOOK_URL` is accessible and HTTPS
4. **Permission denied**: Verify API tokens and secrets match

### Debug Mode

Enable debug logging:
```bash
LOG_LEVEL=debug npm run dev
```

## Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Add tests if applicable
5. Submit a pull request

## License

MIT License - see LICENSE file for details.