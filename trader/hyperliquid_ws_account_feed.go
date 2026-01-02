package trader

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/sonirico/go-hyperliquid"
)

// accountFeed 通过 WS 订阅 allDexsClearinghouseState，缓存多 dex 账户状态，减少 HTTP 调用。
type accountFeed struct {
	mu        sync.RWMutex
	byDex     map[string]hyperliquid.ClearinghouseState
	updatedAt time.Time
}

var accountFeedSingleton struct {
	once sync.Once
	feed *accountFeed
}

func getAccountFeed() *accountFeed {
	accountFeedSingleton.once.Do(func() {
		accountFeedSingleton.feed = &accountFeed{
			byDex: make(map[string]hyperliquid.ClearinghouseState),
		}
	})
	return accountFeedSingleton.feed
}

// startAccountFeed 在后台订阅 allDexsClearinghouseState。
func startAccountFeed(ctx context.Context, wallet string, testnet bool) {
	f := getAccountFeed()

	go func() {
		base := hyperliquid.MainnetAPIURL
		if testnet {
			base = hyperliquid.TestnetAPIURL
		}

		cli := hyperliquid.NewWebsocketClient(base)
		_, err := cli.AllDexsClearinghouseState(
			hyperliquid.AllDexsClearinghouseStateSubscriptionParams{
				User: strings.ToLower(wallet),
			},
			func(msg hyperliquid.AllDexsClearinghouseState, err error) {
				if err != nil {
					log.Printf("⚠️ accountFeed ws error: %v", err)
					return
				}
				f.handleAllDexsMsg(msg)
			},
		)
		if err != nil {
			log.Printf("⚠️ accountFeed subscribe failed: %v", err)
			return
		}

		if err := cli.Connect(ctx); err != nil {
			log.Printf("⚠️ accountFeed connect failed: %v", err)
			return
		}

		<-ctx.Done()
		_ = cli.Close()
	}()
}

func (f *accountFeed) handleAllDexsMsg(msg hyperliquid.AllDexsClearinghouseState) {
	now := time.Now()
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, pair := range msg.ClearinghouseStates {
		f.byDex[pair.First] = pair.Second
	}
	f.updatedAt = now
}

// getUserState 从缓存获取指定 dex 的 state；freshWithin 表示可接受的最新时间窗口。
func (f *accountFeed) getUserState(dex string, freshWithin time.Duration) (*hyperliquid.ClearinghouseState, bool) {
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
