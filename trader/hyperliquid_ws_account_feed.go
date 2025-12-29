package trader

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/sonirico/go-hyperliquid"
	"nofx/hyperws"
)

// accountFeed 通过 WS 订阅 allDexsClearinghouseState，缓存多 dex 账户状态，减少 HTTP 调用。
type accountFeed struct {
	mu        sync.RWMutex
	byDex     map[string]hyperliquid.UserState
	updatedAt time.Time
}

var accountFeedSingleton struct {
	once sync.Once
	feed *accountFeed
}

func getAccountFeed() *accountFeed {
	accountFeedSingleton.once.Do(func() {
		accountFeedSingleton.feed = &accountFeed{
			byDex: make(map[string]hyperliquid.UserState),
		}
	})
	return accountFeedSingleton.feed
}

// startAccountFeed 在后台订阅 allDexsClearinghouseState。
func startAccountFeed(ctx context.Context, wallet string, testnet bool) {
	f := getAccountFeed()

	go func() {
		url := "wss://api.hyperliquid.xyz/ws"
		if testnet {
			url = "wss://api.hyperliquid-testnet.xyz/ws"
		}
		man := hyperws.Get(url)
		payload := map[string]interface{}{
			"method": "subscribe",
			"subscription": map[string]interface{}{
				"type": "allDexsClearinghouseState",
				"user": strings.ToLower(wallet),
			},
		}
		man.Subscribe("allDexsClearinghouseState", payload, func(_ string, data json.RawMessage) {
			f.handleAllDexsMsg(data)
		})
	}()
}

func (f *accountFeed) handleAllDexsMsg(data json.RawMessage) {
	var payload struct {
		User                string              `json:"user"`
		ClearinghouseStates [][]json.RawMessage `json:"clearinghouseStates"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return
	}

	now := time.Now()
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, pair := range payload.ClearinghouseStates {
		if len(pair) != 2 {
			continue
		}
		var dex string
		if err := json.Unmarshal(pair[0], &dex); err != nil {
			continue
		}
		var state hyperliquid.UserState
		if err := json.Unmarshal(pair[1], &state); err != nil {
			continue
		}
		f.byDex[dex] = state
	}
	f.updatedAt = now
}

// getUserState 从缓存获取指定 dex 的 state；freshWithin 表示可接受的最新时间窗口。
func (f *accountFeed) getUserState(dex string, freshWithin time.Duration) (*hyperliquid.UserState, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if f.updatedAt.IsZero() || time.Since(f.updatedAt) > freshWithin {
		return nil, false
	}
	state, ok := f.byDex[dex]
	if !ok {
		return nil, false
	}
	return &state, true
}
