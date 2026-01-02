package trader

import (
	"log"
	"strconv"
	"sync"
	"time"

	hyperliquid "github.com/sonirico/go-hyperliquid"
)

const (
	allMidsTTL       = 1500 * time.Millisecond
	clearinghouseTTL = 15 * time.Second
)

// hyperliquidWSManager 基于 SDK 的 WS 客户端：allMids + allDexsClearinghouseState
type hyperliquidWSManager struct {
	client *hyperliquid.WebsocketClient

	// allMids 缓存：dex -> coin -> mid
	allMids  map[string]map[string]float64
	lastMids map[string]time.Time
	midsMu   sync.RWMutex

	// clearinghouse 缓存：dex -> state
	chState   map[string]*hyperliquid.ClearinghouseState
	chUpdated map[string]time.Time
	chMu      sync.RWMutex

	subscribedUsers map[string]struct{}
	subMu           sync.Mutex
}

var (
	wsManagerMainnet *hyperliquidWSManager
	wsManagerTestnet *hyperliquidWSManager
	wsOnce           sync.Once
)

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

func newHyperliquidWSManager(testnet bool) *hyperliquidWSManager {
	url := hyperliquid.MainnetAPIURL
	if testnet {
		url = hyperliquid.TestnetAPIURL
	}
	ws := hyperliquid.NewWebsocketClient(url)
	if err := ws.Connect(nil); err != nil {
		log.Printf("⚠️ Hyperliquid WS 连接失败: %v", err)
	}

	m := &hyperliquidWSManager{
		client:          ws,
		allMids:         make(map[string]map[string]float64),
		lastMids:        make(map[string]time.Time),
		chState:         make(map[string]*hyperliquid.ClearinghouseState),
		chUpdated:       make(map[string]time.Time),
		subscribedUsers: make(map[string]struct{}),
	}
	m.subscribeAllMids()
	return m
}

// 订阅 allMids (默认 + 常见 dex)
func (m *hyperliquidWSManager) subscribeAllMids() {
	if m.client == nil {
		return
	}
	dexes := []string{"", "xyz", "flx", "vntl", "hyna"}
	for _, dex := range dexes {
		d := dex
		_, err := m.client.AllMids(hyperliquid.AllMidsSubscriptionParams{Dex: optDexPtr(d)}, func(am hyperliquid.AllMids, err error) {
			if err != nil {
				log.Printf("⚠️ WS allMids 回调错误(dex=%s): %v", d, err)
				return
			}
			mids := make(map[string]float64, len(am.Mids))
			for k, v := range am.Mids {
				if f, err := strconv.ParseFloat(v, 64); err == nil {
					mids[k] = f
				}
			}
			m.midsMu.Lock()
			m.allMids[d] = mids
			m.lastMids[d] = time.Now()
			m.midsMu.Unlock()
		})
		if err != nil {
			log.Printf("⚠️ WS allMids 订阅失败(dex=%s): %v", dex, err)
		}
	}
}

// SubscribeAccount 订阅全 dex 账户状态
func (m *hyperliquidWSManager) SubscribeAccount(user string) {
	if m.client == nil || user == "" {
		return
	}
	m.subMu.Lock()
	if _, ok := m.subscribedUsers[user]; ok {
		m.subMu.Unlock()
		return
	}
	m.subscribedUsers[user] = struct{}{}
	m.subMu.Unlock()

	_, err := m.client.AllDexsClearinghouseState(
		hyperliquid.AllDexsClearinghouseStateSubscriptionParams{User: user},
		func(data hyperliquid.AllDexsClearinghouseState, err error) {
			if err != nil {
				log.Printf("⚠️ WS allDexsClearinghouseState 回调错误: %v", err)
				return
			}
			now := time.Now()
			m.chMu.Lock()
			for _, item := range data.ClearinghouseStates {
				m.chState[item.Dex] = &item.State
				m.chUpdated[item.Dex] = now
			}
			m.chMu.Unlock()
		},
	)
	if err != nil {
		log.Printf("⚠️ WS allDexsClearinghouseState 订阅失败: %v", err)
	}
}

func (m *hyperliquidWSManager) getAllMids(ttl time.Duration, dex string) (map[string]float64, bool) {
	if m == nil {
		return nil, false
	}
	m.midsMu.RLock()
	defer m.midsMu.RUnlock()
	last, ok := m.lastMids[dex]
	if !ok || time.Since(last) > ttl {
		return nil, false
	}
	src := m.allMids[dex]
	if len(src) == 0 {
		return nil, false
	}
	cp := make(map[string]float64, len(src))
	for k, v := range src {
		cp[k] = v
	}
	return cp, true
}

func (m *hyperliquidWSManager) getClearinghouseState(ttl time.Duration, dex string) (*hyperliquid.ClearinghouseState, bool) {
	if m == nil {
		return nil, false
	}
	m.chMu.RLock()
	defer m.chMu.RUnlock()
	last, ok := m.chUpdated[dex]
	if !ok || time.Since(last) > ttl {
		return nil, false
	}
	if st, ok := m.chState[dex]; ok {
		return st, true
	}
	return nil, false
}

func (m *hyperliquidWSManager) setClearinghouseState(dex string, st *hyperliquid.ClearinghouseState) {
	if m == nil || st == nil {
		return
	}
	m.chMu.Lock()
	m.chState[dex] = st
	m.chUpdated[dex] = time.Now()
	m.chMu.Unlock()
}

func optDexPtr(dex string) *string {
	if dex == "" {
		return nil
	}
	return &dex
}
