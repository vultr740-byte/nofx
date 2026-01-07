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

// BuildKlinePNG 生成指定交易对的K线图，entryPrice 可选（<=0 表示不标注）。
// interval: Binance 间隔（如 15m/1h），limit: K线数量。
func (g *Generator) BuildKlinePNG(ctx context.Context, symbol, interval string, limit int, entryPrice float64, entrySide string) ([]byte, error) {
	if limit <= 0 {
		limit = 150
	}
	if interval == "" {
		interval = "15m"
	}
	entrySide = strings.ToLower(strings.TrimSpace(entrySide))

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

	longColor := drawing.ColorFromHex("00C853")
	shortColor := drawing.ColorFromHex("FF5252")
	entryColor := drawing.ColorFromHex("FFB300")
	if entrySide == "long" {
		entryColor = longColor
	} else if entrySide == "short" {
		entryColor = shortColor
	}

	// 计算范围并增加边距（确保标注不贴边）
	minY, maxY := yVals[0], yVals[0]
	for _, v := range yVals[1:] {
		minY = math.Min(minY, v)
		maxY = math.Max(maxY, v)
	}
	if entryPrice > 0 {
		minY = math.Min(minY, entryPrice)
		maxY = math.Max(maxY, entryPrice)
	}
	yPad := (maxY - minY) * 0.08
	if yPad <= 0 {
		yPad = math.Max(1, math.Abs(maxY)*0.01)
	}

	xMin := chart.TimeToFloat64(xTimes[0])
	xMax := chart.TimeToFloat64(xTimes[len(xTimes)-1])
	step := 15 * time.Minute
	if len(xTimes) >= 2 {
		if d := xTimes[1].Sub(xTimes[0]); d > 0 {
			step = d
		}
	}
	xPad := float64(step * 3) // 右侧留出 3 根K线的空间，放标注气泡

	priceSeries := chart.TimeSeries{
		Name:    "Price",
		XValues: xTimes,
		YValues: yVals,
		Style: chart.Style{
			StrokeColor: priceLine,
			StrokeWidth: 1.8,
		},
	}

	// 可选的入场价标记（水平线 + 点 + 气泡标注）
	var entryLine *chart.TimeSeries
	var entryDot *chart.TimeSeries
	var entryLabel *chart.AnnotationSeries
	if entryPrice > 0 {
		entryLine = &chart.TimeSeries{
			Name:    "Entry",
			XValues: []time.Time{xTimes[0], xTimes[len(xTimes)-1]},
			YValues: []float64{entryPrice, entryPrice},
			Style: chart.Style{
				StrokeColor:     entryColor,
				StrokeWidth:     1.0,
				StrokeDashArray: []float64{6, 6},
			},
		}
		entryDot = &chart.TimeSeries{
			Name:    "Entry point",
			XValues: []time.Time{xTimes[len(xTimes)-1]},
			YValues: []float64{entryPrice},
			Style: chart.Style{
				StrokeWidth: chart.Disabled,
				DotWidth:    5,
				DotColor:    entryColor,
			},
		}

		sideText := ""
		if entrySide == "long" {
			sideText = " (L)"
		} else if entrySide == "short" {
			sideText = " (S)"
		}

		label := fmt.Sprintf("ENTRY %.4f%s", entryPrice, sideText)
		entryLabel = &chart.AnnotationSeries{
			Style: chart.Style{
				FillColor:   plotBg,
				StrokeColor: entryColor,
				StrokeWidth: 1.0,
				FontColor:   entryColor,
				FontSize:    11,
			},
			Annotations: []chart.Value2{
				{
					XValue: chart.TimeToFloat64(xTimes[len(xTimes)-1]),
					YValue: entryPrice,
					Label:  label,
				},
			},
		}
	}

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
			Range: &chart.ContinuousRange{
				Min: xMin,
				Max: xMax + xPad,
			},
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
				Min: minY - yPad,
				Max: maxY + yPad,
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

	if entryLine != nil || entryDot != nil || entryLabel != nil {
		series := make([]chart.Series, 0, 4)
		if entryLine != nil {
			series = append(series, *entryLine)
		}
		series = append(series, priceSeries)
		if entryDot != nil {
			series = append(series, *entryDot)
		}
		if entryLabel != nil {
			series = append(series, *entryLabel)
		}
		graph.Series = series
	}

	var buf bytes.Buffer
	if err := graph.Render(chart.PNG, &buf); err != nil {
		return nil, fmt.Errorf("渲染图表失败: %w", err)
	}
	return buf.Bytes(), nil
}
