package market

import (
	"encoding/json"
	"log"
	"strconv"

	"nofx/hyperws"
)

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

// subscribeHLCandle 通过全局 WS manager 订阅 candle，返回取消订阅函数
func subscribeHLCandle(symbol, interval string, onCandle func(HLCandle)) func() {
	ws := hyperws.Get("wss://api.hyperliquid.xyz/ws")
	payload := map[string]interface{}{
		"method": "subscribe",
		"subscription": map[string]interface{}{
			"type":     "candle",
			"coin":     symbol,
			"interval": interval,
		},
	}

	unsub := ws.Subscribe("candle", payload, func(_ string, data json.RawMessage) {
		var msg struct {
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
		}
		if err := json.Unmarshal(data, &msg); err != nil {
			return
		}
		if msg.S != symbol || msg.I != interval {
			return
		}
		parse := func(s string) float64 {
			f, _ := strconv.ParseFloat(s, 64)
			return f
		}
		c := HLCandle{
			StartTime: msg.T,
			EndTime:   msg.TEnd,
			Symbol:    msg.S,
			Interval:  msg.I,
			Open:      parse(msg.O),
			Close:     parse(msg.C),
			High:      parse(msg.H),
			Low:       parse(msg.L),
			Volume:    parse(msg.V),
			Trades:    msg.N,
		}
		onCandle(c)
	})

	log.Printf("✅ 订阅 HL candle: %s %s", symbol, interval)
	return unsub
}
