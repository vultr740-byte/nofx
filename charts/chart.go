package charts

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/wcharczuk/go-chart/v2"
	"github.com/wcharczuk/go-chart/v2/drawing"

	"nofx/market"
)

// Generator 负责拉取K线并生成带买入标记的价格走势图（PNG），无需浏览器依赖。
type Generator struct {
	api *market.APIClient
}

func NewGenerator() *Generator {
	return &Generator{api: market.NewAPIClient()}
}

// BuildKlinePNG 生成指定交易对的价格走势图，并可选叠加入场/止损/止盈水平线（<=0 表示不展示）。
// interval: Binance 间隔（如 15m/1h），limit: K线数量。
func (g *Generator) BuildKlinePNG(ctx context.Context, symbol, interval string, limit int, entryPrice float64, stopLoss float64, takeProfit float64) ([]byte, error) {
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

	// 主题（偏金融化）
	bg := drawing.ColorFromHex("0B1220")        // 深蓝黑背景
	plotBg := drawing.ColorFromHex("0F1A2B")    // 画布区域
	grid := drawing.ColorFromHex("263445")      // 网格线/轴
	text := drawing.ColorFromHex("9FB0C3")      // 坐标/标题文字
	priceLine := drawing.ColorFromHex("4C78FF") // 价格线

	entryColor := drawing.ColorFromHex("FFB300")      // 入场线（橙黄）
	stopLossColor := drawing.ColorFromHex("FF5252")   // 止损线（红）
	takeProfitColor := drawing.ColorFromHex("00C853") // 止盈线（绿）

	// 计算价格范围并增加边距（Y 轴只按价格走势计算；其它水平线超出范围则不展示）
	minY, maxY := yVals[0], yVals[0]
	for _, v := range yVals[1:] {
		minY = math.Min(minY, v)
		maxY = math.Max(maxY, v)
	}
	yPad := (maxY - minY) * 0.08
	if yPad <= 0 {
		yPad = math.Max(1, math.Abs(maxY)*0.01)
	}

	yMin := minY - yPad
	yMax := maxY + yPad

	priceSeries := chart.TimeSeries{
		Name:    "Price",
		XValues: xTimes,
		YValues: yVals,
		Style: chart.Style{
			StrokeColor: priceLine,
			StrokeWidth: 1.8,
		},
	}

	// 可选的水平线标记（入场 / 止损 / 止盈）：只有落在 Y 轴范围内才展示
	addLevelLine := func(level float64, color drawing.Color) *chart.TimeSeries {
		if level <= 0 {
			return nil
		}
		if level < yMin || level > yMax {
			return nil
		}
		return &chart.TimeSeries{
			XValues: []time.Time{xTimes[0], xTimes[len(xTimes)-1]},
			YValues: []float64{level, level},
			Style: chart.Style{
				StrokeColor:     color,
				StrokeWidth:     1.0,
				StrokeDashArray: []float64{6, 6},
			},
		}
	}
	entryLine := addLevelLine(entryPrice, entryColor)
	stopLossLine := addLevelLine(stopLoss, stopLossColor)
	takeProfitLine := addLevelLine(takeProfit, takeProfitColor)

	timeFmt := "01-02 15:04"
	if strings.HasSuffix(interval, "m") {
		timeFmt = "15:04"
	}

	graph := chart.Chart{
		Title: fmt.Sprintf("%s %s", symbol, interval),
		TitleStyle: chart.Style{
			FontColor: text,
			FontSize:  14,
		},
		Width:  960,
		Height: 420,
		Background: chart.Style{
			Padding: chart.Box{
				Top:    36,
				Left:   16,
				Right:  18,
				Bottom: 18,
			},
			FillColor: bg,
		},
		Canvas: chart.Style{
			FillColor: plotBg,
		},
		XAxis: chart.XAxis{
			ValueFormatter: chart.TimeValueFormatterWithFormat(timeFmt),
			Style: chart.Style{
				StrokeColor: grid,
				FontColor:   text,
				FontSize:    10,
			},
			GridMajorStyle: chart.Style{
				StrokeColor: grid,
				StrokeWidth: 0.5,
			},
		},
		YAxis: chart.YAxis{
			Range: &chart.ContinuousRange{
				Min: yMin,
				Max: yMax,
			},
			Style: chart.Style{
				StrokeColor: grid,
				FontColor:   text,
				FontSize:    10,
			},
			GridMajorStyle: chart.Style{
				StrokeColor: grid,
				StrokeWidth: 0.5,
			},
		},
		Series: []chart.Series{priceSeries},
	}

	if entryLine != nil || stopLossLine != nil || takeProfitLine != nil {
		series := make([]chart.Series, 0, 4)
		if entryLine != nil {
			series = append(series, *entryLine)
		}
		if stopLossLine != nil {
			series = append(series, *stopLossLine)
		}
		if takeProfitLine != nil {
			series = append(series, *takeProfitLine)
		}
		series = append(series, priceSeries) // 价格线放最后，覆盖在水平线之上
		graph.Series = series
	}

	var buf bytes.Buffer
	if err := graph.Render(chart.PNG, &buf); err != nil {
		return nil, fmt.Errorf("渲染图表失败: %w", err)
	}
	return buf.Bytes(), nil
}
