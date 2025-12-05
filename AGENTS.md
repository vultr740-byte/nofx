# Repository Guidelines

## Project Structure & Module Organization
- `main.go`, `manager/`, `market/`, `trader/`, `utils/`: Go services and trading logic for agents, risk, and execution.
- `telegram/`, `auth/`, `api/`: Integrations for Telegram bot, auth flows, and API handlers.
- `web/`: React + TypeScript client (Vite). UI components live in `web/src`, assets in `web/public`, build output in `web/dist`.
- `docs/`, `prompts/`, `decision_logs/`: Product docs, LLM prompt assets, and recorded agent decisions.
- Tests: Go tests are colocated with source (`*_test.go`); front-end tests live in `web/src` and use Vitest/RTL.

## Build, Test, and Development Commands
- Backend (Go): `go test ./...` runs all Go unit/integration tests; `go build -o nofx main.go` builds the service binary; `go run main.go` starts the core services.
- Frontend (web): `cd web && pnpm install` once; `pnpm dev` launches Vite dev server; `pnpm build` produces production assets; `pnpm test` runs Vitest suite.
- Lint/format (web): `pnpm lint` (ESLint), `pnpm format:check` (Prettier).

## Coding Style & Naming Conventions
- Go: gofmt/goimports defaults; prefer small, composable functions; errors wrapped with context; use `ctx` for context plumbing.
- TypeScript/React: ES modules, functional components with hooks; PascalCase components, camelCase utilities; keep props typed and avoid `any`.
- Indentation: tabs for Go, 2 spaces for TS/JS/JSON; keep lines focused and avoid long parameter lists.
- Config: sample configs in `.env.example` and `config.json.example`; avoid committing secrets.

## Testing Guidelines
- Go: name tests `TestXxx` in `*_test.go`; use table-driven tests where possible; validate trading/risk paths deterministically.
- Web: Vitest + React Testing Library in `web/src/**/__tests__` or `*.test.tsx`; prefer testing user-visible behavior over implementation details.
- Aim for deterministic tests; avoid network dependencies in unit tests.

## Commit & Pull Request Guidelines
- Before committing, ensure builds/tests pass: `go build -o nofx main.go && go test ./...`; for web changes also run `pnpm build && pnpm test`.
- Commits: concise, imperative summaries (e.g., `Add HL order precision guard`); group related changes; keep noise low.
- PRs: include purpose, scope, and testing notes (`go test ./...`, `pnpm test`); add screenshots/GIFs for UI changes; reference issues or decisions when relevant.
- Keep diffs focused; flag migrations, config changes, or breaking API updates in the description.

## Security & Configuration Tips
- Secrets: never commit keys; load via environment or `config.json` with local overrides only.
- RPC/API keys: store per environment; rotate regularly.
- When adding new integrations (exchanges/agents), ensure error handling does not leak sensitive data into logs.
