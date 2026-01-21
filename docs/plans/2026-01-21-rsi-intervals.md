# RSI Interval Output Update Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Change 15m RSI to 14-period and add 1h RSI(14) to the prompt output, keeping existing 4h RSI.

**Architecture:** Update `market.Data` fields, compute RSI(14) for 15m and 1h in `market.Get`, and format them in `market.Format` with conditional output when 1h data exists. Add unit tests covering output strings.

**Tech Stack:** Go, market data formatting, standard Go testing.

### Task 1: Add/adjust Data fields and compute RSI(14)

**Files:**
- Modify: `market/types.go`
- Modify: `market/data.go`

**Step 1: Write the failing test**

Create a unit test for `market.Format` that expects `RSI(14, 15m)` and `RSI(14, 1h)`.

**Step 2: Run test to verify it fails**

Run: `go test ./market`
Expected: FAIL because output still includes RSI(7, 15m) and no 1h RSI.

**Step 3: Write minimal implementation**

- In `market/types.go`, rename `CurrentRSI7` to `CurrentRSI14` and add `CurrentRSI1h14`.
- In `market/data.go`, compute `currentRSI15m14 := calculateRSI(klines15m, 14)`.
- If 1h klines exist, compute `currentRSI1h14 := calculateRSI(klines1h, 14)`.
- Populate `Data` fields with the new values.

**Step 4: Run test to verify it passes**

Run: `go test ./market`
Expected: PASS

**Step 5: Commit**

```bash
git add market/types.go market/data.go market/format_test.go

git commit -m "Update RSI periods and add 1h RSI" 
```

### Task 2: Update prompt formatting output

**Files:**
- Modify: `market/data.go`
- Test: `market/format_test.go`

**Step 1: Write the failing test**

Extend the same `market.Format` test to verify the exact output labels:
- `RSI(14, 15m)`
- `RSI(14, 1h)` (only when hourly data exists)

**Step 2: Run test to verify it fails**

Run: `go test ./market`
Expected: FAIL until output is updated.

**Step 3: Write minimal implementation**

- Replace the 15m line with `RSI(14, 15m)` using `CurrentRSI14`.
- Add a new line near the 1h section for `RSI(14, 1h)` when `HourlyContext` has data.

**Step 4: Run test to verify it passes**

Run: `go test ./market`
Expected: PASS

**Step 5: Commit**

```bash
git add market/data.go market/format_test.go

git commit -m "Render RSI(14) for 15m and 1h in prompt" 
```

### Task 3: Full test suite

**Files:**
- None

**Step 1: Run all tests**

Run: `go test ./...`
Expected: PASS (allowing existing ld search path warnings)

**Step 2: Commit**

No commit needed.
