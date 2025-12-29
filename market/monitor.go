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

	log.Printf("找到 %d 个交易对", len(m.symbols))
	// 初始化历史数据
	if err := m.initializeHistoricalData(); err != nil {
		log.Printf("初始化历史数据失败: %v", err)
	}

	return nil
}

func (m *WSMonitor) initializeHistoricalData() error {
	apiClient := NewHLAPIClient()

	var wg sync.WaitGroup
	semaphore := make(chan struct{}, 5) // 限制并发数

	for _, symbol := range m.symbols {
		wg.Add(1)
		semaphore <- struct{}{}

		go func(s string) {
			defer wg.Done()
			defer func() { <-semaphore }()

			// 获取历史K线数据
			klines, err := apiClient.GetKlines(s, "3m", getKlineLimit("3m"))
			if err != nil {
				log.Printf("获取 %s 历史数据失败: %v", s, err)
				return
			}
			if len(klines) > 0 {
				m.klineDataMap3m.Store(s, klines)
				log.Printf("已加载 %s 的历史K线数据-3m: %d 条", s, len(klines))
			}
			// 获取15分钟历史K线数据
			klines15m, err := apiClient.GetKlines(s, "15m", getKlineLimit("15m"))
			if err != nil {
				log.Printf("获取 %s 15分钟历史数据失败: %v", s, err)
				return
			}
			if len(klines15m) > 0 {
				m.klineDataMap15m.Store(s, klines15m)
				log.Printf("已加载 %s 的历史K线数据-15m: %d 条", s, len(klines15m))
			}
			// 获取历史K线数据
			klines4h, err := apiClient.GetKlines(s, "4h", getKlineLimit("4h"))
			if err != nil {
				log.Printf("获取 %s 历史数据失败: %v", s, err)
				return
			}
			if len(klines4h) > 0 {
				m.klineDataMap4h.Store(s, klines4h)
				log.Printf("已加载 %s 的历史K线数据-4h: %d 条", s, len(klines4h))
			}
		}(symbol)
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
func (m *WSMonitor) subscribeAll() error {
	// 执行批量订阅
	log.Println("开始订阅所有交易对...")
	for _, symbol := range m.symbols {
		for _, st := range subKlineTime {
			m.subscribeSymbol(symbol, st)
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
	// 更新K线数据
	var klineDataMap = m.getKlineDataMap(_time)
	value, exists := klineDataMap.Load(symbol)
	var klines []Kline
	if exists {
		klines = value.([]Kline)

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
}

func (m *WSMonitor) GetCurrentKlines(symbol string, _time string) ([]Kline, error) {
	// 对每一个进来的symbol检测是否存在内类 是否的话就订阅它
	value, exists := m.getKlineDataMap(_time).Load(symbol)
	if !exists {
		// 如果Ws数据未初始化完成时,单独使用 Hyperliquid API 获取
		apiClient := NewHLAPIClient()
		limit := getKlineLimit(_time)
		klines, err := apiClient.GetKlines(symbol, _time, limit)
		if err != nil {
			return nil, fmt.Errorf("获取%v分钟K线失败: %v", _time, err)
		}

		// 动态缓存进缓存
		m.getKlineDataMap(_time).Store(strings.ToUpper(symbol), klines)

		// ✅ FIX: 返回深拷贝而非引用
		result := make([]Kline, len(klines))
		copy(result, klines)
		return result, nil
	}

	// ✅ FIX: 返回深拷贝而非引用，避免并发竞态条件
	klines := value.([]Kline)
	result := make([]Kline, len(klines))
	copy(result, klines)
	return result, nil
}

func (m *WSMonitor) Close() {
	m.wsClient.Close()
	close(m.alertsChan)
}
