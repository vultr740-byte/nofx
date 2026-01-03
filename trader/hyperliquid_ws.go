package trader

import (
	"context"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	hyperliquid "github.com/sonirico/go-hyperliquid"
)

// hyperliquidWSManager 提供基础的 allMids WS 订阅与缓存，减少 HTTP 调用。
type hyperliquidWSManager struct {
	client   *hyperliquid.WebsocketClient
	allMids  map[string]map[string]float64 // dex -> coin -> mid
	midsMu   sync.RWMutex
	lastMids map[string]time.Time // dex -> time

	tradesMu      sync.RWMutex
	lastTrades    map[string]float64   // coin -> last trade price
	lastTradesAt  map[string]time.Time // coin -> time
	tradeSubsOnce map[string]struct{}  // coin -> subscribed

	spotMu              sync.RWMutex
	spotUSDCByUser      map[string]float64   // user -> usdc total
	spotUpdatedAtByUser map[string]time.Time // user -> time
	spotSubsOnce        map[string]struct{}  // user -> subscribed
}

func newHyperliquidWSManager(testnet bool) *hyperliquidWSManager {
	url := hyperliquid.MainnetAPIURL
	if testnet {
		url = hyperliquid.TestnetAPIURL
	}

	ws := hyperliquid.NewWebsocketClient(url)
	ctx := context.Background()
	m := &hyperliquidWSManager{
		client:              ws,
		allMids:             make(map[string]map[string]float64),
		lastMids:            make(map[string]time.Time),
		lastTrades:          make(map[string]float64),
		lastTradesAt:        make(map[string]time.Time),
		tradeSubsOnce:       make(map[string]struct{}),
		spotUSDCByUser:      make(map[string]float64),
		spotUpdatedAtByUser: make(map[string]time.Time),
		spotSubsOnce:        make(map[string]struct{}),
	}

	if err := ws.Connect(ctx); err != nil {
		log.Printf("⚠️ Hyperliquid WS 连接失败: %v", err)
		m.client = nil
		return m
	}

	// 订阅默认 perp 及常见 dex 列表
	dexes := []string{"", "xyz", "flx", "vntl", "hyna"}
	for _, dex := range dexes {
		d := dex // capture
		_, err := ws.AllMids(hyperliquid.AllMidsSubscriptionParams{Dex: optDexPtr(d)}, func(am hyperliquid.AllMids, err error) {
			if err != nil {
				log.Printf("⚠️ Hyperliquid WS allMids 回调错误(dex=%s): %v", d, err)
				return
			}
			mids := make(map[string]float64, len(am.Mids))
			for k, v := range am.Mids {
				if f, err := strconv.ParseFloat(v, 64); err == nil {
					mids[k] = f
				}
			}
			key := d
			m.midsMu.Lock()
			if m.allMids == nil {
				m.allMids = make(map[string]map[string]float64)
			}
			m.allMids[key] = mids
			m.lastMids[key] = time.Now()
			m.midsMu.Unlock()
		})
		if err != nil {
			log.Printf("⚠️ Hyperliquid WS allMids 订阅失败(dex=%s): %v", dex, err)
		}
	}

	return m
}

// getAllMids 返回缓存的 allMids 及是否新鲜（TTL 内）
func (m *hyperliquidWSManager) getAllMids(ttl time.Duration, dex string) (map[string]float64, bool) {
	if m == nil || m.client == nil {
		return nil, false
	}
	key := dex
	m.midsMu.RLock()
	defer m.midsMu.RUnlock()
	last, ok := m.lastMids[key]
	if !ok || time.Since(last) > ttl {
		return nil, false
	}
	src, ok := m.allMids[key]
	if !ok || len(src) == 0 {
		return nil, false
	}
	mids := make(map[string]float64, len(src))
	for k, v := range src {
		mids[k] = v
	}
	return mids, true
}

// getLastTradePrice 返回指定币种最近成交价（在 TTL 内）。若未订阅则自动订阅 trades。
func (m *hyperliquidWSManager) getLastTradePrice(ttl time.Duration, coin string) (float64, bool) {
	if m == nil || m.client == nil {
		return 0, false
	}

	// fast path: cached & fresh
	m.tradesMu.RLock()
	price, ok := m.lastTrades[coin]
	ts, okTs := m.lastTradesAt[coin]
	m.tradesMu.RUnlock()
	if ok && okTs && time.Since(ts) <= ttl {
		return price, true
	}

	// ensure subscription once
	m.tradesMu.Lock()
	if _, subbed := m.tradeSubsOnce[coin]; !subbed {
		coinCopy := coin
		_, err := m.client.Trades(
			hyperliquid.TradesSubscriptionParams{Coin: coinCopy},
			func(trades []hyperliquid.Trade, err error) {
				if err != nil || len(trades) == 0 {
					return
				}
				// 取数组最后一个视为最新成交
				last := trades[len(trades)-1]
				if px, err2 := strconv.ParseFloat(last.Px, 64); err2 == nil {
					m.tradesMu.Lock()
					m.lastTrades[coinCopy] = px
					m.lastTradesAt[coinCopy] = time.Now()
					m.tradesMu.Unlock()
				}
			},
		)
		if err == nil {
			m.tradeSubsOnce[coin] = struct{}{}
		}
	}
	m.tradesMu.Unlock()

	// 再检查一次缓存（防止第一次订阅后立刻可用）
	m.tradesMu.RLock()
	price, ok = m.lastTrades[coin]
	ts, okTs = m.lastTradesAt[coin]
	m.tradesMu.RUnlock()
	if ok && okTs && time.Since(ts) <= ttl {
		return price, true
	}
	return 0, false
}

// getSpotUSDC 返回 WS webData2 中的现货 USDC 余额（total），在 TTL 内有效。
func (m *hyperliquidWSManager) getSpotUSDC(ttl time.Duration, user string) (float64, bool) {
	if m == nil || m.client == nil {
		return 0, false
	}

	userLower := strings.ToLower(user)

	// fast path: cached & fresh
	m.spotMu.RLock()
	val, okVal := m.spotUSDCByUser[userLower]
	ts, okTs := m.spotUpdatedAtByUser[userLower]
	m.spotMu.RUnlock()
	if okVal && okTs && !ts.IsZero() && time.Since(ts) <= ttl {
		return val, true
	}

	// ensure subscription once per user
	needSub := false
	m.spotMu.Lock()
	if m.spotSubsOnce == nil {
		m.spotSubsOnce = make(map[string]struct{})
	}
	if _, subbed := m.spotSubsOnce[userLower]; !subbed {
		m.spotSubsOnce[userLower] = struct{}{}
		needSub = true
	}
	m.spotMu.Unlock()

	if needSub {
		userKey := userLower // capture
		_, err := m.client.WebData2(
			hyperliquid.WebData2SubscriptionParams{User: userKey},
			func(wd hyperliquid.WebData2, err error) {
				if err != nil || wd.SpotState == nil {
					return
				}

				found := false
				usdc := 0.0
				for _, b := range wd.SpotState.Balances {
					if b.Coin == "USDC" {
						found = true
						if f, e := strconv.ParseFloat(b.Total, 64); e == nil {
							usdc = f
						}
						break
					}
				}

				now := time.Now()
				m.spotMu.Lock()
				if m.spotUSDCByUser == nil {
					m.spotUSDCByUser = make(map[string]float64)
				}
				if m.spotUpdatedAtByUser == nil {
					m.spotUpdatedAtByUser = make(map[string]time.Time)
				}

				// 若本次推送未包含 USDC，且之前有非零缓存，则不要将其覆盖成 0，避免间歇性“现货=0”抖动。
				prev := m.spotUSDCByUser[userKey]
				if found {
					m.spotUSDCByUser[userKey] = usdc
					m.spotUpdatedAtByUser[userKey] = now
				} else if prev == 0 {
					m.spotUSDCByUser[userKey] = 0
					m.spotUpdatedAtByUser[userKey] = now
				}
				m.spotMu.Unlock()
			},
		)
		if err != nil {
			// allow re-subscribe attempts later if subscription failed
			m.spotMu.Lock()
			delete(m.spotSubsOnce, userLower)
			m.spotMu.Unlock()
			return 0, false
		}
	}

	// check cache again
	m.spotMu.RLock()
	val, okVal = m.spotUSDCByUser[userLower]
	ts, okTs = m.spotUpdatedAtByUser[userLower]
	m.spotMu.RUnlock()
	if okVal && okTs && !ts.IsZero() && time.Since(ts) <= ttl {
		return val, true
	}
	return 0, false
}

// setSpotUSDC 强制写入某个用户的现货 USDC 缓存（用于划转后立即同步显示，避免短时间内旧值回填）。
func (m *hyperliquidWSManager) setSpotUSDC(user string, usdc float64) {
	if m == nil {
		return
	}
	userLower := strings.ToLower(user)
	now := time.Now()
	m.spotMu.Lock()
	if m.spotUSDCByUser == nil {
		m.spotUSDCByUser = make(map[string]float64)
	}
	if m.spotUpdatedAtByUser == nil {
		m.spotUpdatedAtByUser = make(map[string]time.Time)
	}
	m.spotUSDCByUser[userLower] = usdc
	m.spotUpdatedAtByUser[userLower] = now
	m.spotMu.Unlock()
}

var (
	wsManagerMainnet *hyperliquidWSManager
	wsManagerTestnet *hyperliquidWSManager
	wsOnce           sync.Once
)

// getWSManager 获取网络对应的 WS 管理器（单例）。
func getWSManager(testnet bool) *hyperliquidWSManager {
	wsOnce.Do(func() {
		wsManagerMainnet = newHyperliquidWSManager(false)
		wsManagerTestnet = newHyperliquidWSManager(true)
	})
	if testnet {
		return wsManagerTestnet
	}
	return wsManagerMainnet
}

// optDexPtr helper: empty string -> nil, else pointer to dex
func optDexPtr(dex string) *string {
	if dex == "" {
		return nil
	}
	return &dex
}
