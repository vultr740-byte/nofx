package charts

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/wcharczuk/go-chart/v2"

	"nofx/market"
)

// Generator 负责拉取K线并生成带买入标记的价格走势图（PNG），无需浏览器依赖。
type Generator struct {
	api *market.APIClient
}

func NewGenerator() *Generator {
	return &Generator{api: market.NewAPIClient()}
}

// BuildKlinePNG 生成指定交易对的K线图，entryPrice 可选（<=0 表示不标注）。
// interval: Binance 间隔（如 15m/1h），limit: K线数量。
func (g *Generator) BuildKlinePNG(ctx context.Context, symbol, interval string, limit int, entryPrice float64) ([]byte, error) {
	if limit <= 0 {
		limit = 150
	}
	if interval == "" {
		interval = "15m"
	}

	// 拉取 K 线（无 ctx 支持的原有接口，这里忽略 ctx 取消；接口超时由 client 控制）
	klines, err := g.api.GetKlines(symbol, interval, limit)
	if err != nil {
		return nil, fmt.Errorf("拉取K线失败: %w", err)
	}
	if len(klines) == 0 {
		return nil, fmt.Errorf("未获取到K线数据")
	}

	xTimes := make([]time.Time, 0, len(klines))
	yVals := make([]float64, 0, len(klines))
	for _, k := range klines {
		t := time.UnixMilli(k.OpenTime)
		xTimes = append(xTimes, t)
		yVals = append(yVals, k.Close)
	}

	priceSeries := chart.TimeSeries{
		Name:    fmt.Sprintf("%s %s", symbol, interval),
		XValues: xTimes,
		YValues: yVals,
		Style: chart.Style{
			StrokeColor: chart.ColorBlue,
			StrokeWidth: 1.5,
		},
	}

	// 可选的买入价标记（水平线）
	var entrySeries *chart.ContinuousSeries
	if entryPrice > 0 {
		x1 := float64(xTimes[0].Unix())
		x2 := float64(xTimes[len(xTimes)-1].Unix())
		entrySeries = &chart.ContinuousSeries{
			Style: chart.Style{
				StrokeColor:     chart.ColorOrange,
				StrokeWidth:     1.0,
				StrokeDashArray: []float64{5, 5},
			},
			XValues: []float64{x1, x2},
			YValues: []float64{entryPrice, entryPrice},
			Name:    "Entry",
		}
	}

	graph := chart.Chart{
		Background: chart.Style{
			Padding: chart.Box{
				Top:    20,
				Left:   10,
				Right:  10,
				Bottom: 20,
			},
			FillColor: chart.ColorBlack,
		},
		XAxis: chart.XAxis{
			ValueFormatter: chart.TimeDateValueFormatter,
			Style: chart.Style{
				StrokeColor: chart.ColorAlternateGray,
				FontColor:   chart.ColorWhite,
			},
		},
		YAxis: chart.YAxis{
			Style: chart.Style{
				StrokeColor: chart.ColorAlternateGray,
				FontColor:   chart.ColorWhite,
			},
		},
		Series: []chart.Series{
			priceSeries,
		},
	}

	if entrySeries != nil {
		graph.Series = append(graph.Series, entrySeries)
	}

	graph.Elements = []chart.Renderable{
		chart.Legend(&graph, chart.Style{
			FontColor: chart.ColorWhite,
		}),
	}

	var buf bytes.Buffer
	if err := graph.Render(chart.PNG, &buf); err != nil {
		return nil, fmt.Errorf("渲染图表失败: %w", err)
	}
	return buf.Bytes(), nil
}
