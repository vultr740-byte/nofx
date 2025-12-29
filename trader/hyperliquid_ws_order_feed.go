package trader

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sonirico/go-hyperliquid"
)

// orderFeed 订阅 orderUpdates / userFills，维护内存挂单+成交缓存，减少 HTTP openOrders/FrontendOpenOrders 调用。
type orderFeed struct {
	mu            sync.RWMutex
	openByCoin    map[string][]hyperliquid.OpenOrder         // 普通挂单（按 Coin）
	triggerByCoin map[string][]hyperliquid.FrontendOpenOrder // 触发类（TP/SL）
	conn          *websocket.Conn
	done          chan struct{}
}

var orderFeedSingleton struct {
	once sync.Once
	feed *orderFeed
}

func getOrderFeed() *orderFeed {
	orderFeedSingleton.once.Do(func() {
		orderFeedSingleton.feed = &orderFeed{
			openByCoin:    make(map[string][]hyperliquid.OpenOrder),
			triggerByCoin: make(map[string][]hyperliquid.FrontendOpenOrder),
			done:          make(chan struct{}),
		}
	})
	return orderFeedSingleton.feed
}

// startOrderFeed 启动 WS 订阅 orderUpdates/userFills/openOrders snapshot，主 dex 为空字符串。
func startOrderFeed(ctx context.Context, wallet string, testnet bool) {
	f := getOrderFeed()
	go func() {
		url := "wss://api.hyperliquid.xyz/ws"
		if testnet {
			url = "wss://api.hyperliquid-testnet.xyz/ws"
		}
		for {
			dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
			conn, _, err := dialer.Dial(url, nil)
			if err != nil {
				log.Printf("⚠️ orderFeed 连接失败: %v", err)
				time.Sleep(2 * time.Second)
				continue
			}
			f.mu.Lock()
			f.conn = conn
			f.mu.Unlock()

			subs := []map[string]interface{}{
				{"type": "orderUpdates", "user": strings.ToLower(wallet)},
				{"type": "userFills", "user": strings.ToLower(wallet)},
				{"type": "openOrders", "user": strings.ToLower(wallet)},         // snapshot + updates
				{"type": "frontendOpenOrders", "user": strings.ToLower(wallet)}, // snapshot + updates
			}
			for _, s := range subs {
				if err := conn.WriteJSON(map[string]interface{}{
					"method":       "subscribe",
					"subscription": s,
				}); err != nil {
					log.Printf("⚠️ orderFeed 订阅失败(%v): %v", s["type"], err)
				}
			}
			log.Printf("✅ orderFeed 已订阅 orderUpdates/userFills/openOrders/frontendOpenOrders")

			for {
				_, msg, err := conn.ReadMessage()
				if err != nil {
					log.Printf("⚠️ orderFeed 读失败: %v", err)
					conn.Close()
					break
				}
				f.handleMessage(msg)
			}
			select {
			case <-ctx.Done():
				return
			default:
			}
			time.Sleep(2 * time.Second)
		}
	}()
}

// 数据结构与 SDK ws_types_remote_easyjson.go 对齐
type wsOrderUpdate struct {
	Channel string `json:"channel"`
	Data    []struct {
		Coin       string `json:"coin"`
		Oid        int64  `json:"oid"`
		Sz         string `json:"sz"`
		Side       string `json:"side"`
		Px         string `json:"px"`
		Tif        string `json:"tif"`
		ReduceOnly bool   `json:"reduceOnly"`
		Status     string `json:"status"`
		OrderType  struct {
			Limit *struct {
				Tif string `json:"tif"`
			} `json:"limit,omitempty"`
			Trigger *struct {
				TriggerPx string `json:"triggerPx"`
				IsMarket  bool   `json:"isMarket"`
				Tpsl      string `json:"tpsl"`
			} `json:"trigger,omitempty"`
		} `json:"orderType"`
		IsPositionTpSl   bool    `json:"isPositionTpSl"`
		IsTrigger        bool    `json:"isTrigger"`
		TriggerCondition string  `json:"triggerCondition"`
		ClOid            *string `json:"cloid,omitempty"`
	} `json:"data"`
}

type wsOpenOrders struct {
	Channel string `json:"channel"`
	Data    struct {
		User   string                  `json:"user"`
		Orders []hyperliquid.OpenOrder `json:"orders"`
	} `json:"data"`
}

type wsFrontendOrders struct {
	Channel string `json:"channel"`
	Data    struct {
		User   string                          `json:"user"`
		Orders []hyperliquid.FrontendOpenOrder `json:"orders"`
	} `json:"data"`
}

type wsUserFills struct {
	Channel string `json:"channel"`
	Data    struct {
		IsSnapshot bool                      `json:"isSnapshot"`
		User       string                    `json:"user"`
		Fills      []hyperliquid.WsOrderFill `json:"fills"`
	} `json:"data"`
}

func (f *orderFeed) handleMessage(msg []byte) {
	// openOrders snapshot/update
	var o wsOpenOrders
	if err := json.Unmarshal(msg, &o); err == nil && o.Channel == "openOrders" {
		f.mu.Lock()
		f.openByCoin = make(map[string][]hyperliquid.OpenOrder)
		for _, ord := range o.Data.Orders {
			f.openByCoin[ord.Coin] = append(f.openByCoin[ord.Coin], ord)
		}
		f.mu.Unlock()
		return
	}

	// frontendOpenOrders snapshot/update
	var fo wsFrontendOrders
	if err := json.Unmarshal(msg, &fo); err == nil && fo.Channel == "frontendOpenOrders" {
		f.mu.Lock()
		f.triggerByCoin = make(map[string][]hyperliquid.FrontendOpenOrder)
		for _, ord := range fo.Data.Orders {
			f.triggerByCoin[ord.Coin] = append(f.triggerByCoin[ord.Coin], ord)
		}
		f.mu.Unlock()
		return
	}

	// orderUpdates 增量（这里简单重拉 snapshot，由于 HL 未提供差量标志）
	var upd wsOrderUpdate
	if err := json.Unmarshal(msg, &upd); err == nil && upd.Channel == "orderUpdates" {
		// 收到增量时暂不处理细粒度，等待下一次 snapshot 或上层查询时回退 HTTP
		return
	}

	// userFills 目前仅记录，不修改挂单缓存
	var fills wsUserFills
	if err := json.Unmarshal(msg, &fills); err == nil && fills.Channel == "userFills" {
		return
	}
}

// getOpenOrdersFromCache 返回某币种的挂单（浅拷贝）
func (f *orderFeed) getOpenOrdersFromCache(coin string) []hyperliquid.OpenOrder {
	f.mu.RLock()
	defer f.mu.RUnlock()
	orders := f.openByCoin[coin]
	cp := make([]hyperliquid.OpenOrder, len(orders))
	copy(cp, orders)
	return cp
}

// getFrontendOrdersFromCache 返回触发类挂单
func (f *orderFeed) getFrontendOrdersFromCache(coin string) []hyperliquid.FrontendOpenOrder {
	f.mu.RLock()
	defer f.mu.RUnlock()
	orders := f.triggerByCoin[coin]
	cp := make([]hyperliquid.FrontendOpenOrder, len(orders))
	copy(cp, orders)
	return cp
}
