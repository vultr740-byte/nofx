package market

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Get 获取指定代币的市场数据
func Get(symbol string) (*Data, error) {
	var klines1h, klines15m, klines4h []Kline
	var err error
	// 标准化symbol
	symbol = Normalize(symbol)
	// 获取15分钟K线数据 (最近10个)
	klines15m, err = WSMonitorCli.GetCurrentKlines(symbol, "15m") // 多获取一些用于计算
	if err != nil {
		return nil, fmt.Errorf("获取15分钟K线失败: %v", err)
	}

	// 获取4小时K线数据 (最近10个)
	klines4h, err = WSMonitorCli.GetCurrentKlines(symbol, "4h") // 多获取用于计算指标
	if err != nil {
		return nil, fmt.Errorf("获取4小时K线失败: %v", err)
	}

	// 获取1小时K线数据（结构共振用；失败不阻断整体数据）
	klines1h, err = WSMonitorCli.GetCurrentKlines(symbol, "1h")
	if err != nil {
		log.Printf("⚠️ 获取1小时K线失败(%s): %v", symbol, err)
		klines1h = nil
	}

	// 计算当前价格 (基于15分钟最新数据)
	currentPrice := klines15m[len(klines15m)-1].Close
	currentRSI7 := calculateRSI(klines15m, 7)

	// 计算价格变化百分比
	// 1小时价格变化 = 4个15分钟K线前的价格 (4 * 15 = 60分钟)
	priceChange1h := 0.0
	if len(klines15m) >= 5 { // 至少需要5根K线 (当前 + 4根前)
		price1hAgo := klines15m[len(klines15m)-5].Close
		if price1hAgo > 0 {
			priceChange1h = ((currentPrice - price1hAgo) / price1hAgo) * 100
		}
	}

	// 4小时价格变化 = 1个4小时K线前的价格
	priceChange4h := 0.0
	if len(klines4h) >= 2 {
		price4hAgo := klines4h[len(klines4h)-2].Close
		if price4hAgo > 0 {
			priceChange4h = ((currentPrice - price4hAgo) / price4hAgo) * 100
		}
	}

	// 获取OI数据
	oiData, err := getOpenInterestData(symbol)
	if err != nil {
		// OI失败不影响整体,使用默认值
		oiData = &OIData{Latest: 0, Average: 0}
	}

	// 获取Funding Rate
	fundingRate, _ := getFundingRate(symbol)

	// 计算日内系列数据
	intradayData := calculateIntradaySeries(klines15m)

	// 计算1小时结构数据（仅收盘价序列）
	hourlyData := calculateHourlyData(klines1h)

	// 计算长期数据
	longerTermData := calculateLongerTermData(klines4h)

	return &Data{
		Symbol:            symbol,
		CurrentPrice:      currentPrice,
		PriceChange1h:     priceChange1h,
		PriceChange4h:     priceChange4h,
		CurrentRSI7:       currentRSI7,
		OpenInterest:      oiData,
		FundingRate:       fundingRate,
		IntradaySeries:    intradayData,
		HourlyContext:     hourlyData,
		LongerTermContext: longerTermData,
	}, nil
}

// calculateEMA 计算EMA
func calculateEMA(klines []Kline, period int) float64 {
	if len(klines) < period {
		return 0
	}

	// 计算SMA作为初始EMA
	sum := 0.0
	for i := 0; i < period; i++ {
		sum += klines[i].Close
	}
	ema := sum / float64(period)

	// 计算EMA
	multiplier := 2.0 / float64(period+1)
	for i := period; i < len(klines); i++ {
		ema = (klines[i].Close-ema)*multiplier + ema
	}

	return ema
}

// calculateMACD 计算MACD
func calculateMACD(klines []Kline) float64 {
	if len(klines) < 26 {
		return 0
	}

	// 计算12期和26期EMA
	ema12 := calculateEMA(klines, 12)
	ema26 := calculateEMA(klines, 26)

	// MACD = EMA12 - EMA26
	return ema12 - ema26
}

// calculateRSI 计算RSI
func calculateRSI(klines []Kline, period int) float64 {
	if len(klines) <= period {
		return 0
	}

	gains := 0.0
	losses := 0.0

	// 计算初始平均涨跌幅
	for i := 1; i <= period; i++ {
		change := klines[i].Close - klines[i-1].Close
		if change > 0 {
			gains += change
		} else {
			losses += -change
		}
	}

	avgGain := gains / float64(period)
	avgLoss := losses / float64(period)

	// 使用Wilder平滑方法计算后续RSI
	for i := period + 1; i < len(klines); i++ {
		change := klines[i].Close - klines[i-1].Close
		if change > 0 {
			avgGain = (avgGain*float64(period-1) + change) / float64(period)
			avgLoss = (avgLoss * float64(period-1)) / float64(period)
		} else {
			avgGain = (avgGain * float64(period-1)) / float64(period)
			avgLoss = (avgLoss*float64(period-1) + (-change)) / float64(period)
		}
	}

	if avgLoss == 0 {
		return 100
	}

	rs := avgGain / avgLoss
	rsi := 100 - (100 / (1 + rs))

	return rsi
}

// calculateATR 计算ATR
func calculateATR(klines []Kline, period int) float64 {
	if len(klines) <= period {
		return 0
	}

	trs := make([]float64, len(klines))
	for i := 1; i < len(klines); i++ {
		high := klines[i].High
		low := klines[i].Low
		prevClose := klines[i-1].Close

		tr1 := high - low
		tr2 := math.Abs(high - prevClose)
		tr3 := math.Abs(low - prevClose)

		trs[i] = math.Max(tr1, math.Max(tr2, tr3))
	}

	// 计算初始ATR
	sum := 0.0
	for i := 1; i <= period; i++ {
		sum += trs[i]
	}
	atr := sum / float64(period)

	// Wilder平滑
	for i := period + 1; i < len(klines); i++ {
		atr = (atr*float64(period-1) + trs[i]) / float64(period)
	}

	return atr
}

// calculateIntradaySeries 计算日内系列数据 (基于15分钟K线)
func calculateIntradaySeries(klines []Kline) *IntradayData {
	data := &IntradayData{
		MidPrices: make([]float64, 0, 50),
		// 注释掉技术指标字段，default.txt 策略只使用结构分析
		// EMA20Values: make([]float64, 0, 50),
		// MACDValues:  make([]float64, 0, 50),
		// RSI7Values:  make([]float64, 0, 50),
		// RSI14Values: make([]float64, 0, 50),
	}

	// 获取最近50个数据点 (50 * 15分钟 ≈ 12.5小时历史)
	start := len(klines) - 50
	if start < 0 {
		start = 0
	}

	for i := start; i < len(klines); i++ {
		data.MidPrices = append(data.MidPrices, klines[i].Close)

		// 注释掉技术指标计算，default.txt 策略只使用结构分析
		// if i >= 19 {
		// 	ema20 := calculateEMA(klines[:i+1], 20)
		// 	data.EMA20Values = append(data.EMA20Values, ema20)
		// }

		// if i >= 25 {
		// 	macd := calculateMACD(klines[:i+1])
		// 	data.MACDValues = append(data.MACDValues, macd)
		// }

		// if i >= 7 {
		// 	rsi7 := calculateRSI(klines[:i+1], 7)
		// 	data.RSI7Values = append(data.RSI7Values, rsi7)
		// }
		// if i >= 14 {
		// 	rsi14 := calculateRSI(klines[:i+1], 14)
		// 	data.RSI14Values = append(data.RSI14Values, rsi14)
		// }
	}

	return data
}

// calculateLongerTermData 计算长期数据
func calculateLongerTermData(klines []Kline) *LongerTermData {
	data := &LongerTermData{
		// 注释掉技术指标字段，default.txt 策略只使用结构分析
		// MACDValues:  make([]float64, 0, 10),
		// RSI14Values: make([]float64, 0, 10),
	}

	// 收集4h收盘价序列
	for _, k := range klines {
		data.ClosePrices = append(data.ClosePrices, k.Close)
	}

	// 注释掉EMA计算，default.txt 策略只使用结构分析
	// data.EMA20 = calculateEMA(klines, 20)
	// data.EMA50 = calculateEMA(klines, 50)

	// 计算ATR
	data.ATR14 = calculateATR(klines, 14)

	// 计算RSI14（4h）
	data.RSI14 = calculateRSI(klines, 14)

	// 计算成交量
	if len(klines) > 0 {
		data.CurrentVolume = klines[len(klines)-1].Volume
		// 计算平均成交量
		sum := 0.0
		for _, k := range klines {
			sum += k.Volume
		}
		data.AverageVolume = sum / float64(len(klines))
	}

	// 计算MACD和RSI序列
	start := len(klines) - 10
	if start < 0 {
		start = 0
	}

	// 注释掉MACD和RSI计算，default.txt 策略只使用结构分析
	// for i := start; i < len(klines); i++ {
	// 	if i >= 25 {
	// 		macd := calculateMACD(klines[:i+1])
	// 		data.MACDValues = append(data.MACDValues, macd)
	// 	}
	// 	if i >= 14 {
	// 		rsi14 := calculateRSI(klines[:i+1], 14)
	// 		data.RSI14Values = append(data.RSI14Values, rsi14)
	// 	}
	// }

	return data
}

func calculateHourlyData(klines []Kline) *HourlyData {
	if len(klines) == 0 {
		return nil
	}
	data := &HourlyData{
		ClosePrices: make([]float64, 0, len(klines)),
	}
	for _, k := range klines {
		data.ClosePrices = append(data.ClosePrices, k.Close)
	}
	return data
}

// getOpenInterestData 获取OI数据（Binance 期货）
func getOpenInterestData(symbol string) (*OIData, error) {
	bnSymbol, ok := toBinanceSymbol(symbol)
	if !ok {
		return nil, fmt.Errorf("无法将符号 %s 映射为Binance交易对", symbol)
	}

	url := fmt.Sprintf("https://fapi.binance.com/fapi/v1/openInterest?symbol=%s", bnSymbol)

	var lastErr error
	const maxRetries = 3

	for attempt := 1; attempt <= maxRetries; attempt++ {
		if attempt > 1 {
			time.Sleep(2 * time.Second)
		}

		resp, err := http.Get(url)
		if err != nil {
			lastErr = err
			log.Printf("⚠️ 第%d次获取 %s OI 失败: %v", attempt, symbol, err)
			continue
		}

		body, err := ioutil.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			log.Printf("⚠️ 第%d次读取 %s OI 响应失败: %v", attempt, symbol, err)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("status %d, body %s", resp.StatusCode, truncateForLog(string(body)))
			log.Printf("⚠️ 第%d次获取 %s OI 返回异常: %v", attempt, bnSymbol, lastErr)
			continue
		}

		var result struct {
			OpenInterest string `json:"openInterest"`
			Symbol       string `json:"symbol"`
			Time         int64  `json:"time"`
		}

		if err := json.Unmarshal(body, &result); err != nil {
			lastErr = err
			log.Printf("⚠️ 第%d次解析 %s OI 响应失败: %v", attempt, bnSymbol, err)
			continue
		}

		oi, _ := strconv.ParseFloat(result.OpenInterest, 64)
		return &OIData{
			Latest:  oi,
			Average: oi * 0.999,
		}, nil
	}

	return nil, fmt.Errorf("获取OI失败: %w", lastErr)
}

// getFundingRate 获取资金费率（Binance 期货）
func getFundingRate(symbol string) (float64, error) {
	bnSymbol, ok := toBinanceSymbol(symbol)
	if !ok {
		return 0, fmt.Errorf("无法将符号 %s 映射为Binance交易对", symbol)
	}

	url := fmt.Sprintf("https://fapi.binance.com/fapi/v1/premiumIndex?symbol=%s", bnSymbol)

	resp, err := http.Get(url)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}

	var result struct {
		Symbol          string `json:"symbol"`
		MarkPrice       string `json:"markPrice"`
		IndexPrice      string `json:"indexPrice"`
		LastFundingRate string `json:"lastFundingRate"`
		NextFundingTime int64  `json:"nextFundingTime"`
		InterestRate    string `json:"interestRate"`
		Time            int64  `json:"time"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return 0, err
	}

	rate, _ := strconv.ParseFloat(result.LastFundingRate, 64)
	return rate, nil
}

// Format 格式化输出市场数据
func Format(data *Data) string {
	var sb strings.Builder

	// 使用动态精度格式化价格
	priceStr := formatPriceWithDynamicPrecision(data.CurrentPrice)
	sb.WriteString(fmt.Sprintf("current_price = %s\n\n", priceStr))
	// 注释掉技术指标显示，default.txt 策略只使用结构分析
	// sb.WriteString(fmt.Sprintf("current_price = %s, current_ema20 = %.3f, current_macd = %.3f, current_rsi (7 period, 15m) = %.3f\n\n",
	// 	priceStr, data.CurrentEMA20, data.CurrentMACD, data.CurrentRSI7))

	sb.WriteString(fmt.Sprintf("In addition, here is the latest %s open interest and funding rate for perps:\n\n",
		data.Symbol))

	if data.OpenInterest != nil {
		// 使用动态精度格式化 OI 数据
		oiLatestStr := formatPriceWithDynamicPrecision(data.OpenInterest.Latest)
		oiAverageStr := formatPriceWithDynamicPrecision(data.OpenInterest.Average)
		sb.WriteString(fmt.Sprintf("Open Interest: Latest: %s Average: %s\n\n",
			oiLatestStr, oiAverageStr))
	}

	sb.WriteString(fmt.Sprintf("Funding Rate: %.2e\n\n", data.FundingRate))

	if data.IntradaySeries != nil {
		sb.WriteString("Close prices (15m, oldest → latest):\n\n")

		if len(data.IntradaySeries.MidPrices) > 0 {
			sb.WriteString(fmt.Sprintf("%s\n\n", formatFloatSlice(data.IntradaySeries.MidPrices)))
		}

		sb.WriteString(fmt.Sprintf("RSI(7, 15m): %.2f\n\n", data.CurrentRSI7))

		// 注释掉技术指标显示，default.txt 策略只使用结构分析
		// if len(data.IntradaySeries.EMA20Values) > 0 {
		// 	sb.WriteString(fmt.Sprintf("EMA indicators (20‑period): %s\n\n", formatFloatSlice(data.IntradaySeries.EMA20Values)))
		// }

		// if len(data.IntradaySeries.MACDValues) > 0 {
		// 	sb.WriteString(fmt.Sprintf("MACD indicators: %s\n\n", formatFloatSlice(data.IntradaySeries.MACDValues)))
		// }

		// if len(data.IntradaySeries.RSI7Values) > 0 {
		// 	sb.WriteString(fmt.Sprintf("RSI indicators (7‑Period): %s\n\n", formatFloatSlice(data.IntradaySeries.RSI7Values)))
		// }

		// if len(data.IntradaySeries.RSI14Values) > 0 {
		// 	sb.WriteString(fmt.Sprintf("RSI indicators (14‑Period): %s\n\n", formatFloatSlice(data.IntradaySeries.RSI14Values)))
		// }
	}

	if data.HourlyContext != nil && len(data.HourlyContext.ClosePrices) > 0 {
		sb.WriteString("Close prices (1h, oldest → latest):\n\n")
		sb.WriteString(fmt.Sprintf("%s\n\n", formatFloatSlice(data.HourlyContext.ClosePrices)))
	}

	if data.LongerTermContext != nil {
		sb.WriteString("Longer‑term context (4‑hour timeframe):\n\n")

		// 注释掉EMA显示，default.txt 策略只使用结构分析
		// sb.WriteString(fmt.Sprintf("20‑Period EMA: %.3f vs. 50‑Period EMA: %.3f\n\n",
		// 	data.LongerTermContext.EMA20, data.LongerTermContext.EMA50))

		// 保留ATR和成交量数据，这些对结构分析和风险管理很重要
		sb.WriteString(fmt.Sprintf("14‑Period ATR: %.3f\n\n",
			data.LongerTermContext.ATR14))

		sb.WriteString(fmt.Sprintf("Current Volume: %.3f vs. Average Volume: %.3f\n\n",
			data.LongerTermContext.CurrentVolume, data.LongerTermContext.AverageVolume))

		sb.WriteString(fmt.Sprintf("RSI(14, 4h): %.2f\n\n", data.LongerTermContext.RSI14))

		if len(data.LongerTermContext.ClosePrices) > 0 {
			sb.WriteString("Close prices (4h, oldest → latest):\n\n")
			sb.WriteString(fmt.Sprintf("%s\n\n", formatFloatSlice(data.LongerTermContext.ClosePrices)))
		}

		// 注释掉技术指标显示，default.txt 策略只使用结构分析
		// if len(data.LongerTermContext.MACDValues) > 0 {
		// 	sb.WriteString(fmt.Sprintf("MACD indicators: %s\n\n", formatFloatSlice(data.LongerTermContext.MACDValues)))
		// }

		// if len(data.LongerTermContext.RSI14Values) > 0 {
		// 	sb.WriteString(fmt.Sprintf("RSI indicators (14‑Period): %s\n\n", formatFloatSlice(data.LongerTermContext.RSI14Values)))
		// }
	}

	return sb.String()
}

// formatPriceWithDynamicPrecision 根据价格区间动态选择精度
// 这样可以完美支持从超低价 meme coin (< 0.0001) 到 BTC/ETH 的所有币种
func formatPriceWithDynamicPrecision(price float64) string {
	switch {
	case price < 0.0001:
		// 超低价 meme coin: 1000SATS, 1000WHY, DOGS
		// 0.00002070 → "0.00002070" (8位小数)
		return fmt.Sprintf("%.8f", price)
	case price < 0.001:
		// 低价 meme coin: NEIRO, HMSTR, HOT, NOT
		// 0.00015060 → "0.000151" (6位小数)
		return fmt.Sprintf("%.6f", price)
	case price < 0.01:
		// 中低价币: PEPE, SHIB, MEME
		// 0.00556800 → "0.005568" (6位小数)
		return fmt.Sprintf("%.6f", price)
	case price < 1.0:
		// 低价币: ASTER, DOGE, ADA, TRX
		// 0.9954 → "0.9954" (4位小数)
		return fmt.Sprintf("%.4f", price)
	case price < 100:
		// 中价币: SOL, AVAX, LINK, MATIC
		// 23.4567 → "23.4567" (4位小数)
		return fmt.Sprintf("%.4f", price)
	default:
		// 高价币: BTC, ETH (节省 Token)
		// 45678.9123 → "45678.91" (2位小数)
		return fmt.Sprintf("%.2f", price)
	}
}

// formatFloatSlice 格式化float64切片为字符串（使用动态精度）
func formatFloatSlice(values []float64) string {
	strValues := make([]string, len(values))
	for i, v := range values {
		strValues[i] = formatPriceWithDynamicPrecision(v)
	}
	return "[" + strings.Join(strValues, ", ") + "]"
}

// Normalize 基础符号标准化（大小写）
func Normalize(symbol string) string {
	return strings.ToUpper(symbol)
}

// toBinanceSymbol 将内部符号转换为 Binance 期货/现货使用的交易对格式。
// 规则：
// - 已含 USDT/USDC/BUSD/USD 后缀则直接返回（去除中划线）
// - 含 ":"（如商品前缀）视为非 Binance 资产，返回 false
// - 含 "-PERP" 去除后缀再处理
// - 其余默认追加 "USDT"
func toBinanceSymbol(symbol string) (string, bool) {
	s := strings.ToUpper(strings.TrimSpace(symbol))

	// Hyperliquid 或自定义前缀的商品类，不适配 Binance
	if strings.Contains(s, ":") {
		return "", false
	}

	// 去除常见衍生品后缀和分隔符
	s = strings.TrimSuffix(s, "-PERP")
	s = strings.ReplaceAll(s, "-", "")

	hasQuote := strings.HasSuffix(s, "USDT") || strings.HasSuffix(s, "USDC") ||
		strings.HasSuffix(s, "BUSD") || strings.HasSuffix(s, "USD")

	if hasQuote {
		return s, true
	}

	return s + "USDT", true
}

func truncateForLog(s string) string {
	const maxLen = 200
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// parseFloat 解析float值
func parseFloat(v interface{}) (float64, error) {
	switch val := v.(type) {
	case string:
		return strconv.ParseFloat(val, 64)
	case float64:
		return val, nil
	case int:
		return float64(val), nil
	case int64:
		return float64(val), nil
	default:
		return 0, fmt.Errorf("unsupported type: %T", v)
	}
}
