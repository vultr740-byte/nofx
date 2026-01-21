# Telegram /chart Interval Buttons Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add inline time-interval buttons under `/chart` results, updating the chart image in-place when users switch intervals.

**Architecture:** Build a reusable caption + keyboard helper for `/chart`, send it with the initial photo, and add a callback handler (`chart_interval`) that regenerates the chart and edits message media + markup. Use stateless callback data for symbol/interval.

**Tech Stack:** Go, tgbotapi (Telegram Bot API), existing chart generator in `nofx/charts`.

### Task 1: Add helper for chart caption + inline keyboard

**Files:**
- Modify: `telegram/chart_handler.go`
- Test: `telegram` manual validation

**Step 1: Write the failing test**

No automated tests exist for telegram inline keyboard. Skip.

**Step 2: Run test to verify it fails**

Skip.

**Step 3: Write minimal implementation**

Add helpers in `telegram/chart_handler.go`:
- `buildChartCaption(asset, interval string, entryPrice, stopLoss, takeProfit float64, entrySide string) string`
- `buildChartIntervalKeyboard(telegramID int64, symbol, interval string) tgbotapi.InlineKeyboardMarkup`
- `chartIntervals` whitelist: `[]string{"15m","1h","4h","1d","3d","7d"}`

**Step 4: Run test to verify it passes**

Run: `go test ./...`
Expected: PASS

**Step 5: Commit**

```bash
git add telegram/chart_handler.go

git commit -m "Add /chart interval keyboard helpers"
```

### Task 2: Add chart interval callback handler and in-place edit

**Files:**
- Modify: `telegram/bot.go`
- Modify: `telegram/chart_handler.go`

**Step 1: Write the failing test**

No automated tests exist for callback handlers. Skip.

**Step 2: Run test to verify it fails**

Skip.

**Step 3: Write minimal implementation**

- Add new case in `handleCallbackQuery` for `chart_interval`.
- Implement `handleChartIntervalCallback` in `telegram/chart_handler.go`:
  - Validate callback message exists.
  - Parse `symbol` and `interval` from `parts`.
  - Validate interval in whitelist.
  - Validate symbol via `market.ToBinanceSymbol`.
  - Re-run entry/stop/take profit lookup (reuse existing logic or refactor to helper).
  - Generate PNG with `charts.NewGenerator().BuildKlinePNG`.
  - Build `EditMessageMedia` with photo and updated caption/keyboard.
  - `answerCallbackQuery` with feedback (e.g., "已切换到 1h").
  - On edit failure, log and fall back to sending a new chart message.

**Step 4: Run test to verify it passes**

Run: `go test ./...`
Expected: PASS

**Step 5: Commit**

```bash
git add telegram/bot.go telegram/chart_handler.go

git commit -m "Enable /chart interval switching via inline buttons"
```

### Task 3: Manual verification

**Files:**
- None

**Step 1: Run local bot and test**

Run: `go run main.go`
Expected: bot starts

Manual:
- `/chart BTC` shows 15m buttons, selected marked with icon
- Tap 1h/4h/1d/3d/7d; chart image updates in place and selected icon changes

**Step 2: Record outcomes**

If manual check passes, proceed to finalize.

**Step 3: Commit**

No commit needed.
