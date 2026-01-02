#!/usr/bin/env node
/**
 * Quick Hyperliquid WS sanity check (no external deps; uses global WebSocket in Node 18+).
 *
 * Examples:
 *   node scripts/hl-ws-check.js --network mainnet --coin BTC
 *   node scripts/hl-ws-check.js --network testnet --coin SOL --timeout 20000
 */

const args = process.argv.slice(2);

function getArg(name, def) {
  const key = `--${name}`;
  const idx = args.indexOf(key);
  if (idx !== -1 && idx + 1 < args.length) return args[idx + 1];
  return def;
}

const network = getArg("network", "mainnet");
const coin = getArg("coin", "BTC");
const timeoutMs = Number(getArg("timeout", "15000"));
const url =
  network === "testnet"
    ? "wss://api.hyperliquid-testnet.xyz/ws"
    : "wss://api.hyperliquid.xyz/ws";

const targetAllMids = 3;
const targetTrades = 2;

let allMidsCount = 0;
let tradesCount = 0;
let subscribed = false;
let closed = false;

const ws = new WebSocket(url);

function done(ok, reason) {
  if (closed) return;
  closed = true;
  clearTimeout(timer);
  if (ok) {
    console.log(
      `OK | network=${network} coin=${coin} allMids=${allMidsCount} trades=${tradesCount}`
    );
    process.exit(0);
  } else {
    console.error(`FAIL | ${reason}`);
    process.exit(1);
  }
}

ws.onopen = () => {
  console.log(`opened ${url}`);
  ws.send(
    JSON.stringify({
      method: "subscribe",
      subscription: { type: "allMids" },
    })
  );
  ws.send(
    JSON.stringify({
      method: "subscribe",
      subscription: { type: "trades", coin },
    })
  );
};

ws.onerror = (err) => {
  done(false, `ws error: ${err.message || err}`);
};

ws.onclose = () => {
  if (!closed) done(false, "ws closed before completion");
};

ws.onmessage = (ev) => {
  let msg;
  try {
    msg = JSON.parse(ev.data);
  } catch (e) {
    return;
  }
  if (msg.channel === "subscriptionResponse") {
    subscribed = true;
    return;
  }
  if (msg.channel === "allMids") {
    allMidsCount += 1;
    if (allMidsCount === 1) {
      console.log(
        `allMids sample: ${Object.keys(msg.data.mids).slice(0, 5).join(",")}`
      );
    }
  }
  if (msg.channel === "trades") {
    tradesCount += 1;
    if (tradesCount === 1) {
      const t = msg.data[0];
      console.log(
        `trade sample: ${t.coin} ${t.side} px=${t.px} sz=${t.sz} time=${t.time}`
      );
    }
  }
  if (
    subscribed &&
    allMidsCount >= targetAllMids &&
    tradesCount >= targetTrades
  ) {
    done(true, "ok");
  }
};

const timer = setTimeout(() => {
  done(
    false,
    `timeout after ${timeoutMs}ms (subs=${subscribed} allMids=${allMidsCount} trades=${tradesCount})`
  );
}, timeoutMs);
