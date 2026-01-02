package hyperws

import (
	"encoding/json"
	"log"
	"net/url"
	"reflect"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type handler func(channel string, data json.RawMessage)

type subEntry struct {
	payload  map[string]interface{}
	channel  string
	handlers []handler
}

type Manager struct {
	url   string
	mu    sync.RWMutex
	conn  *websocket.Conn
	subs  []*subEntry
	byCh  map[string][]*subEntry
	start sync.Once
}

var (
	mgrMu    sync.Mutex
	mgrByURL = make(map[string]*Manager)
)

// Get returns singleton manager; the first caller must pass wsURL.
func Get(wsURL string) *Manager {
	if wsURL == "" {
		wsURL = "wss://api.hyperliquid.xyz/ws"
	}

	mgrMu.Lock()
	defer mgrMu.Unlock()

	if m, ok := mgrByURL[wsURL]; ok {
		return m
	}

	m := &Manager{
		url:  wsURL,
		byCh: make(map[string][]*subEntry),
	}
	m.startConnectLoop()
	mgrByURL[wsURL] = m
	return m
}

// Subscribe registers a subscription payload and handler; returns unsubscribe function.
func (m *Manager) Subscribe(channel string, payload map[string]interface{}, h handler) func() {
	m.mu.Lock()
	defer m.mu.Unlock()

	// try to merge if same payload+channel
	for _, s := range m.subs {
		if s.channel == channel && jsonEqual(s.payload, payload) {
			s.handlers = append(s.handlers, h)
			return func() { m.removeHandler(s, h) }
		}
	}

	entry := &subEntry{
		payload:  payload,
		channel:  channel,
		handlers: []handler{h},
	}
	m.subs = append(m.subs, entry)
	m.byCh[channel] = append(m.byCh[channel], entry)

	// send subscribe if connected
	if m.conn != nil {
		_ = m.conn.WriteJSON(payload)
	}

	return func() { m.removeHandler(entry, h) }
}

func (m *Manager) removeHandler(entry *subEntry, h handler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var nh []handler
	for _, fn := range entry.handlers {
		if reflect.ValueOf(fn).Pointer() != reflect.ValueOf(h).Pointer() {
			nh = append(nh, fn)
		}
	}
	entry.handlers = nh
	if len(nh) == 0 {
		// remove entry
		for i, s := range m.subs {
			if s == entry {
				m.subs = append(m.subs[:i], m.subs[i+1:]...)
				break
			}
		}
		var list []*subEntry
		for _, s := range m.byCh[entry.channel] {
			if s != entry {
				list = append(list, s)
			}
		}
		m.byCh[entry.channel] = list
	}
}

func (m *Manager) startConnectLoop() {
	m.start.Do(func() {
		go func() {
			backoff := time.Second
			for {
				m.connectOnce()
				time.Sleep(backoff)
				if backoff < 30*time.Second {
					backoff *= 2
				}
			}
		}()
	})
}

func (m *Manager) connectOnce() {
	u, _ := url.Parse(m.url)
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, _, err := dialer.Dial(u.String(), nil)
	if err != nil {
		log.Printf("⚠️ WS manager 连接失败: %v", err)
		return
	}

	m.mu.Lock()
	m.conn = conn
	subs := append([]*subEntry(nil), m.subs...)
	m.mu.Unlock()

	// replay subscriptions
	for _, s := range subs {
		_ = conn.WriteJSON(s.payload)
	}
	log.Printf("✅ WS manager 已连接并重放 %d 个订阅", len(subs))

	// read loop
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			log.Printf("⚠️ WS manager 读失败: %v", err)
			conn.Close()
			m.mu.Lock()
			if m.conn == conn {
				m.conn = nil
			}
			m.mu.Unlock()
			return
		}
		m.dispatch(msg)
	}
}

func (m *Manager) dispatch(msg []byte) {
	var env struct {
		Channel string          `json:"channel"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(msg, &env); err != nil {
		return
	}

	m.mu.RLock()
	entries := append([]*subEntry(nil), m.byCh[env.Channel]...)
	m.mu.RUnlock()

	for _, e := range entries {
		for _, h := range e.handlers {
			h(env.Channel, env.Data)
		}
	}
}

// jsonEqual checks structural equality of two payloads.
func jsonEqual(a, b map[string]interface{}) bool {
	aj, _ := json.Marshal(a)
	bj, _ := json.Marshal(b)
	return string(aj) == string(bj)
}
