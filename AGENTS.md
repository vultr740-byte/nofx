# Repository Guidelines

## Project Structure & Module Organization
- `main.go`, `manager/`, `market/`, `trader/`, `utils/`: Go services and trading logic for agents, risk, and execution
- `telegram/`: Core Telegram bot integration with Hyperliquid trading, NL parsing, and session management
- `auth/`, `api/`: Authentication flows and API handlers  
- `web/`: React + TypeScript client (Vite). UI components in `web/src`, assets in `web/public`, build output in `web/dist`
- `docs/`, `prompts/`, `decision_logs/`: Product docs, LLM prompt assets, and recorded agent decisions
- `bootstrap/`, `config/`, `crypto/`, `logger/`: Core infrastructure modules
- `third_party/go-hyperliquid/`: Vendored Hyperliquid SDK with local modifications
- Tests: Go tests colocated with source (`*_test.go`); frontend tests in `web/src` using Vitest/RTL

## Build, Test, and Development Commands

### Backend (Go)
- `go run main.go` - Start the core services
- `go build -o nofx main.go` - Build the service binary  
- `go test ./...` - Run all Go unit/integration tests
- `go test -v ./package` - Run tests for specific package with verbose output
- `go test -run TestSpecificFunction ./package` - Run single test function
- `go test -count=1 ./package` - Disable test caching for fresh runs
- `go vet ./...` - Run static analysis (included in go test)

### Frontend (web)
- `cd web && pnpm install` - Install dependencies (once)
- `pnpm dev` - Launch Vite dev server (localhost:3000)
- `pnpm build` - Build production assets (TypeScript compile + Vite build)
- `pnpm preview` - Preview production build locally
- `pnpm test` - Run Vitest test suite
- `pnpm test --run` - Run tests once (no watch mode)
- `pnpm test path/to/test.test.tsx` - Run single test file
- `pnpm lint` - Run ESLint (TypeScript/React)
- `pnpm lint:fix` - Auto-fix ESLint issues
- `pnpm format` - Format code with Prettier
- `pnpm format:check` - Check code formatting
- **TypeScript**: Strict mode enabled; `noUnusedLocals` and `noUnusedParameters` enforced

### Pre-commit Validation
- Husky runs pre-commit hooks automatically
- Go: `go build -o nofx main.go && go test ./...`
- Web: `pnpm build && pnpm test && pnpm lint`
- PR Health Check: `./scripts/pr-check.sh` - Comprehensive analysis before submitting PRs

## Coding Style & Naming Conventions

### Go
- **Formatting**: Use gofmt/goimports defaults (tabs for indentation)
- **Functions**: Prefer small, composable functions with clear single responsibilities
- **Error Handling**: Wrap errors with context using `fmt.Errorf` or custom error types
- **Context**: Use `ctx` parameter for context plumbing throughout call chains
- **Naming**: PascalCase for exported, camelCase for unexported; descriptive variable names
- **Imports**: Group imports in three blocks: stdlib, third-party, local modules
- **Comments**: Use Chinese comments for business logic (as seen in codebase), English for technical docs
- **Testing**: Name tests `TestXxx` in `*_test.go`; use table-driven tests where possible
- **Telegram Integration**: Use `tgbotapi` package; implement proper session state management; handle HTML escape for user content

### TypeScript/React  
- **Modules**: ES modules with import/export syntax
- **Components**: Functional components with hooks; PascalCase component names
- **Props**: Always type props with interfaces; avoid `any` type
- **Utilities**: camelCase for utility functions and variables
- **Formatting**: 2 spaces for indentation (Prettier config)
- **Imports**: React imports first, then third-party, then local modules
- **State**: Use React hooks or Zustand for state management
- **Styling**: Tailwind CSS classes; inline styles only for dynamic values

### Configuration
- Sample configs in `.env.example` and `config.json.example`
- Never commit secrets or API keys
- Use environment variables for deployment-specific values
- **Telegram Bot**: Requires `TELEGRAM_BOT_TOKEN` in environment; bot auto-starts with main service

## Testing Guidelines

### Go Testing
- **Structure**: Tests colocated in `*_test.go` files
- **Naming**: `TestFunctionName` pattern describing what's being tested
- **Style**: Table-driven tests preferred for multiple scenarios
- **Determinism**: Avoid network dependencies in unit tests; mock external services
- **Coverage**: Focus on critical paths: trading logic, risk management, auth flows
- **Validation**: Test both success and error paths comprehensively

### Frontend Testing  
- **Framework**: Vitest + React Testing Library
- **Location**: `web/src/**/__tests__/` or `*.test.tsx` files
- **Focus**: Test user-visible behavior over implementation details
- **Patterns**: Test component rendering, user interactions, state changes
- **Mocking**: Use vi.mock for external dependencies and API calls

## Security & Configuration
- **Secrets**: Never commit keys; load via environment or `config.json` with local overrides
- **API Keys**: Store per environment; rotate regularly  
- **Error Handling**: Ensure error logging doesn't leak sensitive data
- **Input Validation**: Validate all user inputs, especially in trading operations
- **Encryption**: Use existing crypto package for sensitive data storage
- **Telegram Bot Security**: Agent Wallet mode - Agent Key for trading only (balance ~0), Main Wallet stores funds safely

## Import Organization

### Go Import Groups
```go
import (
    // Standard library
    "encoding/json"
    "fmt"
    "os"
    
    // Third-party packages  
    "github.com/gin-gonic/gin"
    "github.com/sirupsen/logrus"
    
    // Local modules
    "nofx/config"
    "nofx/market"
)
```

### TypeScript Import Groups
```typescript
// React & core
import React from 'react'
import { useState } from 'react'

// Third-party libraries
import { clsx } from 'clsx'
import { motion } from 'framer-motion'

// Local modules
import { useLanguage } from '../contexts/LanguageContext'
import { HeaderProps } from './types'
```

## Performance Considerations
- **Go**: Pre-compile regex patterns (as seen in decision/engine.go)
- **React**: Use React.memo for expensive components, useMemo for calculations
- **API**: Implement proper caching for market data and user sessions
- **Database**: Use connection pooling and proper indexing

## Docker & Deployment
- **Development**: `docker compose up -d` for local development
- **Configuration**: Mounts `config.json`, `config.db`, `decision_logs/`, `prompts/`, `secrets/`
- **Timezone**: Set via `NOFX_TIMEZONE` environment variable (defaults to Asia/Shanghai)
- **Backend Port**: Configurable via `NOFX_BACKEND_PORT` (defaults to 8080)

## Telegram Bot Development Guidelines

### Core Architecture
- **Bot Manager**: `telegram/bot.go` - Main bot orchestrator with `TelegramBotManager` struct
- **Hyperliquid Service**: `telegram/hyperliquid.go` - Trading operations integration
- **Session Management**: `telegram/session_state.go` - User conversation state handling
- **NL Parser**: `telegram/nl_parser.go` - Natural language command processing
- **Command Validator**: `telegram/command_validator.go` - Input validation and security

### Key Development Patterns
- **HTML Escaping**: Always use `esc()` function for user-generated content in messages
- **Session State**: Implement proper state machines for multi-step conversations
- **Error Handling**: Provide user-friendly error messages in Chinese/English
- **Security**: Validate all user inputs; implement rate limiting for sensitive operations
- **Async Operations**: Use goroutines for long-running operations with proper synchronization

### Bot Commands Structure
- `/start` - Account creation and welcome flow
- `/balance` - Balance inquiry with detailed breakdown
- `/positions` - Current positions display
- `/deposit` - USDC deposit address generation
- `/settings` - Configuration management and key export

### Security Best Practices
- **Agent Wallet Mode**: Separate trading keys (Agent Key) from storage keys (Main Wallet)
- **Balance Validation**: Always check Agent Wallet balance (~0) before allowing operations
- **Key Export**: Implement secure key export with user confirmation and timeout
- **Input Sanitization**: HTML escape all user content; validate numeric inputs

## Important Scripts & Automation
- **PR Health Check**: `./scripts/pr-check.sh` - Comprehensive PR analysis and suggestions
- **Encryption Setup**: `./scripts/setup_encryption.sh` - Initialize RSA encryption for secrets
- **Code Generation**: `./generate_beta_code.sh` - Generate beta access codes
- **Deployment**: `./deploy_encryption.sh` - Deploy encryption configuration

## Commit & Pull Request Guidelines
- **Pre-commit**: Ensure `go build -o nofx main.go && go test ./...` and `pnpm build && pnpm test` pass
- **Messages**: Concise, imperative summaries (e.g., "Add HL order precision guard")
- **Scope**: Group related changes; keep diffs focused
- **PR Description**: Include purpose, scope, testing notes, and screenshots for UI changes
- **Breaking Changes**: Flag migrations, config changes, or breaking API updates prominently