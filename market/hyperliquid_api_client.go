package market

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// HLAPIClient 调用 Hyperliquid Info API 获取历史 K 线
type HLAPIClient struct {
	client  *http.Client
	baseURL string
}

func NewHLAPIClient() *HLAPIClient {
	return &HLAPIClient{
		client:  &http.Client{Timeout: 10 * time.Second},
		baseURL: "https://api.hyperliquid.xyz/info",
	}
}

type candleSnapshotReq struct {
	Type string           `json:"type"`
	Req  candleSnapshotIn `json:"req"`
}

type candleSnapshotIn struct {
	Coin      string `json:"coin"`
	Interval  string `json:"interval"`
	StartTime int64  `json:"startTime"`
	EndTime   int64  `json:"endTime"`
}

type candleSnapshotOut struct {
	T    int64  `json:"t"` // open time
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

// GetKlines 获取最近 limit 根 K 线（使用 candleSnapshot）
func (c *HLAPIClient) GetKlines(symbol, interval string, limit int) ([]Kline, error) {
	now := time.Now().UnixMilli()
	msPer := intervalToMs(interval)
	if msPer == 0 {
		return nil, fmt.Errorf("unsupported interval: %s", interval)
	}
	end := now
	start := now - int64(limit)*msPer

	reqBody := candleSnapshotReq{
		Type: "candleSnapshot",
		Req: candleSnapshotIn{
			Coin:      ToHLSymbol(symbol),
			Interval:  interval,
			StartTime: start,
			EndTime:   end,
		},
	}

	raw, _ := json.Marshal(reqBody)
	resp, err := c.client.Post(c.baseURL, "application/json", bytes.NewBuffer(raw))
	if err != nil {
		return nil, fmt.Errorf("HL candleSnapshot request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HL candleSnapshot status %d: %s", resp.StatusCode, string(body))
	}

	var out []candleSnapshotOut
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode candleSnapshot: %w", err)
	}

	klines := make([]Kline, 0, len(out))
	for _, o := range out {
		open, _ := parseFloat(o.O)
		closep, _ := parseFloat(o.C)
		high, _ := parseFloat(o.H)
		low, _ := parseFloat(o.L)
		vol, _ := parseFloat(o.V)
		klines = append(klines, Kline{
			OpenTime:  o.T,
			CloseTime: o.TEnd,
			Open:      open,
			Close:     closep,
			High:      high,
			Low:       low,
			Volume:    vol,
			Trades:    o.N,
		})
	}
	return klines, nil
}

// intervalToMs 将 HL 间隔字符串转换为毫秒
func intervalToMs(interval string) int64 {
	switch interval {
	case "1m":
		return 60 * 1000
	case "3m":
		return 3 * 60 * 1000
	case "5m":
		return 5 * 60 * 1000
	case "15m":
		return 15 * 60 * 1000
	case "30m":
		return 30 * 60 * 1000
	case "1h":
		return 60 * 60 * 1000
	case "2h":
		return 2 * 60 * 60 * 1000
	case "4h":
		return 4 * 60 * 60 * 1000
	case "8h":
		return 8 * 60 * 60 * 1000
	case "12h":
		return 12 * 60 * 60 * 1000
	case "1d":
		return 24 * 60 * 60 * 1000
	case "3d":
		return 3 * 24 * 60 * 60 * 1000
	case "1w":
		return 7 * 24 * 60 * 60 * 1000
	case "1M":
		return 30 * 24 * 60 * 60 * 1000
	default:
		return 0
	}
}
