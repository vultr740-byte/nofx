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
	allMids  map[string]float64
	midsMu   sync.RWMutex
	lastMids time.Time
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
		return &hyperliquidWSManager{client: ws, allMids: make(map[string]float64)}
	}

	m := &hyperliquidWSManager{
		client:  ws,
		allMids: make(map[string]float64),
	}

	_, err := ws.AllMids(hyperliquid.AllMidsSubscriptionParams{Dex: nil}, func(am hyperliquid.AllMids, err error) {
		if err != nil {
			log.Printf("⚠️ Hyperliquid WS allMids 回调错误: %v", err)
			return
		}
		mids := make(map[string]float64, len(am.Mids))
		for k, v := range am.Mids {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				mids[k] = f
			}
		}
		m.midsMu.Lock()
		m.allMids = mids
		m.lastMids = time.Now()
		m.midsMu.Unlock()
	})
	if err != nil {
		log.Printf("⚠️ Hyperliquid WS allMids 订阅失败: %v", err)
	}

	return m
}

// getAllMids 返回缓存的 allMids 及是否新鲜（TTL 内）
func (m *hyperliquidWSManager) getAllMids(ttl time.Duration) (map[string]float64, bool) {
	if m == nil {
		return nil, false
	}
	m.midsMu.RLock()
	defer m.midsMu.RUnlock()
	if time.Since(m.lastMids) > ttl || len(m.allMids) == 0 {
		return nil, false
	}
	mids := make(map[string]float64, len(m.allMids))
	for k, v := range m.allMids {
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
