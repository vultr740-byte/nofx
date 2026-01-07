package charts

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/chromedp/chromedp"
	echarts "github.com/go-echarts/go-echarts/v2/charts"
	"github.com/go-echarts/go-echarts/v2/components"
	"github.com/go-echarts/go-echarts/v2/opts"

	"nofx/market"
)

// Generator 负责拉取K线并生成带买入标记的价格走势图（PNG）。
type Generator struct {
	api *market.APIClient
}

func NewGenerator() *Generator {
	return &Generator{api: market.NewAPIClient()}
}

// BuildKlinePNG 生成指定交易对的K线图，entryPrice 为可选买入价（<=0 表示不标注）。
func (g *Generator) BuildKlinePNG(ctx context.Context, symbol, interval string, limit int, entryPrice float64) ([]byte, error) {
	if limit <= 0 {
		limit = 150
	}
	if interval == "" {
		interval = "15m"
	}

	klines, err := g.api.GetKlines(symbol, interval, limit)
	if err != nil {
		return nil, fmt.Errorf("拉取K线失败: %w", err)
	}
	if len(klines) == 0 {
		return nil, fmt.Errorf("未获取到K线数据")
	}

	xAxis := make([]string, 0, len(klines))
	series := make([]opts.KlineData, 0, len(klines))
	for _, k := range klines {
		xAxis = append(xAxis, time.UnixMilli(k.OpenTime).Format("01-02 15:04"))
		series = append(series, opts.KlineData{Value: [4]float64{k.Open, k.Close, k.Low, k.High}})
	}

	kline := echarts.NewKLine()
	kline.SetGlobalOptions(
		echarts.WithInitializationOpts(opts.Initialization{ChartID: "kline", Width: "900px", Height: "500px"}),
		echarts.WithTitleOpts(opts.Title{Title: fmt.Sprintf("%s %s", symbol, interval)}),
		echarts.WithTooltipOpts(opts.Tooltip{Show: opts.Bool(true), Trigger: "axis"}),
		echarts.WithXAxisOpts(opts.XAxis{SplitNumber: 20}),
		echarts.WithYAxisOpts(opts.YAxis{Scale: opts.Bool(true)}),
		echarts.WithDataZoomOpts(opts.DataZoom{Type: "inside"}, opts.DataZoom{Type: "slider"}),
	)

	seriesOpts := []echarts.SeriesOpts{
		echarts.WithItemStyleOpts(opts.ItemStyle{Color: "#26a69a", Color0: "#ef5350"}),
	}

	if entryPrice > 0 {
		seriesOpts = append(seriesOpts,
			echarts.WithMarkLineStyleOpts(opts.MarkLineStyle{LineStyle: &opts.LineStyle{Color: "#ff9800", Width: 1.2, Type: "dashed"}}),
			echarts.WithMarkLineNameYAxisItemOpts(opts.MarkLineNameYAxisItem{YAxis: entryPrice, Name: "Entry"}),
		)
	}

	kline.SetXAxis(xAxis).AddSeries("kline", series, seriesOpts...)

	page := components.NewPage()
	page.AddCharts(kline)

	var htmlBuf bytes.Buffer
	if err := page.Render(&htmlBuf); err != nil {
		return nil, fmt.Errorf("渲染HTML失败: %w", err)
	}

	png, err := renderHTMLToPNG(ctx, htmlBuf.String())
	if err != nil {
		return nil, fmt.Errorf("渲染图表截图失败: %w", err)
	}

	return png, nil
}

// renderHTMLToPNG 使用 chromedp 将内联 HTML 渲染为 PNG。
func renderHTMLToPNG(ctx context.Context, html string) ([]byte, error) {
	c, cancel := chromedp.NewContext(ctx)
	defer cancel()

	dataURL := "data:text/html;charset=utf-8," + url.QueryEscape(html)
	var buf []byte
	err := chromedp.Run(c,
		chromedp.Navigate(dataURL),
		chromedp.EmulateViewport(900, 520),
		chromedp.Sleep(2*time.Second),
		chromedp.FullScreenshot(&buf, 90),
	)
	if err != nil {
		return nil, err
	}
	return buf, nil
}
