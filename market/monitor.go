package market

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

type WSMonitor struct {
	wsClient        *WSClient              // Binance WS (保留以备后续切换)
	combinedClient  *CombinedStreamsClient // Binance 合并流（保留但默认不用）
	symbols         []string
	featuresMap     sync.Map
	alertsChan      chan Alert
	klineDataMap3m  sync.Map // 存储每个交易对的K线历史数据
	klineDataMap15m sync.Map // 存储每个交易对的15分钟K线历史数据
	klineDataMap4h  sync.Map // 存储每个交易对的K线历史数据
	tickerDataMap   sync.Map // 存储每个交易对的ticker数据
	batchSize       int
	filterSymbols   sync.Map // 使用sync.Map来存储需要监控的币种和其状态
	symbolStats     sync.Map // 存储币种统计信息
	FilterSymbol    []string //经过筛选的币种

	subMu      sync.Mutex
	subscribed map[string]struct{} // key: symbol|interval

	readyMu sync.Mutex
	ready   map[string]chan struct{} // key: symbol|interval
}

// getKlineLimit 根据时间周期返回对应的K线数据根数
func getKlineLimit(timeframe string) int {
	switch timeframe {
	case "3m":
		return 20 // 3分钟数据：20根 = 1小时历史
	case "15m":
		return 50 // 15分钟数据：50根 ≈ 12.5小时历史
	case "4h":
		return 21 // 4小时数据：21根 = 3.5天历史
	default:
		return 100 // 默认值
	}
}

type SymbolStats struct {
	LastActiveTime   time.Time
	AlertCount       int
	VolumeSpikeCount int
	LastAlertTime    time.Time
	Score            float64 // 综合评分
}

var WSMonitorCli *WSMonitor
var subKlineTime = []string{"3m", "15m", "4h"} // 管理订阅流的K线周期

func NewWSMonitor(batchSize int) *WSMonitor {
	WSMonitorCli = &WSMonitor{
		wsClient:       NewWSClient(),
		combinedClient: NewCombinedStreamsClient(batchSize),
		alertsChan:     make(chan Alert, 1000),
		batchSize:      batchSize,
		subscribed:     make(map[string]struct{}),
		ready:          make(map[string]chan struct{}),
	}
	return WSMonitorCli
}

func (m *WSMonitor) Initialize(coins []string) error {
	log.Println("初始化WebSocket监控器...")
	// 获取交易对信息
	apiClient := NewAPIClient()
	// 如果不指定交易对，则使用market市场的所有交易对币种
	if len(coins) == 0 {
		exchangeInfo, err := apiClient.GetExchangeInfo()
		if err != nil {
			return err
		}
		// 筛选永续合约交易对 --仅测试时使用
		//exchangeInfo.Symbols = exchangeInfo.Symbols[0:2]
		for _, symbol := range exchangeInfo.Symbols {
			if symbol.Status == "TRADING" && symbol.ContractType == "PERPETUAL" && strings.ToUpper(symbol.Symbol[len(symbol.Symbol)-4:]) == "USDT" {
				m.symbols = append(m.symbols, symbol.Symbol)
				m.filterSymbols.Store(symbol.Symbol, true)
			}
		}
	} else {
		m.symbols = coins
	}

	// 统一币种格式（避免 BTC vs BTCUSDT 等导致 WS/缓存错配），并去重
	seen := make(map[string]struct{}, len(m.symbols))
	normalized := make([]string, 0, len(m.symbols))
	for _, s := range m.symbols {
		sym := normalizeKlineSymbol(s)
		if sym == "" {
			continue
		}
		if _, ok := seen[sym]; ok {
			continue
		}
		seen[sym] = struct{}{}
		normalized = append(normalized, sym)
	}
	m.symbols = normalized

	log.Printf("找到 %d 个交易对", len(m.symbols))

	return nil
}

func (m *WSMonitor) Start(coins []string) {
	log.Printf("启动WebSocket实时监控...")
	// 初始化交易对
	err := m.Initialize(coins)
	if err != nil {
		log.Printf("❌ 初始化币种失败: %v", err)
		return
	}

	if err := m.subscribeAll(); err != nil {
		log.Printf("❌ 订阅 Hyperliquid K线失败: %v", err)
		return
	}
}

// subscribeSymbol 注册监听
func (m *WSMonitor) subscribeSymbol(symbol, st string) []string {
	var streams []string
	hlSymbol := ToHLSymbol(symbol)
	// 使用全局 WS manager 订阅
	unsub := subscribeHLCandle(hlSymbol, st, func(c HLCandle) {
		m.handleHLKlineData(symbol, c, st)
	})
	_ = unsub
	streams = append(streams, hlSymbol+"@"+st)

	return streams
}

func subKey(symbol, interval string) string {
	return symbol + "|" + interval
}

// normalizeKlineSymbol 将输入 symbol 规范为内部 K 线缓存的 key。
// - 普通币种：统一为 Binance 风格（默认追加 USDT），例如 BTC -> BTCUSDT
// - Hyperliquid/HIP-3 等带 ":" 的符号保持不变（仅做大写）
func normalizeKlineSymbol(symbol string) string {
	symbol = Normalize(symbol)
	if symbol == "" {
		return ""
	}
	if s, ok := toBinanceSymbol(symbol); ok {
		return s
	}
	return symbol
}

// ensureSubscribed 确保已通过 WS 订阅到指定 symbol/interval 的 K 线推送（幂等）。
func (m *WSMonitor) ensureSubscribed(symbol, interval string) {
	symbol = normalizeKlineSymbol(symbol)
	if symbol == "" || interval == "" {
		return
	}

	key := subKey(symbol, interval)
	m.subMu.Lock()
	if _, ok := m.subscribed[key]; ok {
		m.subMu.Unlock()
		return
	}
	m.subscribed[key] = struct{}{}
	m.subMu.Unlock()

	m.subscribeSymbol(symbol, interval)
}

func (m *WSMonitor) getReadyChan(symbol, interval string) chan struct{} {
	key := subKey(symbol, interval)
	m.readyMu.Lock()
	defer m.readyMu.Unlock()
	if m.ready == nil {
		m.ready = make(map[string]chan struct{})
	}
	ch, ok := m.ready[key]
	if !ok {
		ch = make(chan struct{})
		m.ready[key] = ch
	}
	return ch
}

func (m *WSMonitor) clearReadyChan(symbol, interval string, ch chan struct{}) {
	key := subKey(symbol, interval)
	m.readyMu.Lock()
	if m.ready != nil && m.ready[key] == ch {
		delete(m.ready, key)
	}
	m.readyMu.Unlock()
}

func (m *WSMonitor) signalReady(symbol, interval string) {
	key := subKey(symbol, interval)
	m.readyMu.Lock()
	ch, ok := m.ready[key]
	if ok {
		delete(m.ready, key)
	}
	m.readyMu.Unlock()
	if ok {
		close(ch)
	}
}

func (m *WSMonitor) waitForFirstKline(symbol, interval string, timeout time.Duration) error {
	if timeout <= 0 {
		return nil
	}

	// fast path
	if v, ok := m.getKlineDataMap(interval).Load(symbol); ok {
		if ks, ok2 := v.([]Kline); ok2 && len(ks) > 0 {
			return nil
		}
	}

	ch := m.getReadyChan(symbol, interval)
	// re-check after registering waiter to avoid missing a signal
	if v, ok := m.getKlineDataMap(interval).Load(symbol); ok {
		if ks, ok2 := v.([]Kline); ok2 && len(ks) > 0 {
			m.clearReadyChan(symbol, interval, ch)
			return nil
		}
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-ch:
		return nil
	case <-timer.C:
		m.clearReadyChan(symbol, interval, ch)
		return fmt.Errorf("WS kline not ready: symbol=%s interval=%s timeout=%s", symbol, interval, timeout)
	}
}

func (m *WSMonitor) subscribeAll() error {
	// 执行批量订阅
	log.Println("开始订阅所有交易对...")
	for _, symbol := range m.symbols {
		for _, st := range subKlineTime {
			m.ensureSubscribed(symbol, st)
		}
	}
	log.Println("所有交易对订阅完成")
	return nil
}

func (m *WSMonitor) handleKlineData(symbol string, ch <-chan []byte, _time string) {
	for data := range ch {
		var klineData KlineWSData
		if err := json.Unmarshal(data, &klineData); err != nil {
			log.Printf("解析Kline数据失败: %v", err)
			continue
		}
		m.processKlineUpdate(symbol, klineData, _time)
	}
}

// handleHLKlineData 处理 Hyperliquid K 线
func (m *WSMonitor) handleHLKlineData(symbol string, candle HLCandle, _time string) {
	kline := Kline{
		OpenTime:            candle.StartTime,
		Open:                candle.Open,
		High:                candle.High,
		Low:                 candle.Low,
		Close:               candle.Close,
		Volume:              candle.Volume,
		CloseTime:           candle.EndTime,
		QuoteVolume:         0,
		Trades:              candle.Trades,
		TakerBuyBaseVolume:  0,
		TakerBuyQuoteVolume: 0,
	}
	m.storeKline(symbol, kline, _time)
}

// toHLSymbol 将内部 symbol 映射为 Hyperliquid 符号
func ToHLSymbol(symbol string) string {
	s := strings.TrimSpace(symbol)
	if strings.Contains(s, ":") {
		parts := strings.SplitN(s, ":", 2)
		prefix := strings.ToLower(parts[0])
		suffix := strings.ToUpper(parts[1])
		return prefix + ":" + suffix
	}
	// 去掉常见后缀
	s = strings.ToUpper(s)
	s = strings.TrimSuffix(s, "USDT")
	s = strings.TrimSuffix(s, "USD")
	// 简单商品映射
	switch s {
	case "SILVER", "XAG":
		return "xyz:SILVER"
	case "GOLD", "XAU":
		return "xyz:GOLD"
	}
	return s
}

func (m *WSMonitor) getKlineDataMap(_time string) *sync.Map {
	var klineDataMap *sync.Map
	if _time == "3m" {
		klineDataMap = &m.klineDataMap3m
	} else if _time == "15m" {
		klineDataMap = &m.klineDataMap15m
	} else if _time == "4h" {
		klineDataMap = &m.klineDataMap4h
	} else {
		klineDataMap = &sync.Map{}
	}
	return klineDataMap
}
func (m *WSMonitor) processKlineUpdate(symbol string, wsData KlineWSData, _time string) {
	// 转换WebSocket数据为Kline结构
	kline := Kline{
		OpenTime:  wsData.Kline.StartTime,
		CloseTime: wsData.Kline.CloseTime,
		Trades:    wsData.Kline.NumberOfTrades,
	}
	kline.Open, _ = parseFloat(wsData.Kline.OpenPrice)
	kline.High, _ = parseFloat(wsData.Kline.HighPrice)
	kline.Low, _ = parseFloat(wsData.Kline.LowPrice)
	kline.Close, _ = parseFloat(wsData.Kline.ClosePrice)
	kline.Volume, _ = parseFloat(wsData.Kline.Volume)
	kline.High, _ = parseFloat(wsData.Kline.HighPrice)
	kline.QuoteVolume, _ = parseFloat(wsData.Kline.QuoteVolume)
	kline.TakerBuyBaseVolume, _ = parseFloat(wsData.Kline.TakerBuyBaseVolume)
	kline.TakerBuyQuoteVolume, _ = parseFloat(wsData.Kline.TakerBuyQuoteVolume)
	m.storeKline(symbol, kline, _time)
}

// storeKline 将 K 线写入对应的周期缓存
func (m *WSMonitor) storeKline(symbol string, kline Kline, _time string) {
	symbol = normalizeKlineSymbol(symbol)
	if symbol == "" {
		return
	}

	// 更新K线数据
	var klineDataMap = m.getKlineDataMap(_time)
	value, exists := klineDataMap.Load(symbol)
	var klines []Kline
	if exists {
		// copy-on-write: 避免并发读/写共享底层数组产生竞态
		src := value.([]Kline)
		klines = make([]Kline, len(src))
		copy(klines, src)

		// 检查是否是新的K线
		if len(klines) > 0 && klines[len(klines)-1].OpenTime == kline.OpenTime {
			// 更新当前K线
			klines[len(klines)-1] = kline
		} else {
			// 添加新K线
			klines = append(klines, kline)

			// 保持数据长度
			maxLen := getKlineLimit(_time)
			if len(klines) > maxLen {
				klines = klines[1:]
			}
		}
	} else {
		klines = []Kline{kline}
	}

	klineDataMap.Store(symbol, klines)
	if !exists {
		m.signalReady(symbol, _time)
	}
}

func (m *WSMonitor) GetCurrentKlines(symbol string, _time string) ([]Kline, error) {
	symbol = normalizeKlineSymbol(symbol)
	if symbol == "" {
		return nil, fmt.Errorf("symbol 为空")
	}

	m.ensureSubscribed(symbol, _time)
	if err := m.waitForFirstKline(symbol, _time, 10*time.Second); err != nil {
		return nil, err
	}

	value, exists := m.getKlineDataMap(_time).Load(symbol)
	if !exists {
		return nil, fmt.Errorf("K线数据不存在: symbol=%s interval=%s", symbol, _time)
	}

	// ✅ FIX: 返回深拷贝而非引用，避免并发竞态条件
	klines := value.([]Kline)
	if len(klines) == 0 {
		return nil, fmt.Errorf("K线数据为空: symbol=%s interval=%s", symbol, _time)
	}
	result := make([]Kline, len(klines))
	copy(result, klines)
	return result, nil
}

func (m *WSMonitor) Close() {
	m.wsClient.Close()
	close(m.alertsChan)
}
