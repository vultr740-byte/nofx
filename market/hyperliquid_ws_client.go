package market

import (
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// HLWSClient 提供 Hyperliquid K 线订阅（主网）
type HLWSClient struct {
	conn        *websocket.Conn
	mu          sync.RWMutex
	subscribers map[string]chan HLCandle
	reconnect   bool
	done        chan struct{}
	url         string
}

type hlSubscriptionResponse struct {
	Channel string          `json:"channel"`
	Data    json.RawMessage `json:"data"`
}

type HLCandle struct {
	StartTime int64
	EndTime   int64
	Symbol    string
	Interval  string
	Open      float64
	Close     float64
	High      float64
	Low       float64
	Volume    float64
	Trades    int
}

type hlCandleMsg struct {
	Channel string `json:"channel"`
	Data    struct {
		T    int64  `json:"t"`
		TEnd int64  `json:"T"`
		S    string `json:"s"`
		I    string `json:"i"`
		O    string `json:"o"`
		C    string `json:"c"`
		H    string `json:"h"`
		L    string `json:"l"`
		V    string `json:"v"`
		N    int    `json:"n"`
	} `json:"data"`
}

func NewHLWSClient() *HLWSClient {
	return &HLWSClient{
		subscribers: make(map[string]chan HLCandle),
		reconnect:   true,
		done:        make(chan struct{}),
		// 主网端点；如需 testnet 可在 Connect 时覆盖
		url: "wss://api.hyperliquid.xyz/ws",
	}
}

func (h *HLWSClient) Connect() error {
	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}
	conn, _, err := dialer.Dial(h.url, nil)
	if err != nil {
		return fmt.Errorf("Hyperliquid WS 连接失败: %w", err)
	}

	h.mu.Lock()
	h.conn = conn
	h.mu.Unlock()

	log.Println("Hyperliquid WS 连接成功")
	go h.readLoop()
	return nil
}

// SubscribeCandle 订阅单个交易对的 K 线
func (h *HLWSClient) SubscribeCandle(symbol, interval string) (chan HLCandle, error) {
	stream := h.streamKey(symbol, interval)
	h.mu.RLock()
	if ch, ok := h.subscribers[stream]; ok {
		h.mu.RUnlock()
		return ch, nil
	}
	h.mu.RUnlock()

	req := map[string]interface{}{
		"method": "subscribe",
		"subscription": map[string]interface{}{
			"type":     "candle",
			"coin":     symbol,
			"interval": interval,
		},
	}

	h.mu.RLock()
	conn := h.conn
	h.mu.RUnlock()
	if conn == nil {
		return nil, fmt.Errorf("Hyperliquid WS 未连接")
	}
	if err := conn.WriteJSON(req); err != nil {
		return nil, fmt.Errorf("订阅 K 线失败: %w", err)
	}

	ch := make(chan HLCandle, 100)
	h.mu.Lock()
	h.subscribers[stream] = ch
	h.mu.Unlock()
	log.Printf("订阅 HL K线: %s %s", symbol, interval)
	return ch, nil
}

func (h *HLWSClient) readLoop() {
	for {
		select {
		case <-h.done:
			return
		default:
		}

		h.mu.RLock()
		conn := h.conn
		h.mu.RUnlock()
		if conn == nil {
			time.Sleep(time.Second)
			continue
		}

		_, message, err := conn.ReadMessage()
		if err != nil {
			log.Printf("HL WS 读消息失败: %v", err)
			h.handleReconnect()
			return
		}
		h.handleMessage(message)
	}
}

func (h *HLWSClient) handleMessage(msg []byte) {
	// 只关心 candle channel
	var candle hlCandleMsg
	if err := json.Unmarshal(msg, &candle); err == nil && candle.Channel == "candle" {
		key := h.streamKey(candle.Data.S, candle.Data.I)
		h.mu.RLock()
		ch, ok := h.subscribers[key]
		h.mu.RUnlock()
		if !ok {
			return
		}
		hlc, err := convertHLCandle(candle)
		if err != nil {
			log.Printf("解析 HL candle 失败: %v", err)
			return
		}
		select {
		case ch <- hlc:
		default:
			// 丢弃溢出以避免阻塞
		}
		return
	}
}

func convertHLCandle(m hlCandleMsg) (HLCandle, error) {
	parseF := func(s string) (float64, error) {
		return strconv.ParseFloat(s, 64)
	}

	o, err := parseF(m.Data.O)
	if err != nil {
		return HLCandle{}, err
	}
	c, err := parseF(m.Data.C)
	if err != nil {
		return HLCandle{}, err
	}
	hh, err := parseF(m.Data.H)
	if err != nil {
		return HLCandle{}, err
	}
	l, err := parseF(m.Data.L)
	if err != nil {
		return HLCandle{}, err
	}
	v, err := parseF(m.Data.V)
	if err != nil {
		return HLCandle{}, err
	}

	return HLCandle{
		StartTime: m.Data.T,
		EndTime:   m.Data.TEnd,
		Symbol:    strings.ToUpper(m.Data.S),
		Interval:  m.Data.I,
		Open:      o,
		Close:     c,
		High:      hh,
		Low:       l,
		Volume:    v,
		Trades:    m.Data.N,
	}, nil
}

func (h *HLWSClient) streamKey(symbol, interval string) string {
	return strings.ToUpper(symbol) + "@" + strings.ToLower(interval)
}

func (h *HLWSClient) handleReconnect() {
	if !h.reconnect {
		return
	}
	log.Println("HL WS 断开，尝试重连...")
	time.Sleep(2 * time.Second)
	if err := h.Connect(); err != nil {
		log.Printf("HL WS 重连失败: %v", err)
		go h.handleReconnect()
		return
	}
	// 重放订阅
	h.mu.RLock()
	subs := make(map[string]chan HLCandle, len(h.subscribers))
	for k, v := range h.subscribers {
		subs[k] = v
	}
	h.mu.RUnlock()
	for key := range subs {
		parts := strings.Split(key, "@")
		if len(parts) != 2 {
			continue
		}
		sym := parts[0]
		intv := parts[1]
		// 重新订阅
		if _, err := h.SubscribeCandle(sym, intv); err != nil {
			log.Printf("重放订阅失败 %s: %v", key, err)
		}
	}
}

func (h *HLWSClient) Close() {
	h.reconnect = false
	close(h.done)

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.conn != nil {
		h.conn.Close()
		h.conn = nil
	}
	for k, ch := range h.subscribers {
		close(ch)
		delete(h.subscribers, k)
	}
}
