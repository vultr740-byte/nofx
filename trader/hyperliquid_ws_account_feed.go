package trader

import (
	"context"
	"encoding/json"
	"log"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sonirico/go-hyperliquid"
)

// accountFeed 通过 WS 订阅 allDexsClearinghouseState，缓存多 dex 账户状态，减少 HTTP 调用。
type accountFeed struct {
	mu        sync.RWMutex
	byDex     map[string]hyperliquid.UserState
	updatedAt time.Time
	conn      *websocket.Conn
	done      chan struct{}
}

var accountFeedSingleton struct {
	once sync.Once
	feed *accountFeed
}

func getAccountFeed() *accountFeed {
	accountFeedSingleton.once.Do(func() {
		accountFeedSingleton.feed = &accountFeed{
			byDex: make(map[string]hyperliquid.UserState),
			done:  make(chan struct{}),
		}
	})
	return accountFeedSingleton.feed
}

// startAccountFeed 在后台订阅 allDexsClearinghouseState。
func startAccountFeed(ctx context.Context, wallet string, testnet bool) {
	f := getAccountFeed()

	go func() {
		base := "wss://api.hyperliquid.xyz/ws"
		if testnet {
			base = "wss://api.hyperliquid-testnet.xyz/ws"
		}
		u, _ := url.Parse(base)
		dialer := websocket.Dialer{
			HandshakeTimeout: 10 * time.Second,
		}
		for {
			conn, _, err := dialer.Dial(u.String(), nil)
			if err != nil {
				log.Printf("⚠️ accountFeed WS 连接失败: %v", err)
				time.Sleep(2 * time.Second)
				continue
			}
			f.mu.Lock()
			f.conn = conn
			f.mu.Unlock()

			sub := map[string]interface{}{
				"method": "subscribe",
				"subscription": map[string]interface{}{
					"type": "allDexsClearinghouseState",
					"user": strings.ToLower(wallet),
				},
			}
			if err := conn.WriteJSON(sub); err != nil {
				log.Printf("⚠️ accountFeed 订阅失败: %v", err)
				conn.Close()
				time.Sleep(2 * time.Second)
				continue
			}
			log.Printf("✅ accountFeed 已订阅 allDexsClearinghouseState")

			// 读循环
			for {
				_, msg, err := conn.ReadMessage()
				if err != nil {
					log.Printf("⚠️ accountFeed 读失败: %v", err)
					conn.Close()
					break
				}
				var envelope struct {
					Channel string          `json:"channel"`
					Data    json.RawMessage `json:"data"`
				}
				if err := json.Unmarshal(msg, &envelope); err != nil {
					continue
				}
				if envelope.Channel != "allDexsClearinghouseState" {
					continue
				}
				f.handleAllDexsMsg(envelope.Data)
			}
			// 重连
			select {
			case <-ctx.Done():
				return
			default:
			}
			time.Sleep(2 * time.Second)
		}
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
