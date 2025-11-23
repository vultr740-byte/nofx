# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Essential Commands

### Build & Run
```bash
# Build Go backend
go build -o nofx .

# Run backend (from project root)
./nofx

# Install and run frontend (in separate terminal)
cd web && pnpm install && pnpm run dev

# Docker deployment (recommended)
./start.sh start --build
```

### Development
```bash
# Frontend development commands (in web/ directory)
pnpm run dev          # Start development server
pnpm run build        # Production build
pnpm run lint         # Run ESLint
pnpm run lint:fix     # Fix linting issues
pnpm run format       # Format with Prettier
pnpm run test         # Run tests with Vitest

# Go tests
go test ./crypto/     # Run encryption tests
go test ./decision/   # Run decision engine tests
```

### Project Management
```bash
# Docker management
./start.sh status     # Check container status
./start.sh logs       # View logs
./start.sh stop       # Stop services
./start.sh restart    # Restart services
```

## Architecture Overview

### High-Level System Design
NOFX is a **multi-agent AI trading operating system** with a unified architecture supporting multiple exchanges and AI models.

**Core Components:**
1. **AI Decision Engine** (`decision/`) - DeepSeek/Qwen integration with self-learning
2. **Multi-Exchange Layer** (`trader/`, `exchange/`) - Unified abstraction for Binance/Hyperliquid/Aster
3. **Real-time Market Data** (`market/`) - WebSocket streams + technical indicators
4. **Risk Management** (`manager/`) - Position limits, margin control, PnL tracking
5. **Web Interface** (`web/`) - React dashboard with real-time monitoring
6. **Database Layer** (`database/`) - SQLite with trader/model/exchange configuration

### Key Architectural Patterns

**Database-Driven Configuration (v3.0.0+):**
- **No more JSON config files** - all configuration managed through web interface
- Separate tables for AI models, exchanges, traders, and system config
- RESTful API for CRUD operations on all entities

**Multi-Agent Competition:**
- Multiple AI traders can run simultaneously with separate accounts
- Real-time performance comparison and ranking
- Historical performance feedback for self-learning

**Exchange Abstraction:**
- Unified trader interface supporting multiple exchanges
- Exchange-specific implementations in `exchange/` directory
- Automatic precision handling and risk checks per exchange

**Data Flow Architecture:**
1. **Market Data Collection** → WebSocket streams (3m, 4h K-lines)
2. **Technical Analysis** → EMA, MACD, RSI, ATR calculations
3. **AI Decision Making** → Historical analysis + Chain of Thought reasoning
4. **Risk Validation** → Position limits, margin checks
5. **Order Execution** → Exchange-specific API calls
6. **Performance Tracking** → PnL calculation, win rate statistics

### Critical Implementation Details

**Time Dimension Management:**
- **4h K-lines**: 50 lines for long-term analysis (reduced from 100 for day trading optimization)
- **3m K-lines**: Real-time WebSocket data for immediate decisions
- **15m K-lines**: Used for technical calculations (via REST API fallback)
- **Cache Management**: Dynamic sizing based on timeframe (4h: 50, others: 100)

**Position Tracking:**
- Uses `symbol_side` format (e.g., "BTCUSDT_long") to prevent long/short conflicts
- Stores quantity, leverage, entry/exit times for accurate PnL
- Matches open/close pairs using unique keys

**Risk Controls:**
- Per-asset position limits (BTC/ETH: ≤10x equity, Altcoins: ≤1.5x equity)
- Configurable leverage limits (exchange-specific restrictions)
- Margin usage capped at 90%
- Mandatory ≥1:2 risk-reward ratio

**AI Self-Learning:**
- Analyzes last 20 trading cycles before each decision
- Tracks win rate, PnL ratio, coin-specific performance
- Avoids repeating losing patterns, reinforces successful strategies
- Historical feedback automatically injected into AI prompts

### File Structure Highlights

```
nofx/
├── decision/           # AI decision engine and prompts
├── trader/            # Exchange abstraction and order execution
├── manager/           # Trader lifecycle and risk management
├── market/            # Real-time data and technical indicators
├── exchange/          # Exchange-specific implementations
├── telegram/          # Telegram bot integration
├── database/          # SQLite schema and operations
├── web/               # React frontend (TypeScript + Vite)
├── prompts/           # AI prompt templates
└── decision_logs/     # Historical decision records
```

### Configuration Management

**Database Schema:**
- `ai_models`: DeepSeek/Qwen/custom model configurations
- `exchanges`: Binance/Hyperliquid/Aster API credentials
- `traders`: Combined AI model + exchange configurations
- `trading_coins`: Per-trader coin selections

**Web Interface APIs:**
- `/api/models` - AI model CRUD
- `/api/exchanges` - Exchange configuration CRUD
- `/api/traders` - Trader management and control
- `/api/status` - Real-time trading data

### Testing Strategy

- **Unit Tests**: Go test files for core logic (`*_test.go`)
- **Frontend Tests**: Vitest + React Testing Library
- **Integration Tests**: Real exchange API interactions (testnet)
- **Performance Tests**: Market data processing and decision latency

### Development Workflow

1. **Local Development**: Use `./start.sh` for Docker or manual backend/frontend startup
2. **Configuration**: Use web interface at `http://localhost:3000`
3. **Testing**: Test with small amounts on exchange testnets first
4. **Deployment**: Docker Compose for production environments

### Important Considerations

- **Security**: API keys stored in database, never expose in logs
- **Performance**: K-line data optimized for day trading (50 lines for 4h)
- **Rate Limits**: Built-in exchange API rate limiting
- **Error Handling**: Comprehensive retry logic and fallback mechanisms
- **Data Persistence**: SQLite database with proper foreign key constraints