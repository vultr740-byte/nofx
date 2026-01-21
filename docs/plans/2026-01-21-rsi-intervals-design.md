# RSI Interval Output Update Design

## Goal
Update the prompt output to use RSI(14, 15m) instead of RSI(7, 15m), and add RSI(14, 1h) alongside existing RSI(14, 4h).

## Scope
- Market data computation in `market/data.go`
- Prompt formatting in `market.Format`
- Data struct fields in `market/types.go`

## Approach
The system already pulls 15m/1h/4h klines. We will compute RSI(14) for 15m and 1h, and keep the existing RSI(14) for 4h. The prompt text will be updated to show:
- `RSI(14, 15m)` in the intraday section
- `RSI(14, 1h)` in the hourly section (near the 1h close prices)
- `RSI(14, 4h)` unchanged in the longer-term section

If 1h klines are unavailable, the 1h RSI line should be omitted to avoid misleading zeros.

## Data Flow
1. `market.Get` loads klines for 15m/1h/4h.
2. RSI(14) is computed for 15m and 1h, stored on the Data struct.
3. `decision/engine.go` uses `market.Format` to build the prompt; RSI lines are rendered from the updated fields.

## Error Handling
- Existing 1h kline fetch already logs and continues; RSI(14, 1h) should be output only when data exists.
- No change to external API error behavior.

## Testing
- Unit test for `market.Format` output strings to ensure RSI(14, 15m) and RSI(14, 1h) are present when data exists.
- Run `go test ./...`.

