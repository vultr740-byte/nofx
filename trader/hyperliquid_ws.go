package trader

import (
	"context"
	"log"
	"strconv"
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
}

func newHyperliquidWSManager(testnet bool) *hyperliquidWSManager {
	url := hyperliquid.MainnetAPIURL
	if testnet {
		url = hyperliquid.TestnetAPIURL
	}

	ws := hyperliquid.NewWebsocketClient(url)
	ctx := context.Background()
	if err := ws.Connect(ctx); err != nil {
		log.Printf("⚠️ Hyperliquid WS 连接失败: %v", err)
		return &hyperliquidWSManager{client: ws, allMids: make(map[string]map[string]float64), lastMids: make(map[string]time.Time)}
	}

	m := &hyperliquidWSManager{
		client:   ws,
		allMids:  make(map[string]map[string]float64),
		lastMids: make(map[string]time.Time),
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
	if m == nil {
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
