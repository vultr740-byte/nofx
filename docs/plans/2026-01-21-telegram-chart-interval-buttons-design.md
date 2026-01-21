# Telegram /chart Interval Buttons Design

## Goal
Add inline time-interval buttons (15m, 1h, 4h, 1d, 3d, 7d) under the /chart image. Tapping a button should replace the existing chart image in-place and highlight the currently selected interval with an icon.

## Scope
- Telegram bot command `/chart` only.
- Inline keyboard rendered under the chart message.
- Callback-driven interval switching with in-place media update.

## Approach
Use stateless callback data: `chart_interval|<telegramID>|<symbol>|<interval>`. This avoids server-side storage and survives restarts. A fixed whitelist of intervals is used for validation. The chart image is regenerated on each interval switch, and the original message is updated via `EditMessageMedia` plus updated caption/keyboard.

## Components
- `telegram/chart_handler.go`
  - Build and send chart photo with an inline keyboard.
  - Helper for building keyboard with selected interval icon.
  - Helper for building caption text to avoid duplication.
- `telegram/bot.go`
  - Extend callback router with a new `chart_interval` action.
  - Callback handler: validate user and params, regenerate chart, edit media + caption + keyboard.

## Data Flow
1. User runs `/chart BTC 15m` (interval optional, default 15m).
2. Bot validates user, symbol, interval; generates chart.
3. Bot sends photo + caption + inline keyboard (selected interval marked).
4. User taps a time button.
5. Bot answers callback quickly, validates parameters, regenerates chart, then edits existing message media and markup.

## Error Handling
- Reject unsupported symbols or intervals with user-friendly feedback.
- If `EditMessageMedia` fails, fall back to sending a new chart message and warn in logs.
- Always `answerCallbackQuery` to avoid Telegram timeout spinners.

## Testing
- Manual: `/chart BTC` and `/chart BTC 1h`, tap multiple intervals and confirm the message updates in-place, caption is consistent, and selected icon changes.
- Edge: invalid interval in callback data should be rejected gracefully.

## Notes
- Keep callback data short to avoid Telegram 64-byte limits.
- Use existing `esc()` HTML-escape helper for caption content.
