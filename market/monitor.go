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
	klineDataMap1h  sync.Map // 存储每个交易对的1小时K线历史数据
	klineDataMap4h  sync.Map // 存储每个交易对的K线历史数据
	tickerDataMap   sync.Map // 存储每个交易对的ticker数据
	batchSize       int
	filterSymbols   sync.Map // 使用sync.Map来存储需要监控的币种和其状态
	symbolStats     sync.Map // 存储币种统计信息
	FilterSymbol    []string //经过筛选的币种

	subMu      sync.Mutex
	subscribed map[string]struct{} // key: symbol|interval (Binance kline subscriptions)
}

// getKlineLimit 根据时间周期返回对应的K线数据根数
func getKlineLimit(timeframe string) int {
	switch timeframe {
	case "3m":
		return 20 // 3分钟数据：20根 = 1小时历史
	case "15m":
		return 50 // 15分钟数据：50根 ≈ 12.5小时历史
	case "1h":
		return 24 // 1小时数据：24根 = 24小时历史
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
var subKlineTime = []string{"3m", "15m", "1h", "4h"} // 管理订阅流的K线周期

func NewWSMonitor(batchSize int) *WSMonitor {
	WSMonitorCli = &WSMonitor{
		wsClient:       NewWSClient(),
		combinedClient: NewCombinedStreamsClient(batchSize),
		alertsChan:     make(chan Alert, 1000),
		batchSize:      batchSize,
		subscribed:     make(map[string]struct{}),
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

	// 统一币种格式（仅支持 Binance，去重；不支持 ":" / xyz: 等资产）
	seen := make(map[string]struct{}, len(m.symbols))
	normalized := make([]string, 0, len(m.symbols))
	for _, s := range m.symbols {
		bnSymbol, ok := toBinanceSymbol(s)
		if !ok {
			log.Printf("⚠️ 跳过非 Binance 币种: %s", s)
			continue
		}
		if _, ok := seen[bnSymbol]; ok {
			continue
		}
		seen[bnSymbol] = struct{}{}
		normalized = append(normalized, bnSymbol)
	}
	m.symbols = normalized

	log.Printf("找到 %d 个交易对", len(m.symbols))

	return nil
}

// initializeHistoricalData 启动时用 Binance REST 预热 K 线缓存（避免仅靠 WS 等待慢周期K线）。
func (m *WSMonitor) initializeHistoricalData() error {
	apiClient := NewAPIClient()

	var wg sync.WaitGroup
	semaphore := make(chan struct{}, 5) // 限制并发数，避免触发 Binance 限流

	for _, symbol := range m.symbols {
		s := symbol
		wg.Add(1)
		semaphore <- struct{}{}

		go func() {
			defer wg.Done()
			defer func() { <-semaphore }()

			for _, tf := range subKlineTime {
				limit := getKlineLimit(tf)
				klines, err := apiClient.GetKlines(s, tf, limit)
				if err != nil {
					log.Printf("获取 %s %s 历史数据失败: %v", s, tf, err)
					continue
				}
				if len(klines) > 0 {
					m.getKlineDataMap(tf).Store(s, klines)
					log.Printf("已加载 %s 的历史K线数据-%s: %d 条", s, tf, len(klines))
				}
			}
		}()
	}

	wg.Wait()
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

	// 预热历史数据（REST）
	if err := m.initializeHistoricalData(); err != nil {
		log.Printf("初始化历史数据失败: %v", err)
	}

	if err := m.subscribeAll(); err != nil {
		log.Printf("❌ 订阅 Binance K线失败: %v", err)
		return
	}
}

func subKey(symbol, interval string) string {
	return symbol + "|" + interval
}

// ensureSubscribed 确保已为指定 symbol/interval 注册 Binance K 线订阅者（幂等）。
// 返回值表示是否是首次注册。
func (m *WSMonitor) ensureSubscribed(symbol, interval string) bool {
	if symbol == "" || interval == "" {
		return false
	}

	key := subKey(symbol, interval)
	m.subMu.Lock()
	if _, ok := m.subscribed[key]; ok {
		m.subMu.Unlock()
		return false
	}
	m.subscribed[key] = struct{}{}
	m.subMu.Unlock()

	stream := fmt.Sprintf("%s@kline_%s", strings.ToLower(symbol), interval)
	ch := m.combinedClient.AddSubscriber(stream, 1000)
	go m.handleKlineData(symbol, ch, interval)

	return true
}

func (m *WSMonitor) subscribeAll() error {
	// 执行批量订阅
	log.Println("开始订阅所有交易对...")

	m.combinedClient.mu.RLock()
	conn := m.combinedClient.conn
	m.combinedClient.mu.RUnlock()
	if conn == nil {
		if err := m.combinedClient.Connect(); err != nil {
			return err
		}
	}

	for _, symbol := range m.symbols {
		for _, tf := range subKlineTime {
			m.ensureSubscribed(symbol, tf)
		}
	}
	for _, tf := range subKlineTime {
		if err := m.combinedClient.BatchSubscribeKlines(m.symbols, tf); err != nil {
			return err
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

func (m *WSMonitor) getKlineDataMap(_time string) *sync.Map {
	var klineDataMap *sync.Map
	if _time == "3m" {
		klineDataMap = &m.klineDataMap3m
	} else if _time == "15m" {
		klineDataMap = &m.klineDataMap15m
	} else if _time == "1h" {
		klineDataMap = &m.klineDataMap1h
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
	bnSymbol, ok := toBinanceSymbol(symbol)
	if !ok {
		return
	}

	// 更新K线数据
	var klineDataMap = m.getKlineDataMap(_time)
	value, exists := klineDataMap.Load(bnSymbol)
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

	klineDataMap.Store(bnSymbol, klines)
}

func (m *WSMonitor) GetCurrentKlines(symbol string, _time string) ([]Kline, error) {
	bnSymbol, ok := toBinanceSymbol(symbol)
	if !ok || bnSymbol == "" {
		return nil, fmt.Errorf("不支持的币种(非 Binance): %s", symbol)
	}

	m.ensureSubscribed(bnSymbol, _time)

	// 如果 WS 已连接且该 stream 尚未订阅，则补订阅，确保后续实时更新。
	m.combinedClient.mu.RLock()
	conn := m.combinedClient.conn
	m.combinedClient.mu.RUnlock()
	if conn != nil {
		stream := fmt.Sprintf("%s@kline_%s", strings.ToLower(bnSymbol), _time)
		m.combinedClient.subMu.Lock()
		_, subbed := m.combinedClient.subscribed[stream]
		m.combinedClient.subMu.Unlock()
		if !subbed {
			_ = m.combinedClient.BatchSubscribeKlines([]string{bnSymbol}, _time)
		}
	}

	value, exists := m.getKlineDataMap(_time).Load(bnSymbol)
	var cached []Kline
	if exists {
		cached = value.([]Kline)
	}
	if !exists || len(cached) == 0 {
		// 如果缓存未初始化/为空，使用 Binance REST 获取并填充
		apiClient := NewAPIClient()
		limit := getKlineLimit(_time)
		klines, err := apiClient.GetKlines(bnSymbol, _time, limit)
		if err != nil {
			return nil, fmt.Errorf("获取%v分钟K线失败: %v", _time, err)
		}
		m.getKlineDataMap(_time).Store(bnSymbol, klines)

		result := make([]Kline, len(klines))
		copy(result, klines)
		return result, nil
	}

	// ✅ FIX: 返回深拷贝而非引用，避免并发竞态条件
	result := make([]Kline, len(cached))
	copy(result, cached)
	return result, nil
}

func (m *WSMonitor) Close() {
	m.wsClient.Close()
	m.combinedClient.Close()
	close(m.alertsChan)
}
