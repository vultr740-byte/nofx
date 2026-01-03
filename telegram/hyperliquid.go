package telegram

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"nofx/trader"
)

// PerpMetaAsset 描述 allPerpMetas 返回的资产元数据
type PerpMetaAsset struct {
	Name          string `json:"name"`
	SzDecimals    int    `json:"szDecimals"`
	PxDecimals    *int   `json:"pxDecimals,omitempty"`
	MaxLeverage   int    `json:"maxLeverage"`
	MarginTableID int    `json:"marginTableId"`
	IsDelisted    bool   `json:"isDelisted,omitempty"`
	OnlyIsolated  bool   `json:"onlyIsolated,omitempty"`
	MarginMode    string `json:"marginMode,omitempty"`
	GrowthMode    string `json:"growthMode,omitempty"`
}

// PerpMetaResponse 对应 allPerpMetas 的单个响应对象（顶层是数组）
type PerpMetaResponse struct {
	Universe []PerpMetaAsset `json:"universe"`
}

// HyperliquidService Hyperliquid 服务
type HyperliquidService struct{}

// StockCategory 股票资产分类统计
type StockCategory struct {
	Name string
	List []PerpMetaAsset
}

// OrderHistoryItem 订单/成交摘要
type OrderHistoryItem struct {
	Symbol    string
	Side      string
	Dir       string
	Price     float64
	Size      float64
	Fee       float64
	ClosedPnl float64
	Timestamp time.Time
	StopPrice *float64 // 若有止损价格则填充
}

// GetOrderHistory 获取历史成交并格式化
func (s *HyperliquidService) GetOrderHistory(agentKey, walletAddr string, testnet bool, lookback time.Duration, limit int, loc *time.Location) (string, error) {
	traderObj, err := trader.NewHyperliquidTrader(agentKey, walletAddr, testnet)
	if err != nil {
		return "", fmt.Errorf("创建 Hyperliquid 交易器失败: %w", err)
	}

	start := time.Now().Add(-lookback)
	fills, err := traderObj.GetUserFillsByTime(start, nil)
	if err != nil {
		return "", fmt.Errorf("获取历史成交失败: %w", err)
	}

	items := make([]OrderHistoryItem, 0, len(fills))
	for _, f := range fills {
		items = append(items, OrderHistoryItem{
			Symbol:    f.Coin,
			Side:      f.Side,
			Dir:       f.Dir,
			Price:     parseFloatSafe(f.Price),
			Size:      parseFloatSafe(f.Size),
			Fee:       parseFloatSafe(f.Fee),
			ClosedPnl: parseFloatSafe(f.ClosedPnl),
			Timestamp: time.UnixMilli(f.Time),
		})
	}

	// 按时间倒序
	sort.Slice(items, func(i, j int) bool {
		return items[i].Timestamp.After(items[j].Timestamp)
	})

	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}

	return formatOrderHistory(items, lookback, loc), nil
}

// CountFills 获取指定时间窗口内的成交条数
func (s *HyperliquidService) CountFills(agentKey, walletAddr string, testnet bool, lookback time.Duration) (int, error) {
	traderObj, err := trader.NewHyperliquidTrader(agentKey, walletAddr, testnet)
	if err != nil {
		return 0, fmt.Errorf("创建 Hyperliquid 交易器失败: %w", err)
	}

	start := time.Now().Add(-lookback)
	fills, err := traderObj.GetUserFillsByTime(start, nil)
	if err != nil {
		return 0, fmt.Errorf("获取历史成交失败: %w", err)
	}
	return len(fills), nil
}

// GetStockCategories 获取股票资产分类
func (s *HyperliquidService) GetStockCategories(agentKey, walletAddr string, testnet bool) ([]StockCategory, error) {
	trader, err := trader.NewHyperliquidTrader(agentKey, walletAddr, testnet)
	if err != nil {
		return nil, fmt.Errorf("创建 Hyperliquid 交易器失败: %w", err)
	}

	assets, err := s.GetAllPerpMetas(trader)
	if err != nil {
		return nil, fmt.Errorf("获取资产信息失败: %w", err)
	}
	return categorizeStocks(assets), nil
}

// NewHyperliquidService 创建 Hyperliquid 服务
func NewHyperliquidService() *HyperliquidService {
	return &HyperliquidService{}
}

func floorToDecimals(value float64, decimals int) float64 {
	if decimals < 0 {
		return value
	}
	pow := math.Pow(10, float64(decimals))
	return math.Floor(value*pow) / pow
}

// FetchBalance 获取原始余额信息，供外层业务复用
func (s *HyperliquidService) FetchBalance(agentKey, walletAddr string, testnet bool) (map[string]interface{}, error) {
	// 创建 Hyperliquid 交易器
	trader, err := trader.NewHyperliquidTrader(agentKey, walletAddr, testnet)
	if err != nil {
		return nil, fmt.Errorf("创建 Hyperliquid 交易器失败: %w", err)
	}

	// 获取余额
	balance, err := trader.GetBalance()
	if err != nil {
		return nil, fmt.Errorf("获取余额失败: %w", err)
	}

	return balance, nil
}

// GetBalance 获取余额并格式化为 Telegram 消息
func (s *HyperliquidService) GetBalance(agentKey, walletAddr string, testnet bool) (string, error) {
	balance, err := s.FetchBalance(agentKey, walletAddr, testnet)
	if err != nil {
		return "", err
	}

	// 格式化余额信息
	return s.formatBalanceMessage(balance), nil
}

// GetBalanceWithAutoTransfer 仅在明确请求（如 /balance 命令）时自动将现货划转至合约
func (s *HyperliquidService) GetBalanceWithAutoTransfer(agentKey, walletAddr string, testnet bool) (string, error) {
	// 创建 Hyperliquid 交易器
	trader, err := trader.NewHyperliquidTrader(agentKey, walletAddr, testnet)
	if err != nil {
		return "", fmt.Errorf("创建 Hyperliquid 交易器失败: %w", err)
	}

	// 获取初始余额
	balance, err := trader.GetBalance()
	if err != nil {
		return "", fmt.Errorf("获取余额失败: %w", err)
	}

	// 若存在现货 USDC 余额，自动划转到合约账户后重新获取余额
	spotTransferable := 0.0
	if v, ok := balance["spotTransferable"].(float64); ok {
		spotTransferable = v
	} else if v, ok := balance["spotBalance"].(float64); ok {
		spotTransferable = v
	}
	spotTransferable = floorToDecimals(spotTransferable-1e-9, 6)

	if spotTransferable > 0.0001 {
		if err := trader.TransferSpotToPerp(spotTransferable); err != nil {
			log.Printf("⚠️ 现货自动划转失败: %v", err)
			balance["autoTransferError"] = err.Error()
		} else {
			log.Printf("🔄 已自动将现货 %.4f USDC 划转至合约账户", spotTransferable)
			balance["autoTransferred"] = spotTransferable

			// 重新获取余额以展示划转后的数值
			if refreshed, err := trader.GetBalance(); err == nil {
				balance = refreshed
				balance["autoTransferred"] = spotTransferable
			} else {
				log.Printf("⚠️ 划转后刷新余额失败: %v", err)
			}
		}
	}

	return s.formatBalanceMessage(balance), nil
}

// GetPositions 获取持仓并格式化为 Telegram 消息
func (s *HyperliquidService) GetPositions(agentKey, walletAddr string, testnet bool) (string, error) {
	// 创建 Hyperliquid 交易器
	trader, err := trader.NewHyperliquidTrader(agentKey, walletAddr, testnet)
	if err != nil {
		return "", fmt.Errorf("创建 Hyperliquid 交易器失败: %w", err)
	}

	// 获取持仓
	positions, err := trader.GetPositions()
	if err != nil {
		return "", fmt.Errorf("获取持仓失败: %w", err)
	}

	// 获取余额以获取总资产
	balance, balanceErr := trader.GetBalance()
	totalAssets := 0.0
	if balanceErr == nil {
		if total, ok := balance["totalWalletBalance"].(float64); ok {
			totalAssets = total
		}
	}

	// 格式化持仓信息（传入总资产）
	return s.formatPositionsMessage(positions, totalAssets), nil
}

// GetPositionsWithData 获取持仓并返回消息和原始数据
func (s *HyperliquidService) GetPositionsWithData(agentKey, walletAddr string, testnet bool) (message string, positions []map[string]interface{}, err error) {
	// 添加panic恢复机制，防止整个程序崩溃，并向上游返回错误避免发送空消息
	defer func() {
		if r := recover(); r != nil {
			log.Printf("GetPositionsWithData panic recovered: %v", r)
			err = fmt.Errorf("获取持仓失败: %v", r)
		}
	}()

	// 创建 Hyperliquid 交易器
	trader, newErr := trader.NewHyperliquidTrader(agentKey, walletAddr, testnet)
	if newErr != nil {
		err = fmt.Errorf("创建 Hyperliquid 交易器失败: %w", newErr)
		return
	}

	// 获取持仓
	positions, err = trader.GetPositions()
	if err != nil {
		err = fmt.Errorf("获取持仓失败: %w", err)
		return
	}

	// 获取余额以获取总资产
	balance, balanceErr := trader.GetBalance()
	totalAssets := 0.0
	if balanceErr == nil {
		if total, ok := balance["totalWalletBalance"].(float64); ok {
			totalAssets = total
		}
	}

	// 格式化持仓信息（传入总资产）
	message = s.formatPositionsMessage(positions, totalAssets)
	return
}

// formatBalanceMessage 格式化余额消息
func (s *HyperliquidService) formatBalanceMessage(balance map[string]interface{}) string {
	totalWalletBalance, _ := balance["totalWalletBalance"].(float64)
	availableBalance, _ := balance["availableBalance"].(float64)
	totalUnrealizedProfit, _ := balance["totalUnrealizedProfit"].(float64)
	spotBalance, _ := balance["spotBalance"].(float64)
	totalPosition, _ := balance["totalPosition"].(float64)
	autoTransferErr, _ := balance["autoTransferError"].(string)

	// 计算盈亏百分比
	var profitPercent float64
	if totalWalletBalance-totalUnrealizedProfit > 0 {
		profitPercent = (totalUnrealizedProfit / (totalWalletBalance - totalUnrealizedProfit)) * 100
	}

	// 盈亏表情符号
	pnlEmoji := "📊"
	if totalUnrealizedProfit > 0 {
		pnlEmoji = "🟢"
	} else if totalUnrealizedProfit < 0 {
		pnlEmoji = "🔴"
	}

	message := fmt.Sprintf(`总资产: %.2f USDC

📊 资产详情:
• 现货余额: %.2f USDC
• 可用余额: %.2f USDC
• 总持仓: %.2f USDC

%s 盈亏状况:
• 未实现盈亏: %.2f USDC (%.2f%%)`,
		totalWalletBalance,
		spotBalance,
		availableBalance,
		totalPosition,
		pnlEmoji,
		totalUnrealizedProfit,
		profitPercent,
	)

	if autoTransferErr != "" {
		message += fmt.Sprintf("\n⚠️ 自动划转失败: %s", autoTransferErr)
	}

	return message
}

// formatPositionsMessage 格式化持仓消息
func (s *HyperliquidService) formatPositionsMessage(positions []map[string]interface{}, totalAssets float64) string {
	if len(positions) == 0 {
		return `🕒 暂无持仓

💡 去充值 /deposit`
	}

	var message strings.Builder

	for i, pos := range positions {
		symbol, _ := pos["symbol"].(string)
		side, _ := pos["side"].(string)
		positionAmt, _ := pos["positionAmt"].(float64)
		entryPrice, _ := pos["entryPrice"].(float64)
		markPrice, _ := pos["markPrice"].(float64)
		unRealizedProfit, _ := pos["unRealizedProfit"].(float64)
		leverage, _ := pos["leverage"].(float64)
		stopLoss, _ := pos["stopLoss"].(float64)
		takeProfit, _ := pos["takeProfit"].(float64)

		// 计算盈亏百分比
		var profitPercent float64
		if entryPrice > 0 && positionAmt > 0 {
			profitPercent = ((markPrice - entryPrice) / entryPrice) * 100
			if side == "short" {
				profitPercent = -profitPercent
			}
		}

		// 方向和盈亏表情符号
		sideEmoji := "📈"
		sideText := "多头"
		if side == "short" {
			sideEmoji = "📉"
			sideText = "空头"
		}

		pnlEmoji := "🟢"
		if unRealizedProfit < 0 {
			pnlEmoji = "🔴"
		}

		// 格式化杠杆显示
		leverageText := fmt.Sprintf("%.0fx", leverage)

		message.WriteString(fmt.Sprintf(`%s <b>%s → %s (%s)</b>
持仓数量: <code>%.4f</code>
入场价格: <code>$%.4f</code>
标记价格: <code>$%.4f</code>
止损价格: <code>%s</code>
止盈价格: <code>%s</code>
未实现盈亏: <code>%s USDC (%.2f%%)</code> %s
`,
			sideEmoji, symbol, sideText, leverageText,
			positionAmt, entryPrice, markPrice,
			func() string {
				if stopLoss > 0 {
					return fmt.Sprintf("$%.4f", stopLoss)
				}
				return "未设置"
			}(),
			func() string {
				if takeProfit > 0 {
					return fmt.Sprintf("$%.4f", takeProfit)
				}
				return "未设置"
			}(),
			formatUSDCFloat(unRealizedProfit), profitPercent, pnlEmoji,
		))

		// 如果不是最后一个持仓，添加分隔线
		if i < len(positions)-1 {
			message.WriteString("─────────────────\n")
		}
	}

	// 添加汇总信息
	totalUnrealized := 0.0
	positionCount := len(positions)
	for _, pos := range positions {
		if pnl, ok := pos["unRealizedProfit"].(float64); ok {
			totalUnrealized += pnl
		}
	}

	// 计算总盈亏百分比
	var totalProfitPercent float64
	if positionCount > 0 {
		totalValue := 0.0
		for _, pos := range positions {
			if value, ok := pos["entryPrice"].(float64); ok {
				if amt, ok := pos["positionAmt"].(float64); ok {
					totalValue += value * amt
				}
			}
		}
		if totalValue > 0 {
			totalProfitPercent = (totalUnrealized / totalValue) * 100
		}
	}

	message.WriteString(fmt.Sprintf(`
📋 <b>持仓汇总</b>
总资产: <code>%.2f USDC</code>
活跃持仓: <code>%d 个</code>
总未实现盈亏: <code>%s USDC (%.2f%%)</code>`,
		totalAssets, positionCount, formatUSDCFloat(totalUnrealized), totalProfitPercent,
	))

	return message.String()
}

// ValidateAddress 验证地址格式
func (s *HyperliquidService) ValidateAddress(address string) bool {
	return strings.HasPrefix(address, "0x") && len(address) == 42
}

// GetStockAssets 获取所有HIP-3股票资产
func (s *HyperliquidService) GetStockAssets(agentKey, walletAddr string, testnet bool) (string, error) {
	// 创建 Hyperliquid 交易器
	trader, err := trader.NewHyperliquidTrader(agentKey, walletAddr, testnet)
	if err != nil {
		return "", fmt.Errorf("创建 Hyperliquid 交易器失败: %w", err)
	}

	// 获取所有资产信息
	assets, err := s.GetAllPerpMetas(trader)
	if err != nil {
		return "", fmt.Errorf("获取资产信息失败: %w", err)
	}

	// 按市场分类股票资产
	categories := categorizeStocks(assets)

	// 格式化股票信息
	return s.formatStocksMessage(categories), nil
}

// GetAllPerpMetas 获取所有永续合约元数据（使用直接API调用）
func (s *HyperliquidService) GetAllPerpMetas(trader *trader.HyperliquidTrader) ([]PerpMetaAsset, error) {
	// 添加panic恢复机制
	defer func() {
		if r := recover(); r != nil {
			log.Printf("GetAllPerpMetas panic recovered: %v", r)
		}
	}()

	// 直接调用allPerpMetas API
	assets, err := s.callAllPerpMetasAPI()
	if err != nil {
		return nil, fmt.Errorf("调用Hyperliquid API失败: %w", err)
	}

	log.Printf("🔍 调试：从直接API获取到 %d 个资产", len(assets))
	return assets, nil
}

// callAllPerpMetasAPI 直接调用Hyperliquid allPerpMetas API
func (s *HyperliquidService) callAllPerpMetasAPI() ([]PerpMetaAsset, error) {
	// 创建HTTP客户端
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	// 准备请求体
	requestBody := map[string]string{
		"type": "allPerpMetas",
	}

	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("序列化请求体失败: %w", err)
	}

	// 创建请求
	req, err := http.NewRequest("POST", "https://api.hyperliquid.xyz/info", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}

	// 设置请求头
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "NOFX-Telegram-Bot")

	// 发送请求
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("API请求失败: %w", err)
	}
	defer resp.Body.Close()

	// 读取响应
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	log.Printf("📊 allPerpMetas API响应: 状态 %d, 长度 %d 字节", resp.StatusCode, len(body))

	// 检查响应状态
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API返回错误状态码: %d, 响应: %s", resp.StatusCode, string(body))
	}

	// 解析响应 - API返回的是数组，不是对象
	var response []PerpMetaResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("解析响应JSON失败: %w, 响应: %s", err, string(body))
	}

	// 收集所有universe数组中的资产
	var allAssets []PerpMetaAsset
	colonCount := 0

	// 遍历数组中的每个对象
	for _, resp := range response {
		// 处理该universe中的所有资产
		for _, asset := range resp.Universe {
			name := asset.Name

			// 检查是否包含冒号（HIP-3资产）
			if strings.Contains(name, ":") {
				colonCount++
			}

			allAssets = append(allAssets, asset)
		}
	}

	log.Printf("📊 allPerpMetas处理完成: 总资产 %d, HIP-3资产 %d", len(allAssets), colonCount)
	return allAssets, nil
}

// formatStocksMessage 格式化股票资产信息消息
func (s *HyperliquidService) formatStocksMessage(categories []StockCategory) string {
	if len(categories) == 0 {
		return `📊 <b>HIP-3 股票资产</b>

暂无可用的HIP-3股票资产

💡 HIP-3股票资产使用冒号前缀格式（如 xyz:NVDA）`
	}

	var message strings.Builder
	message.WriteString("📊 <b>HIP-3 股票资产</b>\n\n")
	message.WriteString("请选择市场查看股票列表。\n\n")

	// 显示市场统计信息，使用按钮标题同款格式
	message.WriteString("📈 <b>市场列表</b>\n")
	totalAssets := 0
	for _, cat := range categories {
		totalAssets += len(cat.List)
		message.WriteString(fmt.Sprintf("• %s（%d）\n", cat.Name, len(cat.List)))
	}

	message.WriteString(fmt.Sprintf("\n共 %d 个市场，%d 个股票资产。\n", len(categories), totalAssets))
	message.WriteString("点击下方按钮选择市场。")

	return message.String()
}

// categorizeStocks 将HIP-3资产按前缀分组
func categorizeStocks(assets []PerpMetaAsset) []StockCategory {
	groups := make(map[string][]PerpMetaAsset)
	for _, asset := range assets {
		if !strings.Contains(asset.Name, ":") {
			continue
		}
		parts := strings.SplitN(asset.Name, ":", 2)
		prefix := parts[0]
		groups[prefix] = append(groups[prefix], asset)
	}

	var categories []StockCategory
	for name, list := range groups {
		// 按符号排序，方便阅读
		sort.Slice(list, func(i, j int) bool {
			return list[i].Name < list[j].Name
		})
		categories = append(categories, StockCategory{
			Name: name,
			List: list,
		})
	}

	// 按组内资产数量降序，数量相同再按市场名升序
	sort.Slice(categories, func(i, j int) bool {
		if len(categories[i].List) == len(categories[j].List) {
			return categories[i].Name < categories[j].Name
		}
		return len(categories[i].List) > len(categories[j].List)
	})

	return categories
}

// formatOrderHistory 格式化历史成交
func formatOrderHistory(items []OrderHistoryItem, lookback time.Duration, loc *time.Location) string {
	if loc == nil {
		loc = time.UTC
	}
	if len(items) == 0 {
		return fmt.Sprintf("📜 <b>历史成交</b>\n%s内无成交记录。", formatLookback(lookback))
	}

	// 统计
	var totalPnl, totalFee float64
	var win, lose int
	for _, it := range items {
		totalPnl += it.ClosedPnl
		totalFee += it.Fee
		if it.ClosedPnl > 0 {
			win++
		} else if it.ClosedPnl < 0 {
			lose++
		}
	}

	var b strings.Builder
	b.WriteString("📜 <b>历史成交</b>\n")
	winRate := 0.0
	if len(items) > 0 {
		winRate = float64(win) / float64(len(items)) * 100
	}
	b.WriteString(fmt.Sprintf("统计窗口: %s | 笔数: %d | 胜率: %.1f%%\n", formatLookback(lookback), len(items), winRate))
	b.WriteString(fmt.Sprintf("累计盈亏: %s USDC | 手续费: %.2f USDC\n\n", formatSigned(totalPnl), totalFee))

	maxDisplay := len(items)
	if maxDisplay > 20 {
		maxDisplay = 20
	}
	for i := 0; i < maxDisplay; i++ {
		it := items[i]
		sideEmoji := "📈"
		if strings.EqualFold(it.Side, "sell") || strings.Contains(strings.ToLower(it.Dir), "short") {
			sideEmoji = "📉"
		}
		pnlEmoji := "⚪️"
		switch {
		case it.ClosedPnl > 0:
			pnlEmoji = "🟢"
		case it.ClosedPnl < 0:
			pnlEmoji = "🔴"
		}

		ts := it.Timestamp.In(loc)
		lines := []string{
			fmt.Sprintf("#%d %s %s", i+1, sideEmoji, it.Symbol),
			fmt.Sprintf("• 数量/价格: %.4f × %.4f", it.Size, it.Price),
			fmt.Sprintf("• 方向: %s", it.Dir),
			fmt.Sprintf("• 时间: %s", ts.Format("2006-01-02 15:04:05")),
			fmt.Sprintf("• 入场: %.4f", it.Price),
		}
		if it.StopPrice != nil {
			lines = append(lines, fmt.Sprintf("• 止损: %.4f", *it.StopPrice))
		}
		lines = append(lines, fmt.Sprintf("• 盈亏: %s %s USDC | 费: %.4f", pnlEmoji, formatSigned(it.ClosedPnl), it.Fee))

		for _, ln := range lines {
			b.WriteString(ln)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	if len(items) > maxDisplay {
		b.WriteString(fmt.Sprintf("… 还有 %d 条未展示，可调整范围查看更多。", len(items)-maxDisplay))
	}
	return b.String()
}

// formatLookback 将时间窗口转为可读文本
func formatLookback(d time.Duration) string {
	hours := int(d.Hours() + 0.5)
	if hours >= 24 && hours%24 == 0 {
		days := hours / 24
		return fmt.Sprintf("近 %d 天", days)
	}
	return fmt.Sprintf("近 %d 小时", hours)
}

// formatSigned 为数值添加符号
func formatSigned(v float64) string {
	if v > 0 {
		return fmt.Sprintf("+%.2f", v)
	}
	return fmt.Sprintf("%.2f", v)
}

// formatUSDCFloat 动态保留小数，避免小额被四舍五入为 0
func formatUSDCFloat(v float64) string {
	abs := math.Abs(v)
	switch {
	case abs >= 1:
		return fmt.Sprintf("%.2f", v)
	case abs >= 0.01:
		return fmt.Sprintf("%.4f", v)
	case abs >= 0.0001:
		return fmt.Sprintf("%.6f", v)
	default:
		return fmt.Sprintf("%.8f", v)
	}
}

// formatMaybePrice 将价格格式化为 4 位小数，空值返回破折号
func formatMaybePrice(v float64) string {
	if v <= 0 {
		return "—"
	}
	return fmt.Sprintf("%.4f", v)
}

func parseFloatSafe(s string) float64 {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return f
}

// absFloat 返回浮点数的绝对值
func absFloat(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
