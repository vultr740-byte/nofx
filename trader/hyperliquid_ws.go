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

	userMu sync.Mutex
	users  map[string]*hyperliquidWSUserState // user(lower) -> state
}

type hyperliquidWSUserState struct {
	refCount int
	initCh   chan struct{}

	spotUSDC      float64
	spotUpdatedAt time.Time

	perpByDex     map[string]hyperliquid.ClearinghouseState
	perpUpdatedAt time.Time

	subWebData2    *hyperliquid.Subscription
	subAllDexsPerp *hyperliquid.Subscription
}

func newHyperliquidWSManager(testnet bool) *hyperliquidWSManager {
	url := hyperliquid.MainnetAPIURL
	if testnet {
		url = hyperliquid.TestnetAPIURL
	}

	ws := hyperliquid.NewWebsocketClient(url)
	ctx := context.Background()
	m := &hyperliquidWSManager{
		client:        ws,
		allMids:       make(map[string]map[string]float64),
		lastMids:      make(map[string]time.Time),
		lastTrades:    make(map[string]float64),
		lastTradesAt:  make(map[string]time.Time),
		tradeSubsOnce: make(map[string]struct{}),
		users:         make(map[string]*hyperliquidWSUserState),
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

// acquireUserWS 确保指定用户的钱包订阅已开启（按 refcount 幂等）。
// 约定：订阅生命周期由“交易员运行状态”驱动，非运行状态下不主动订阅。
func (m *hyperliquidWSManager) acquireUserWS(user string) {
	if m == nil || m.client == nil {
		return
	}
	userLower := strings.ToLower(strings.TrimSpace(user))
	if userLower == "" {
		return
	}

	m.userMu.Lock()
	state, ok := m.users[userLower]
	if !ok {
		state = &hyperliquidWSUserState{
			refCount:  1,
			perpByDex: make(map[string]hyperliquid.ClearinghouseState),
		}
		m.users[userLower] = state
	} else {
		state.refCount++
	}
	// already subscribed
	if state.subWebData2 != nil && state.subAllDexsPerp != nil {
		m.userMu.Unlock()
		return
	}
	// initialization in progress: wait for it
	if state.initCh != nil {
		ch := state.initCh
		m.userMu.Unlock()
		<-ch
		return
	}
	// start initialization
	state.initCh = make(chan struct{})
	ch := state.initCh
	m.userMu.Unlock()

	// 订阅 WebData2（含 SpotState）
	subWebData2, err := m.client.WebData2(
		hyperliquid.WebData2SubscriptionParams{User: userLower},
		func(wd hyperliquid.WebData2, err error) {
			if err != nil || wd.SpotState == nil {
				return
			}
			usdc := 0.0
			for _, b := range wd.SpotState.Balances {
				if b.Coin == "USDC" {
					if f, e := strconv.ParseFloat(b.Total, 64); e == nil {
						usdc = f
					}
					break
				}
			}
			m.userMu.Lock()
			if st, ok := m.users[userLower]; ok {
				st.spotUSDC = usdc
				st.spotUpdatedAt = time.Now()
			}
			m.userMu.Unlock()
		},
	)
	if err != nil {
		log.Printf("⚠️ Hyperliquid WS webData2 订阅失败(user=%s): %v", userLower, err)
		m.userMu.Lock()
		if st, ok := m.users[userLower]; ok {
			st.initCh = nil
		}
		close(ch)
		m.userMu.Unlock()
		return
	}

	// 订阅 allDexsClearinghouseState（Perp 账户状态）
	subAllDexs, err := m.client.AllDexsClearinghouseState(
		hyperliquid.AllDexsClearinghouseStateSubscriptionParams{User: userLower},
		func(msg hyperliquid.AllDexsClearinghouseState, err error) {
			if err != nil {
				return
			}
			m.userMu.Lock()
			st, ok := m.users[userLower]
			if !ok {
				m.userMu.Unlock()
				return
			}
			if st.perpByDex == nil {
				st.perpByDex = make(map[string]hyperliquid.ClearinghouseState)
			}
			for _, pair := range msg.ClearinghouseStates {
				st.perpByDex[pair.First] = pair.Second
			}
			st.perpUpdatedAt = time.Now()
			m.userMu.Unlock()
		},
	)
	if err != nil {
		log.Printf("⚠️ Hyperliquid WS allDexsClearinghouseState 订阅失败(user=%s): %v", userLower, err)
		subWebData2.Close()
		m.userMu.Lock()
		if st, ok := m.users[userLower]; ok {
			st.initCh = nil
		}
		close(ch)
		m.userMu.Unlock()
		return
	}

	m.userMu.Lock()
	if st, ok := m.users[userLower]; ok {
		st.subWebData2 = subWebData2
		st.subAllDexsPerp = subAllDexs
		st.initCh = nil
		close(ch)
		m.userMu.Unlock()
		return
	}
	m.userMu.Unlock()

	// User state was released during initialization; avoid leaking subscriptions.
	subWebData2.Close()
	subAllDexs.Close()
	close(ch)
}

// releaseUserWS 在 refcount 归零时关闭订阅并清理缓存。
func (m *hyperliquidWSManager) releaseUserWS(user string) {
	if m == nil {
		return
	}
	userLower := strings.ToLower(strings.TrimSpace(user))
	if userLower == "" {
		return
	}

	var toCloseWebData2 *hyperliquid.Subscription
	var toCloseAllDexs *hyperliquid.Subscription

	m.userMu.Lock()
	st, ok := m.users[userLower]
	if !ok {
		m.userMu.Unlock()
		return
	}
	st.refCount--
	if st.refCount > 0 {
		m.userMu.Unlock()
		return
	}
	toCloseWebData2 = st.subWebData2
	toCloseAllDexs = st.subAllDexsPerp
	delete(m.users, userLower)
	m.userMu.Unlock()

	if toCloseWebData2 != nil {
		toCloseWebData2.Close()
	}
	if toCloseAllDexs != nil {
		toCloseAllDexs.Close()
	}
}

// getSpotUSDC 返回指定用户的 Spot USDC 缓存（需要先 acquireUserWS），在 TTL 内有效。
func (m *hyperliquidWSManager) getSpotUSDC(ttl time.Duration, user string) (float64, bool) {
	if m == nil {
		return 0, false
	}
	userLower := strings.ToLower(strings.TrimSpace(user))
	if userLower == "" {
		return 0, false
	}

	m.userMu.Lock()
	st := m.users[userLower]
	var val float64
	var ts time.Time
	if st != nil {
		val = st.spotUSDC
		ts = st.spotUpdatedAt
	}
	m.userMu.Unlock()
	if st == nil || ts.IsZero() || time.Since(ts) > ttl {
		return 0, false
	}
	return val, true
}

// getPerpClearinghouseState 返回指定用户指定 dex 的 Perp clearinghouseState 缓存（需要先 acquireUserWS），在 TTL 内有效。
func (m *hyperliquidWSManager) getPerpClearinghouseState(ttl time.Duration, user string, dex string) (*hyperliquid.ClearinghouseState, bool) {
	if m == nil {
		return nil, false
	}
	userLower := strings.ToLower(strings.TrimSpace(user))
	if userLower == "" {
		return nil, false
	}

	m.userMu.Lock()
	st := m.users[userLower]
	var ts time.Time
	var state hyperliquid.ClearinghouseState
	var ok bool
	if st != nil {
		ts = st.perpUpdatedAt
		state, ok = st.perpByDex[dex]
	}
	m.userMu.Unlock()
	if st == nil || ts.IsZero() || time.Since(ts) > ttl {
		return nil, false
	}
	if !ok {
		return nil, false
	}
	// return a copy so callers can't mutate internal cache
	s := state
	return &s, true
}

var (
	wsManagerMainnet *hyperliquidWSManager
	wsManagerTestnet *hyperliquidWSManager
	wsMainOnce       sync.Once
	wsTestOnce       sync.Once
)

// getWSManager 获取网络对应的 WS 管理器（单例）。
func getWSManager(testnet bool) *hyperliquidWSManager {
	if testnet {
		wsTestOnce.Do(func() {
			wsManagerTestnet = newHyperliquidWSManager(true)
		})
		return wsManagerTestnet
	}
	wsMainOnce.Do(func() {
		wsManagerMainnet = newHyperliquidWSManager(false)
	})
	return wsManagerMainnet
}

// optDexPtr helper: empty string -> nil, else pointer to dex
func optDexPtr(dex string) *string {
	if dex == "" {
		return nil
	}
	return &dex
}
