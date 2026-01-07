package trader

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	hyperliquid "github.com/sonirico/go-hyperliquid"
	"golang.org/x/sync/singleflight"
)

// 统一节流 Hyperliquid /info 类 HTTP 请求，避免启动阶段/多交易员并发触发 429。
// 这里做“全局串行 + 最小间隔”的保守策略：每 500ms 最多放行 1 个请求。
var hyperliquidInfoLimiter = newTokenBucket(1, 500*time.Millisecond)

type tokenBucket struct {
	tokens chan struct{}
}

func newTokenBucket(burst int, interval time.Duration) *tokenBucket {
	if burst <= 0 {
		burst = 1
	}
	if interval <= 0 {
		interval = 500 * time.Millisecond
	}
	b := &tokenBucket{tokens: make(chan struct{}, burst)}
	for i := 0; i < burst; i++ {
		b.tokens <- struct{}{}
	}
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for range t.C {
			select {
			case b.tokens <- struct{}{}:
			default:
			}
		}
	}()
	return b
}

func (b *tokenBucket) Wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-b.tokens:
		return nil
	}
}

type hyperliquidOrdersCacheEntry struct {
	mu         sync.Mutex
	frontendAt time.Time
	frontend   []hyperliquid.FrontendOpenOrder

	openAt time.Time
	open   []hyperliquid.OpenOrder
}

var hyperliquidOrdersCache = struct {
	entries sync.Map // key -> *hyperliquidOrdersCacheEntry
	group   singleflight.Group
}{}

func ordersCacheKey(testnet bool, walletAddr string) string {
	return fmt.Sprintf("%t:%s", testnet, strings.ToLower(strings.TrimSpace(walletAddr)))
}

func getOrdersCacheEntry(testnet bool, walletAddr string) *hyperliquidOrdersCacheEntry {
	key := ordersCacheKey(testnet, walletAddr)
	if v, ok := hyperliquidOrdersCache.entries.Load(key); ok {
		return v.(*hyperliquidOrdersCacheEntry)
	}
	e := &hyperliquidOrdersCacheEntry{}
	v, _ := hyperliquidOrdersCache.entries.LoadOrStore(key, e)
	return v.(*hyperliquidOrdersCacheEntry)
}

func copyFrontendOrders(src []hyperliquid.FrontendOpenOrder) []hyperliquid.FrontendOpenOrder {
	if src == nil {
		return nil
	}
	return append([]hyperliquid.FrontendOpenOrder(nil), src...)
}

func copyOpenOrders(src []hyperliquid.OpenOrder) []hyperliquid.OpenOrder {
	if src == nil {
		return nil
	}
	return append([]hyperliquid.OpenOrder(nil), src...)
}

func (t *HyperliquidTrader) getFrontendOpenOrdersCached(ttl time.Duration) ([]hyperliquid.FrontendOpenOrder, error) {
	entry := getOrdersCacheEntry(t.testnet, t.walletAddr)

	entry.mu.Lock()
	ts := entry.frontendAt
	cached := copyFrontendOrders(entry.frontend)
	entry.mu.Unlock()
	if !ts.IsZero() && time.Since(ts) <= ttl {
		return cached, nil
	}

	key := "frontend:" + ordersCacheKey(t.testnet, t.walletAddr)
	v, err, _ := hyperliquidOrdersCache.group.Do(key, func() (any, error) {
		// double-check after collapsing
		entry.mu.Lock()
		ts := entry.frontendAt
		cached := copyFrontendOrders(entry.frontend)
		entry.mu.Unlock()
		if !ts.IsZero() && time.Since(ts) <= ttl {
			return cached, nil
		}

		if err := hyperliquidInfoLimiter.Wait(t.ctx); err != nil {
			return nil, err
		}

		orders, err := t.exchange.Info().FrontendOpenOrders(t.ctx, t.walletAddr)
		if err != nil {
			return nil, err
		}

		entry.mu.Lock()
		entry.frontendAt = time.Now()
		entry.frontend = copyFrontendOrders(orders)
		out := copyFrontendOrders(entry.frontend)
		entry.mu.Unlock()
		return out, nil
	})

	if err != nil {
		// 429/网络波动时优先返回缓存（哪怕略旧），避免影响 /positions 或决策上下文。
		const staleTTL = 2 * time.Minute
		entry.mu.Lock()
		ts := entry.frontendAt
		cached := copyFrontendOrders(entry.frontend)
		entry.mu.Unlock()
		if !ts.IsZero() && time.Since(ts) <= staleTTL {
			log.Printf("⚠️ FrontendOpenOrders 获取失败，返回缓存(≤%.0fs): %v", staleTTL.Seconds(), err)
			return cached, nil
		}
		return nil, err
	}

	out, _ := v.([]hyperliquid.FrontendOpenOrder)
	return out, nil
}

func (t *HyperliquidTrader) getOpenOrdersCached(ttl time.Duration) ([]hyperliquid.OpenOrder, error) {
	entry := getOrdersCacheEntry(t.testnet, t.walletAddr)

	entry.mu.Lock()
	ts := entry.openAt
	cached := copyOpenOrders(entry.open)
	entry.mu.Unlock()
	if !ts.IsZero() && time.Since(ts) <= ttl {
		return cached, nil
	}

	key := "open:" + ordersCacheKey(t.testnet, t.walletAddr)
	v, err, _ := hyperliquidOrdersCache.group.Do(key, func() (any, error) {
		// double-check after collapsing
		entry.mu.Lock()
		ts := entry.openAt
		cached := copyOpenOrders(entry.open)
		entry.mu.Unlock()
		if !ts.IsZero() && time.Since(ts) <= ttl {
			return cached, nil
		}

		if err := hyperliquidInfoLimiter.Wait(t.ctx); err != nil {
			return nil, err
		}

		orders, err := t.exchange.Info().OpenOrders(t.ctx, t.walletAddr)
		if err != nil {
			return nil, err
		}

		entry.mu.Lock()
		entry.openAt = time.Now()
		entry.open = copyOpenOrders(orders)
		out := copyOpenOrders(entry.open)
		entry.mu.Unlock()
		return out, nil
	})

	if err != nil {
		const staleTTL = 2 * time.Minute
		entry.mu.Lock()
		ts := entry.openAt
		cached := copyOpenOrders(entry.open)
		entry.mu.Unlock()
		if !ts.IsZero() && time.Since(ts) <= staleTTL {
			log.Printf("⚠️ OpenOrders 获取失败，返回缓存(≤%.0fs): %v", staleTTL.Seconds(), err)
			return cached, nil
		}
		return nil, err
	}

	out, _ := v.([]hyperliquid.OpenOrder)
	return out, nil
}
