package trader

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/sonirico/go-hyperliquid"
)

type perpMetaResponse struct {
	Universe []struct {
		Name string `json:"name"`
	} `json:"universe"`
}

// 轻量化的 PerpMeta/Asset 结构用于解析 allPerpMetas
type PerpMetaLite struct {
	Universe []PerpMetaAssetLite `json:"universe"`
}

type PerpMetaAssetLite struct {
	Name         string `json:"name"`
	SzDecimals   int    `json:"szDecimals"`
	PxDecimals   *int   `json:"pxDecimals"`
	MarginMode   string `json:"marginMode,omitempty"`
	OnlyIsolated bool   `json:"onlyIsolated,omitempty"`
}

// normalizeHip3Symbol 确保HIP-3符号前缀小写、后缀大写（如 xyz:TSLA）
func normalizeHip3Symbol(symbol string) string {
	if !strings.Contains(symbol, ":") {
		return symbol
	}
	parts := strings.SplitN(symbol, ":", 2)
	prefix := strings.ToLower(strings.TrimSpace(parts[0]))
	suffix := strings.ToUpper(strings.TrimSpace(parts[1]))
	return prefix + ":" + suffix
}

// dexLabel 为空字符串时返回 "main" 方便日志
func dexLabel(dex string) string {
	if dex == "" {
		return "main"
	}
	return dex
}

func shortHexAddr(addr string) string {
	a := strings.TrimSpace(addr)
	if len(a) <= 12 {
		return a
	}
	if strings.HasPrefix(a, "0x") && len(a) >= 10 {
		return a[:6] + "..." + a[len(a)-4:]
	}
	return a[:4] + "..." + a[len(a)-4:]
}

// infoAPIURL 根据网络返回 Info API 地址
func infoAPIURL(testnet bool) string {
	if testnet {
		return "https://api.hyperliquid-testnet.xyz/info"
	}
	return "https://api.hyperliquid.xyz/info"
}

// fetchMetaForDex 获取指定 dex 的 meta（返回 collateralToken）
func fetchMetaForDex(testnet bool, dex string) (map[string]interface{}, error) {
	payload := map[string]interface{}{
		"type": "meta",
	}
	if dex != "" {
		payload["dex"] = dex
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST", infoAPIURL(testnet), bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("meta(dex=%s) status %d: %s", dexLabel(dex), resp.StatusCode, string(respBody))
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(respBody, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// fetchSpotMeta 拉取 spotMeta
func fetchSpotMeta(testnet bool) (*hyperliquid.SpotMeta, error) {
	payload := []byte(`{"type":"spotMeta"}`)
	req, err := http.NewRequest("POST", infoAPIURL(testnet), bytes.NewBuffer(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("spotMeta status %d: %s", resp.StatusCode, string(body))
	}
	var sm hyperliquid.SpotMeta
	if err := json.Unmarshal(body, &sm); err != nil {
		return nil, err
	}
	return &sm, nil
}

// getPriceFromWSCached 尝试从 WS allMids 缓存获取价格，返回值及命中标记
func (t *HyperliquidTrader) getPriceFromWSCached(coin string, dex string) (float64, bool) {
	ws := getWSManager(t.testnet)
	if ws == nil {
		return 0, false
	}
	mids, ok := ws.getAllMids(1500*time.Millisecond, dex)
	if !ok {
		return 0, false
	}
	coin = normalizeHip3Symbol(coin)
	if px, exists := mids[coin]; exists && px > 0 {
		return px, true
	}
	return 0, false
}

// fetchPriceFromInfoAPI 调用 Info API allMids 获取价格（用于AllMids缺失时兜底）
func (t *HyperliquidTrader) fetchPriceFromInfoAPI(coin string) (float64, error) {
	coin = normalizeHip3Symbol(coin)
	payload := []byte(`{"type":"allMids"}`)
	req, err := http.NewRequest("POST", infoAPIURL(t.testnet), bytes.NewBuffer(payload))
	if err != nil {
		return 0, fmt.Errorf("创建 allMids 请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "NOFX-Hyperliquid-Price")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("调用 allMids 失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("读取 allMids 响应失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("allMids 返回状态码 %d: %s", resp.StatusCode, string(body))
	}

	var mids map[string]string
	if err := json.Unmarshal(body, &mids); err != nil {
		return 0, fmt.Errorf("解析 allMids 响应失败: %w", err)
	}

	if priceStr, ok := mids[coin]; ok {
		price, err := strconv.ParseFloat(priceStr, 64)
		if err != nil {
			return 0, fmt.Errorf("价格格式错误: %v", err)
		}
		return price, nil
	}

	return 0, fmt.Errorf("allMids 未找到价格: %s", coin)
}

// getMidPrice 优先使用 WS allMids 缓存，失败再调用 HTTP allMids
func (t *HyperliquidTrader) getMidPrice(coin string) (float64, error) {
	// 默认使用 perp dex（空字符串）；如后续需要可传入特定 dex
	if px, ok := t.getPriceFromWSCached(coin, ""); ok {
		return px, nil
	}
	return t.fetchPriceFromInfoAPI(coin)
}

// getAllMidsMap 优先使用 WS 缓存，失败再调用 HTTP 接口
func (t *HyperliquidTrader) getAllMidsMap() (map[string]string, error) {
	ws := getWSManager(t.testnet)
	if ws != nil {
		if mids, ok := ws.getAllMids(1500*time.Millisecond, ""); ok && len(mids) > 0 {
			res := make(map[string]string, len(mids))
			for k, v := range mids {
				res[k] = fmt.Sprintf("%f", v)
			}
			return res, nil
		}
	}
	return t.exchange.Info().AllMids(t.ctx)
}

// fetchPriceFromRecentTrades 调用 Info API recentTrades 获取最新成交价（用于HIP-3等特殊资产）
func (t *HyperliquidTrader) fetchPriceFromRecentTrades(coin string) (float64, error) {
	// 优先使用 WS trades，避免额外 HTTP
	if ws := getWSManager(t.testnet); ws != nil {
		if px, ok := ws.getLastTradePrice(2*time.Second, normalizeHip3Symbol(coin)); ok {
			return px, nil
		}
	}

	coin = normalizeHip3Symbol(coin)
	payload := []byte(fmt.Sprintf(`{"type":"recentTrades","coin":%q}`, coin))
	req, err := http.NewRequest("POST", infoAPIURL(t.testnet), bytes.NewBuffer(payload))
	if err != nil {
		return 0, fmt.Errorf("创建 recentTrades 请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "NOFX-Hyperliquid-RecentTrades")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("调用 recentTrades 失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("读取 recentTrades 响应失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("recentTrades 返回状态码 %d: %s", resp.StatusCode, string(body))
	}

	// 响应为数组，元素字段可能是 px 或 price
	var trades []map[string]interface{}
	if err := json.Unmarshal(body, &trades); err != nil {
		return 0, fmt.Errorf("解析 recentTrades 响应失败: %w", err)
	}
	if len(trades) == 0 {
		return 0, fmt.Errorf("recentTrades 返回空结果")
	}

	// 尝试解析 px 或 price
	parsePrice := func(v interface{}) (float64, bool) {
		switch val := v.(type) {
		case string:
			if f, err := strconv.ParseFloat(val, 64); err == nil {
				return f, true
			}
		case float64:
			return val, true
		}
		return 0, false
	}

	if f, ok := parsePrice(trades[0]["px"]); ok {
		return f, nil
	}
	if f, ok := parsePrice(trades[0]["price"]); ok {
		return f, nil
	}

	// 部分节点返回 pricePx
	if f, ok := parsePrice(trades[0]["pricePx"]); ok {
		return f, nil
	}

	return 0, fmt.Errorf("recentTrades 未找到价格字段")
}

// fetchHip3PriceFromAssetCtx 通过 metaAndAssetCtxs 获取 HIP-3 资产的 mark/mid/oracle 价格
func (t *HyperliquidTrader) fetchHip3PriceFromAssetCtx(coin string) (float64, error) {
	coin = normalizeHip3Symbol(coin)
	payload := []byte(`{"type":"metaAndAssetCtxs"}`)
	req, err := http.NewRequest("POST", infoAPIURL(t.testnet), bytes.NewBuffer(payload))
	if err != nil {
		return 0, fmt.Errorf("创建 metaAndAssetCtxs 请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "NOFX-Hyperliquid-AssetCtx")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("调用 metaAndAssetCtxs 失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("读取 metaAndAssetCtxs 响应失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("metaAndAssetCtxs 返回状态码 %d: %s", resp.StatusCode, string(body))
	}

	// 新格式：数组 [meta, assetCtxs]
	var arr []json.RawMessage
	if err := json.Unmarshal(body, &arr); err != nil {
		return 0, fmt.Errorf("解析 metaAndAssetCtxs 失败: %w", err)
	}
	if len(arr) != 2 {
		return 0, fmt.Errorf("metaAndAssetCtxs 返回意外结构，len=%d", len(arr))
	}

	var meta struct {
		Universe []PerpMetaAssetLite `json:"universe"`
	}
	if err := json.Unmarshal(arr[0], &meta); err != nil {
		return 0, fmt.Errorf("解析 metaAndAssetCtxs meta 失败: %w", err)
	}

	var assetCtxs []map[string]json.RawMessage
	if err := json.Unmarshal(arr[1], &assetCtxs); err != nil {
		return 0, fmt.Errorf("解析 metaAndAssetCtxs assetCtxs 失败: %w", err)
	}

	for idx, asset := range meta.Universe {
		if normalizeHip3Symbol(asset.Name) != coin {
			continue
		}
		if idx >= len(assetCtxs) {
			return 0, fmt.Errorf("assetCtxs 缺少 %s 的上下文", coin)
		}
		ctxMap := assetCtxs[idx]
		// 优先 midPx -> markPx -> oraclePx
		parsePrice := func(key string) (float64, bool) {
			raw, ok := ctxMap[key]
			if !ok || len(raw) == 0 || string(raw) == "null" {
				return 0, false
			}
			var s string
			if err := json.Unmarshal(raw, &s); err == nil {
				if f, err := strconv.ParseFloat(s, 64); err == nil {
					return f, true
				}
			}
			var f float64
			if err := json.Unmarshal(raw, &f); err == nil && f != 0 {
				return f, true
			}
			return 0, false
		}
		if v, ok := parsePrice("midPx"); ok {
			return v, nil
		}
		if v, ok := parsePrice("markPx"); ok {
			return v, nil
		}
		if v, ok := parsePrice("oraclePx"); ok {
			return v, nil
		}
		return 0, fmt.Errorf("未找到 %s 的 mid/mark/oracle 价格", coin)
	}

	return 0, fmt.Errorf("metaAndAssetCtxs 未找到交易对: %s", coin)
}

// fetchPerpMetaAsset 通过 allPerpMetas 获取资产名和精度（支持主网/测试网切换）
// 使用轻量结构体避免依赖 SDK 内部类型
func (t *HyperliquidTrader) fetchPerpMetaAsset(coin string, forceMainnet bool) (string, *PerpMetaAssetLite, error) {
	coin = normalizeHip3Symbol(coin)
	metas, err := t.fetchAllPerpMetas(forceMainnet)
	if err != nil {
		return "", nil, err
	}

	log.Printf("🔍 [HIP-3 API] 处理 allPerpMetas 响应，共 %d 个 meta 块", len(metas))
	for _, meta := range metas {
		log.Printf("🔍 [HIP-3 API] 处理 meta 块，包含 %d 个资产", len(meta.Universe))
		for _, asset := range meta.Universe {
			name := normalizeHip3Symbol(asset.Name)
			if !strings.Contains(name, ":") {
				continue
			}
			parts := strings.SplitN(name, ":", 2)
			if len(parts) != 2 {
				continue
			}
			if strings.EqualFold(parts[1], coin) {
				// 详细日志记录 API 返回的两个精度值
				if asset.PxDecimals != nil {
					log.Printf("🔍 [HIP-3 API] %s - SzDecimals: %d, PxDecimals: %d",
						name, asset.SzDecimals, *asset.PxDecimals)
				} else {
					log.Printf("🔍 [HIP-3 API] %s - SzDecimals: %d, PxDecimals: nil (将使用SzDecimals作为价格精度)",
						name, asset.SzDecimals)
				}
				t.hip3Meta[name] = asset
				return name, &asset, nil
			}
		}
	}

	return "", nil, fmt.Errorf("InfoAPI 未找到交易对: %s", coin)
}

// ResolveNonCryptoSymbol 使用 Info API (allPerpMetas) 为非加密资产获取带前缀的HIP-3符号
// preferMainnet: 强制使用主网 Info API（与 /stocks 列表一致）
func (t *HyperliquidTrader) ResolveNonCryptoSymbol(symbol string, preferMainnet bool) (string, error) {
	base := convertSymbolToHyperliquid(strings.ToUpper(symbol))
	// 直传冒号格式则直接返回
	if strings.Contains(base, ":") {
		norm := normalizeHip3Symbol(base)
		return norm, nil
	}

	mapped, asset, err := t.fetchPerpMetaAsset(base, preferMainnet)
	if err != nil {
		return "", err
	}
	if asset != nil {
		t.hip3Meta[normalizeHip3Symbol(mapped)] = *asset
	}
	return normalizeHip3Symbol(mapped), nil
}

// resolveFromInfoAPI 使用 Info API 的 allPerpMetas 兜底匹配 HIP-3 股票（与 /stocks 列表一致）
// forceMainnet: 为非加密资产匹配时可强制使用主网 Info API（与 /stocks 保持一致）
func (t *HyperliquidTrader) resolveFromInfoAPI(coin string, forceMainnet bool) (string, error) {
	mapped, _, err := t.fetchPerpMetaAsset(coin, forceMainnet)
	return mapped, err
}

// fetchAllPerpMetas 带重试获取 allPerpMetas
func (t *HyperliquidTrader) fetchAllPerpMetas(forceMainnet bool) ([]PerpMetaLite, error) {
	payload := []byte(`{"type":"allPerpMetas"}`)
	endpoint := infoAPIURL(t.testnet)
	if forceMainnet {
		endpoint = infoAPIURL(false)
	}

	var lastErr error
	backoff := 500 * time.Millisecond
	for i := 0; i < 4; i++ {
		req, err := http.NewRequest("POST", endpoint, bytes.NewBuffer(payload))
		if err != nil {
			return nil, fmt.Errorf("创建 InfoAPI 请求失败: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "NOFX-Hyperliquid-Resolve")

		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("调用 InfoAPI 失败: %w", err)
			time.Sleep(backoff)
			backoff *= 2
			continue
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("读取 InfoAPI 响应失败: %w", err)
			time.Sleep(backoff)
			backoff *= 2
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("InfoAPI 返回错误状态码 %d: %s", resp.StatusCode, string(body))
			time.Sleep(backoff)
			backoff *= 2
			continue
		}
		var metas []PerpMetaLite
		if err := json.Unmarshal(body, &metas); err != nil {
			lastErr = fmt.Errorf("解析 InfoAPI 响应失败: %w", err)
			time.Sleep(backoff)
			backoff *= 2
			continue
		}
		return metas, nil
	}
	return nil, lastErr
}

// GetRecentTradePrice 使用 recentTrades 接口获取最新成交价（适用于 HIP-3 股票等非加密资产）
func (t *HyperliquidTrader) GetRecentTradePrice(coin string) (float64, error) {
	return t.fetchPriceFromRecentTrades(coin)
}

// GetUserFillsByTime 获取指定时间范围内的成交记录
func (t *HyperliquidTrader) GetUserFillsByTime(start time.Time, end *time.Time) ([]hyperliquid.Fill, error) {
	startMs := start.UnixMilli()
	var endMs *int64
	if end != nil {
		v := end.UnixMilli()
		endMs = &v
	}
	return t.exchange.Info().UserFillsByTime(t.ctx, t.walletAddr, startMs, endMs, nil)
}

// HyperliquidTrader Hyperliquid交易器
type HyperliquidTrader struct {
	exchange         *hyperliquid.Exchange
	ctx              context.Context
	walletAddr       string
	meta             *hyperliquid.Meta // 缓存meta信息（包含精度等）
	spotMeta         *hyperliquid.SpotMeta
	assetMap         map[string]int // HIP-3 名称 -> assetId (补充SDK缺失的股票映射)
	testnet          bool           // 当前是否为测试网
	hip3Meta         map[string]PerpMetaAssetLite
	isCrossMargin    bool // 是否为全仓模式
	orderMu          sync.Mutex
	stopLossOrders   map[string]orderRef // symbol -> 最近一次止损挂单
	takeProfitOrders map[string]orderRef // symbol -> 最近一次止盈挂单

	wsManager *hyperliquidWSManager // WS 管理器（缓存 allMids / openOrders）

	agentPrivateKey *ecdsa.PrivateKey // Agent 签名私钥，用于自定义 action
	apiBaseURL      string            // Exchange 基础地址
	abstractionOnce sync.Once         // 只尝试一次开启 DEX 抽象

	// 抵押资产缓存：dex -> collateral token index；token index -> token 元信息
	dexCollateral    map[string]int
	collateralTokens map[int]hyperliquid.SpotTokenInfo

	// 自动抵押兑换参数
	autoCollateralSwap bool
	maxSwapSlippage    float64 // 价格滑点上限（比例）
	minFillRatio       float64 // 市价单最小成交比例
}

const hyenaBuilderAddress = "0x1924b8561eeF20e70Ede628A296175D358BE80e5"

// Builder功能暂时禁用：未使用API Wallet时会导致授权失败
func hyenaBuilderInfo() *hyperliquid.BuilderInfo {
	return nil
}

// CheckBuilderApproval 检查是否已授权 Builder
func (t *HyperliquidTrader) CheckBuilderApproval() (bool, error) {
	payload := map[string]interface{}{
		"type": "clearinghouseState",
		"user": t.walletAddr,
	}
	jsonBody, err := json.Marshal(payload)
	if err != nil {
		return false, fmt.Errorf("序列化请求失败: %w", err)
	}

	req, err := http.NewRequest("POST", infoAPIURL(t.testnet), bytes.NewBuffer(jsonBody))
	if err != nil {
		return false, fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, fmt.Errorf("读取响应失败: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return false, fmt.Errorf("解析响应失败: %w", err)
	}

	// 检查 builderFeeApprovals 数组
	approvals, ok := result["builderFeeApprovals"].([]interface{})
	if !ok {
		return false, nil
	}

	targetAddr := strings.ToLower(hyenaBuilderAddress)
	for _, approval := range approvals {
		approvalMap, ok := approval.(map[string]interface{})
		if !ok {
			continue
		}
		builder, ok := approvalMap["builder"].(string)
		if ok && strings.ToLower(builder) == targetAddr {
			return true, nil
		}
	}
	return false, nil
}

// ApproveBuilder 授权 Builder（一次性操作）
func (t *HyperliquidTrader) ApproveBuilder() error {
	log.Printf("🔐 正在授权 Builder: %s", hyenaBuilderAddress)

	// 使用 SDK 的 ApproveBuilderFee 方法
	// 参数: builder地址, maxFeeRate (bps, "1" = 0.01%)
	_, err := t.exchange.ApproveBuilderFee(t.ctx, hyenaBuilderAddress, "1")
	if err != nil {
		return fmt.Errorf("授权 Builder 失败: %w", err)
	}

	log.Printf("✅ Builder 授权成功: %s", hyenaBuilderAddress)
	return nil
}

// EnsureBuilderApproved 确保 Builder 已授权（检查+授权）
func (t *HyperliquidTrader) EnsureBuilderApproved() error {
	approved, err := t.CheckBuilderApproval()
	if err != nil {
		log.Printf("⚠️ 检查 Builder 授权状态失败: %v，尝试直接授权...", err)
	}

	if approved {
		log.Printf("✅ Builder 已授权: %s", hyenaBuilderAddress)
		return nil
	}

	log.Printf("📝 Builder 未授权，正在进行一次性授权...")
	return t.ApproveBuilder()
}

type orderRef struct {
	oid   int64
	cloid string
}

// NewHyperliquidTrader 创建Hyperliquid交易器
func NewHyperliquidTrader(privateKeyHex string, walletAddr string, testnet bool) (*HyperliquidTrader, error) {
	// 去掉私钥的 0x 前缀（如果有，不区分大小写）
	privateKeyHex = strings.TrimPrefix(strings.ToLower(privateKeyHex), "0x")

	// 解析私钥
	privateKey, err := crypto.HexToECDSA(privateKeyHex)
	if err != nil {
		return nil, fmt.Errorf("解析私钥失败: %w", err)
	}

	// 选择API URL
	apiURL := hyperliquid.MainnetAPIURL
	if testnet {
		apiURL = hyperliquid.TestnetAPIURL
	}

	// Security enhancement: Implement Agent Wallet best practices
	// Reference: https://hyperliquid.gitbook.io/hyperliquid-docs/for-developers/api/nonces-and-api-wallets
	agentAddr := crypto.PubkeyToAddress(*privateKey.Public().(*ecdsa.PublicKey)).Hex()

	if walletAddr == "" {
		return nil, fmt.Errorf("❌ Configuration error: Main wallet address (hyperliquid_wallet_addr) not provided\n" +
			"🔐 Correct configuration pattern:\n" +
			"  1. hyperliquid_private_key = Agent Private Key (for signing only, balance should be ~0)\n" +
			"  2. hyperliquid_wallet_addr = Main Wallet Address (holds funds, never expose private key)\n" +
			"💡 Please create an Agent Wallet on Hyperliquid official website and authorize it before configuration:\n" +
			"   https://app.hyperliquid.xyz/ → Settings → API Wallets")
	}

	// Check if user accidentally uses main wallet private key (security risk)
	if strings.EqualFold(walletAddr, agentAddr) {
		log.Printf("⚠️⚠️⚠️ WARNING: Main wallet address (%s) matches Agent wallet address!", walletAddr)
		log.Printf("   This indicates you may be using your main wallet private key, which poses extremely high security risks!")
		log.Printf("   Recommendation: Immediately create a separate Agent Wallet on Hyperliquid official website")
		log.Printf("   Reference: https://hyperliquid.gitbook.io/hyperliquid-docs/for-developers/api/nonces-and-api-wallets")
	} else {
		log.Printf("✓ Using Agent Wallet mode (secure)")
		log.Printf("  └─ Agent wallet address: %s (for signing)", agentAddr)
		log.Printf("  └─ Main wallet address: %s (holds funds)", walletAddr)
	}

	ctx := context.Background()

	// ✅ 初始化 Info 元数据（仅主 dex），全局复用，避免并发初始化时反复拉取导致 429
	bootstrap, err := getHyperliquidBootstrapInfo(ctx, testnet)
	if err != nil {
		return nil, fmt.Errorf("获取 Hyperliquid meta/spotMeta 失败: %w", err)
	}

	// 创建Exchange客户端（Exchange包含Info功能），传入预取的 meta/spotMeta，避免 SDK 内部自动拉取失败导致 panic
	exchange, err := safeNewHyperliquidExchange(ctx, privateKey, apiURL, walletAddr, bootstrap.meta, bootstrap.spotMeta)
	if err != nil {
		return nil, err
	}

	log.Printf("✓ Hyperliquid交易器初始化成功 (testnet=%v, wallet=%s)", testnet, walletAddr)

	meta := bootstrap.meta
	dexCollateral := bootstrap.dexCollateral
	tokenByIndex := bootstrap.tokenByIndex
	spotMeta := bootstrap.spotMeta

	// 🔍 Security check: Validate Agent wallet balance (should be close to 0)
	// Only check if using separate Agent wallet (not when main wallet is used as agent)
	if !strings.EqualFold(walletAddr, agentAddr) {
		agentState, err := exchange.Info().UserState(ctx, agentAddr)
		if err == nil && agentState != nil && agentState.CrossMarginSummary.AccountValue != "" {
			// Parse Agent wallet balance
			agentBalance, _ := strconv.ParseFloat(agentState.CrossMarginSummary.AccountValue, 64)

			if agentBalance > 100 {
				// Critical: Agent wallet holds too much funds
				log.Printf("🚨🚨🚨 CRITICAL SECURITY WARNING 🚨🚨🚨")
				log.Printf("   Agent wallet balance: %.2f USDC (exceeds safe threshold of 100 USDC)", agentBalance)
				log.Printf("   Agent wallet address: %s", agentAddr)
				log.Printf("   ⚠️  Agent wallets should only be used for signing and hold minimal/zero balance")
				log.Printf("   ⚠️  High balance in Agent wallet poses security risks")
				log.Printf("   📖 Reference: https://hyperliquid.gitbook.io/hyperliquid-docs/for-developers/api/nonces-and-api-wallets")
				log.Printf("   💡 Recommendation: Transfer funds to main wallet and keep Agent wallet balance near 0")
				return nil, fmt.Errorf("security check failed: Agent wallet balance too high (%.2f USDC), exceeds 100 USDC threshold", agentBalance)
			} else if agentBalance > 10 {
				// Warning: Agent wallet has some balance (acceptable but not ideal)
				log.Printf("⚠️  Notice: Agent wallet address (%s) has some balance: %.2f USDC", agentAddr, agentBalance)
				log.Printf("   While not critical, it's recommended to keep Agent wallet balance near 0 for security")
			} else {
				// OK: Agent wallet balance is safe
				log.Printf("✓ Agent wallet balance is safe: %.2f USDC (near zero as recommended)", agentBalance)
			}
		} else if err != nil {
			// Failed to query agent balance - log warning but don't block initialization
			log.Printf("⚠️  Could not verify Agent wallet balance (query failed): %v", err)
			log.Printf("   Proceeding with initialization, but please manually verify Agent wallet balance is near 0")
		}
	}

	// 创建 trader 实例
	trader := &HyperliquidTrader{
		exchange:           exchange,
		ctx:                ctx,
		walletAddr:         walletAddr,
		wsManager:          getWSManager(testnet),
		meta:               meta,
		spotMeta:           spotMeta,
		assetMap:           make(map[string]int),
		hip3Meta:           make(map[string]PerpMetaAssetLite),
		testnet:            testnet,
		isCrossMargin:      true, // 默认使用全仓模式
		stopLossOrders:     make(map[string]orderRef),
		takeProfitOrders:   make(map[string]orderRef),
		agentPrivateKey:    privateKey,
		apiBaseURL:         apiURL,
		dexCollateral:      dexCollateral,
		collateralTokens:   tokenByIndex,
		autoCollateralSwap: true,
		maxSwapSlippage:    0.005, // 0.5%
		minFillRatio:       0.95,
	}

	// 🔐 自动检查并授权 Builder（一次性）
	// Builder 功能暂时禁用（主钱包私钥不可用于 API 授权）

	return trader, nil
}

// safeNewHyperliquidExchange 包装 hyperliquid.NewExchange，防止内部panic导致进程崩溃
func safeNewHyperliquidExchange(ctx context.Context, privateKey *ecdsa.PrivateKey, apiURL string, walletAddr string, meta *hyperliquid.Meta, spotMeta *hyperliquid.SpotMeta) (ex *hyperliquid.Exchange, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("初始化 Hyperliquid Exchange 失败（panic）: %v", r)
		}
	}()

	ex = hyperliquid.NewExchange(
		ctx,
		privateKey,
		apiURL,
		meta,
		"",         // vault address (empty for personal account)
		walletAddr, // wallet address
		spotMeta,
	)
	return ex, nil
}

// GetBalance 获取账户余额
func (t *HyperliquidTrader) GetBalance() (map[string]interface{}, error) {
	prefix := fmt.Sprintf("[HL wallet=%s]", shortHexAddr(t.walletAddr))
	logf := func(format string, args ...interface{}) {
		log.Printf(prefix+" "+format, args...)
	}

	logf("🔄 正在调用Hyperliquid API获取账户余额...")

	const (
		wsTTL  = 15 * time.Second // 优先使用 WS 数据，避免启动瞬间/多账号并发时触发 HTTP 429
		wsWait = 5 * time.Second  // 启动后等待 WS 首帧再回退 HTTP
	)

	// ✅ Step 1: 查询 Spot 现货账户余额
	var spotUSDCBalance float64 = 0.0
	var spotUSDCHold float64 = 0.0
	spotFromWS := false
	if ws := getWSManager(t.testnet); ws != nil {
		if total, hold, ok := ws.getSpotUSDCWithHold(wsTTL, t.walletAddr); ok {
			spotUSDCBalance = total
			spotUSDCHold = hold
			logf("✅ 使用 WS webData2 现货余额: %.2f USDC (≤ %.0fs)", spotUSDCBalance, wsTTL.Seconds())
			spotFromWS = true
		} else if ws.waitForSpotState(t.walletAddr, wsWait) {
			if total, hold, ok := ws.getSpotUSDCWithHold(wsTTL, t.walletAddr); ok {
				spotUSDCBalance = total
				spotUSDCHold = hold
				logf("✅ 使用 WS webData2 现货余额(等待≤%.0fs): %.2f USDC", wsWait.Seconds(), spotUSDCBalance)
				spotFromWS = true
			}
		}
	}
	if !spotFromWS {
		if err := hyperliquidInfoLimiter.Wait(t.ctx); err != nil {
			logf("⚠️ 等待 Hyperliquid info 限流失败(spot): %v", err)
		}
		spotState, err := t.exchange.Info().SpotUserState(t.ctx, t.walletAddr)
		if err != nil {
			logf("⚠️ 查询 Spot 余额失败（可能无现货资产）: %v", err)
		} else if spotState != nil && len(spotState.Balances) > 0 {
			for _, balance := range spotState.Balances {
				if balance.Coin == "USDC" {
					spotUSDCBalance, _ = strconv.ParseFloat(balance.Total, 64)
					spotUSDCHold, _ = strconv.ParseFloat(balance.Hold, 64)
					logf("✓ 发现 Spot 现货余额: %.2f USDC", spotUSDCBalance)
					break
				}
			}
		}
	}
	spotUSDCTransferable := spotUSDCBalance - spotUSDCHold
	if spotUSDCTransferable < 0 {
		spotUSDCTransferable = 0
	}

	// ✅ Step 2: 优先使用 WS allDexsClearinghouseState 缓存（dex=""），过期再回退 HTTP
	var (
		accountValue       float64
		totalMarginUsed    float64
		totalNtlPos        float64
		totalUnrealizedPnl float64
		availableBalance   float64
		summary            interface{}
		summaryType        string
	)

	if ws := getWSManager(t.testnet); ws != nil {
		if state, ok := ws.getPerpClearinghouseState(wsTTL, t.walletAddr, ""); ok && state.MarginSummary != nil {
			logf("✅ 使用 WS 缓存的 clearinghouseState (dex=\"\", <= %.0fs)", wsTTL.Seconds())
			accountValue, _ = strconv.ParseFloat(state.MarginSummary.AccountValue, 64)
			totalMarginUsed, _ = strconv.ParseFloat(state.MarginSummary.TotalMarginUsed, 64)
			totalNtlPos, _ = strconv.ParseFloat(state.MarginSummary.TotalNtlPos, 64)
			for _, assetPos := range state.AssetPositions {
				unrealizedPnl, _ := strconv.ParseFloat(assetPos.Position.UnrealizedPnl, 64)
				totalUnrealizedPnl += unrealizedPnl
			}
			availableBalance, _ = strconv.ParseFloat(state.Withdrawable, 64)
			summaryType = "WS MarginSummary"
			summary = state.MarginSummary
		}

		if summary == nil && ws.waitForPerpState(t.walletAddr, wsWait) {
			if state, ok := ws.getPerpClearinghouseState(wsTTL, t.walletAddr, ""); ok && state.MarginSummary != nil {
				logf("✅ 使用 WS 缓存的 clearinghouseState (等待≤%.0fs)", wsWait.Seconds())
				accountValue, _ = strconv.ParseFloat(state.MarginSummary.AccountValue, 64)
				totalMarginUsed, _ = strconv.ParseFloat(state.MarginSummary.TotalMarginUsed, 64)
				totalNtlPos, _ = strconv.ParseFloat(state.MarginSummary.TotalNtlPos, 64)
				for _, assetPos := range state.AssetPositions {
					unrealizedPnl, _ := strconv.ParseFloat(assetPos.Position.UnrealizedPnl, 64)
					totalUnrealizedPnl += unrealizedPnl
				}
				availableBalance, _ = strconv.ParseFloat(state.Withdrawable, 64)
				summaryType = "WS MarginSummary"
				summary = state.MarginSummary
			}
		}
	}

	// 回退 HTTP
	if summary == nil {
		if err := hyperliquidInfoLimiter.Wait(t.ctx); err != nil {
			logf("⚠️ 等待 Hyperliquid info 限流失败(perp): %v", err)
		}
		accountState, err := t.exchange.Info().UserState(t.ctx, t.walletAddr)
		if err != nil {
			// 429/网络波动时，尽量使用更宽松 TTL 的 WS 缓存兜底，避免启动阶段直接失败。
			if ws := getWSManager(t.testnet); ws != nil {
				const staleTTL = 2 * time.Minute
				if state, ok := ws.getPerpClearinghouseState(staleTTL, t.walletAddr, ""); ok && state.MarginSummary != nil {
					logf("⚠️ HTTP UserState 失败，使用 WS 兜底缓存 (≤ %.0fs): %v", staleTTL.Seconds(), err)
					accountValue, _ = strconv.ParseFloat(state.MarginSummary.AccountValue, 64)
					totalMarginUsed, _ = strconv.ParseFloat(state.MarginSummary.TotalMarginUsed, 64)
					totalNtlPos, _ = strconv.ParseFloat(state.MarginSummary.TotalNtlPos, 64)
					for _, assetPos := range state.AssetPositions {
						unrealizedPnl, _ := strconv.ParseFloat(assetPos.Position.UnrealizedPnl, 64)
						totalUnrealizedPnl += unrealizedPnl
					}
					availableBalance, _ = strconv.ParseFloat(state.Withdrawable, 64)
					summaryType = "WS MarginSummary (stale)"
					summary = state.MarginSummary
				}
			}
			if summary == nil {
				logf("❌ Hyperliquid Perpetuals API调用失败: %v", err)
				return nil, fmt.Errorf("获取账户信息失败: %w", err)
			}
		}
		if summary == nil {
			accountValue, _ = strconv.ParseFloat(accountState.MarginSummary.AccountValue, 64)
			totalMarginUsed, _ = strconv.ParseFloat(accountState.MarginSummary.TotalMarginUsed, 64)
			totalNtlPos, _ = strconv.ParseFloat(accountState.MarginSummary.TotalNtlPos, 64)
			for _, assetPos := range accountState.AssetPositions {
				unrealizedPnl, _ := strconv.ParseFloat(assetPos.Position.UnrealizedPnl, 64)
				totalUnrealizedPnl += unrealizedPnl
			}
			if accountState.Withdrawable != "" {
				availableBalance, _ = strconv.ParseFloat(accountState.Withdrawable, 64)
			}
			summaryType = "HTTP MarginSummary"
			summary = accountState.MarginSummary
		}
	}

	// 解析余额信息（MarginSummary字段都是string）
	result := make(map[string]interface{})

	// ✅ Step 3: 总资产使用 MarginSummary 的 accountValue（包含占用保证金）

	// 🔍 调试：打印API返回的完整摘要结构
	summaryJSON, _ := json.MarshalIndent(summary, "  ", "  ")
	logf("🔍 [DEBUG] Hyperliquid API %s 完整数据:", summaryType)
	logf("%s", string(summaryJSON))

	// ✅ 正确理解Hyperliquid字段：
	// AccountValue = 总账户净值（已包含空闲资金+持仓价值+未实现盈亏）
	// TotalMarginUsed = 持仓占用的保证金（已包含在AccountValue中，仅用于显示）

	// ✅ Step 4: 可用余额直接使用 Withdrawable 字段
	logf("✓ 使用 Withdrawable 字段: %.2f USDC", availableBalance)

	// ✅ Step 5: 计算总资产
	// Hyperliquid 的 AccountValue 仅覆盖 Perpetuals 账户，不包含现货余额，因此需要与 Spot 余额相加
	totalWalletBalance := accountValue + spotUSDCBalance

	result["totalWalletBalance"] = totalWalletBalance    // 总资产（使用 accountValue）
	result["availableBalance"] = availableBalance        // 可用余额（Withdrawable 字段）
	result["totalUnrealizedProfit"] = totalUnrealizedPnl // 未实现盈亏（仅来自 Perpetuals）
	result["spotBalance"] = spotUSDCBalance              // Spot 现货余额（单独返回）
	result["spotHold"] = spotUSDCHold                    // Spot 现货占用（挂单/冻结）
	result["spotTransferable"] = spotUSDCTransferable    // Spot 可划转余额
	result["totalMarginUsed"] = totalMarginUsed          // 占用保证金
	result["totalPosition"] = totalNtlPos                // 总持仓名义价值

	// 增强的调试日志：显示完整的余额字段映射
	logf("🔍 [DEBUG] Hyperliquid 余额字段详情 (JavaScript 方式):")
	logf("  • AccountValue (总资产): %.2f USDC", accountValue)
	logf("  • Withdrawable (可提现): %.2f USDC", availableBalance)
	logf("  • TotalMarginUsed (占用保证金): %.2f USDC", totalMarginUsed)
	logf("  • SpotUSDCBalance (现货余额): %.2f USDC", spotUSDCBalance)
	logf("  • SpotUSDCHold (现货占用): %.2f USDC", spotUSDCHold)
	logf("  • SpotUSDCTransferable (可划转): %.2f USDC", spotUSDCTransferable)
	logf("  • TotalUnrealizedPnL (未实现盈亏): %.2f USDC", totalUnrealizedPnl)
	logf("  • TotalNtlPos (总持仓): %.2f USDC", totalNtlPos)
	logf("")
	logf("✅ JavaScript 计算方式:")
	logf("  • 总资产 = AccountValue = %.2f USDC", totalWalletBalance)
	logf("  • 现货余额单独展示: %.2f USDC", spotUSDCBalance)
	logf("")
	logf("💰 账户总览:")
	logf("  • 总资产 (AccountValue): %.2f USDC", totalWalletBalance)
	logf("  • 可用余额 (Withdrawable): %.2f USDC", availableBalance)
	logf("  • 现货余额 (Spot): %.2f USDC", spotUSDCBalance)
	logf("  • 未实现盈亏: %.2f USDC", totalUnrealizedPnl)
	logf("  ⭐ 与 Hyperliquid 官网对比: 总资产 %.2f USDC", totalWalletBalance)

	return result, nil
}

// TransferSpotToPerp 将 USDC 从现货账户划转到合约账户
func (t *HyperliquidTrader) TransferSpotToPerp(amount float64) error {
	if amount <= 0 {
		return nil
	}
	prefix := fmt.Sprintf("[HL wallet=%s]", shortHexAddr(t.walletAddr))
	logf := func(format string, args ...interface{}) {
		log.Printf(prefix+" "+format, args...)
	}

	logf("🔄 正在将 %.4f USDC 从 Spot 划转到 Perp...", amount)
	res, err := t.exchange.UsdClassTransfer(t.ctx, amount, true)
	if err != nil {
		return fmt.Errorf("Spot->Perp 划转失败: %w", err)
	}
	if res == nil {
		return fmt.Errorf("Spot->Perp 划转失败: empty response")
	}
	if strings.ToLower(strings.TrimSpace(res.Status)) != "ok" {
		msg := strings.TrimSpace(res.Error)
		if msg == "" {
			msg = strings.TrimSpace(res.Response)
		}
		if msg != "" {
			return fmt.Errorf("Spot->Perp 划转失败: %s", msg)
		}
		return fmt.Errorf("Spot->Perp 划转失败: status=%s", res.Status)
	}
	if res.TxHash != "" {
		logf("✅ Spot->Perp 划转完成: %.4f USDC (tx=%s)", amount, res.TxHash)
	} else {
		logf("✅ Spot->Perp 划转完成: %.4f USDC", amount)
	}
	return nil
}

// fetchUserStateWithDex 调用 clearinghouseState，支持 dex 参数以获取不同 perp 市场（含 HIP-3）
func (t *HyperliquidTrader) fetchUserStateWithDex(dex string) (*hyperliquid.UserState, error) {
	payload := map[string]interface{}{
		"type": "clearinghouseState",
		"user": t.walletAddr,
	}
	if dex != "" {
		payload["dex"] = dex
	}

	reqBody, _ := json.Marshal(payload)

	if err := hyperliquidInfoLimiter.Wait(t.ctx); err != nil {
		return nil, fmt.Errorf("等待 Hyperliquid info 限流失败(dex=%s): %w", dex, err)
	}

	req, err := http.NewRequestWithContext(t.ctx, "POST", infoAPIURL(t.testnet), bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "NOFX-Hyperliquid-Positions")

	resp, err := hyperliquidInfoHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求失败(dex=%s): %w", dex, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败(dex=%s): %w", dex, err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("clearinghouseState 返回状态码 %d (dex=%s): %s", resp.StatusCode, dex, string(body))
	}

	var state hyperliquid.UserState
	if err := json.Unmarshal(body, &state); err != nil {
		return nil, fmt.Errorf("解析响应失败(dex=%s): %w", dex, err)
	}
	return &state, nil
}

// GetPositions 获取所有持仓
func (t *HyperliquidTrader) GetPositions() ([]map[string]interface{}, error) {
	// 优先使用 WS allDexsClearinghouseState 缓存拿持仓，避免启动/多账号并发时触发 HTTP 429。
	const (
		wsTTL  = 15 * time.Second
		wsWait = 5 * time.Second
	)

	var allPositions []hyperliquid.AssetPosition
	usedWS := false
	if ws := getWSManager(t.testnet); ws != nil {
		if states, ok := ws.getAllPerpClearinghouseStates(wsTTL, t.walletAddr); ok {
			usedWS = true
			for dex, st := range states {
				if len(st.AssetPositions) > 0 {
					log.Printf("🔍 [DEBUG] (WS) dex=%s 返回 %d 个资产持仓", dex, len(st.AssetPositions))
				}
				allPositions = append(allPositions, st.AssetPositions...)
			}
		} else if ws.waitForPerpState(t.walletAddr, wsWait) {
			if states, ok := ws.getAllPerpClearinghouseStates(wsTTL, t.walletAddr); ok {
				usedWS = true
				for dex, st := range states {
					if len(st.AssetPositions) > 0 {
						log.Printf("🔍 [DEBUG] (WS) dex=%s 返回 %d 个资产持仓", dex, len(st.AssetPositions))
					}
					allPositions = append(allPositions, st.AssetPositions...)
				}
			}
		}
	}

	// WS 未就绪才回退 HTTP（仅主 dex，避免 HIP-3 dex 扇出导致 429）
	if !usedWS {
		dexes := []string{""}
		for _, dex := range dexes {
			state, err := t.fetchUserStateWithDex(dex)
			if err != nil {
				log.Printf("⚠️ 获取持仓失败(dex=%s): %v", dex, err)
				continue
			}
			if len(state.AssetPositions) > 0 {
				log.Printf("🔍 [DEBUG] dex=%s 返回 %d 个资产持仓", dex, len(state.AssetPositions))
			}
			allPositions = append(allPositions, state.AssetPositions...)
		}
	}

	if len(allPositions) == 0 {
		log.Printf("🔍 [DEBUG] 未获取到任何持仓（WS=%t）", usedWS)
		return []map[string]interface{}{}, nil
	}

	// 预先获取触发类挂单，用于止盈/止损信息
	const ordersTTL = 3 * time.Second
	frontendOrders, err := t.getFrontendOpenOrdersCached(ordersTTL)
	if err != nil {
		log.Printf("⚠️ 获取前端挂单失败，止盈止损信息将缺失: %v", err)
		frontendOrders = nil
	}

	// 额外获取普通挂单，用于兜底（部分 reduce-only 限价单没有触发标记）
	openOrders, err := t.getOpenOrdersCached(ordersTTL)
	if err != nil {
		log.Printf("⚠️ 获取 OpenOrders 失败，无法兜底识别 reduce-only 限价单: %v", err)
		openOrders = nil
	}

	// 记录已被识别为 TP/SL 的订单，避免 fallback 再次把同一触发单误判为另一种类型
	classifiedOrderKinds := make(map[int64]string)

	var result []map[string]interface{}

	// 遍历所有持仓
	for i, assetPos := range allPositions {
		position := assetPos.Position

		// 记录原始数据用于调试
		log.Printf("🔍 [DEBUG] 资产持仓 %d: Coin=%s, Szi=%s, PositionValue=%s",
			i, position.Coin, position.Szi, position.PositionValue)

		// 增强的持仓数量解析
		posAmt, err := t.parsePositionSzi(position.Szi)
		if err != nil {
			log.Printf("❌ 跳过无效持仓 %d: %v", i, err)
			continue
		}

		if posAmt == 0 {
			log.Printf("⚪ 跳过零持仓 %s", position.Coin)
			continue
		}

		log.Printf("✅ 发现有效持仓 %s: 数量=%.6f", position.Coin, posAmt)

		posMap := make(map[string]interface{})

		// 标准化symbol格式（Hyperliquid使用如"BTC"，我们转换为"BTCUSDT"）
		symbol := convertSymbolFromHyperliquid(position.Coin)
		posMap["symbol"] = symbol

		// 持仓数量和方向 - 保留原始符号用于平仓判断
		if posAmt > 0 {
			posMap["side"] = "long"
		} else {
			posMap["side"] = "short"
		}
		posMap["quantity"] = posAmt // 保留原始符号（空头为负数，多头为正数）

		// 为了向后兼容，同时保留positionAmt字段（绝对值）
		posMap["positionAmt"] = math.Abs(posAmt)

		// 同时保留 Szi 原始值用于调试
		posMap["Szi"] = position.Szi

		// 价格信息（EntryPx和LiquidationPx是指针类型）
		var entryPrice, liquidationPx float64
		if position.EntryPx != nil {
			entryPrice, _ = strconv.ParseFloat(*position.EntryPx, 64)
		}
		if position.LiquidationPx != nil {
			liquidationPx, _ = strconv.ParseFloat(*position.LiquidationPx, 64)
		}

		positionValue, _ := strconv.ParseFloat(position.PositionValue, 64)
		unrealizedPnl, _ := strconv.ParseFloat(position.UnrealizedPnl, 64)

		// 计算mark price（positionValue / abs(posAmt)）
		var markPrice float64
		if posAmt != 0 {
			markPrice = positionValue / absFloat(posAmt)
		}

		posMap["entryPrice"] = entryPrice
		posMap["markPrice"] = markPrice
		posMap["unRealizedProfit"] = unrealizedPnl
		posMap["leverage"] = float64(position.Leverage.Value)
		posMap["liquidationPrice"] = liquidationPx

		// 匹配止盈/止损触发单（使用 Hyperliquid 前端字段判断类型）
		var stopLossPx, takeProfitPx float64
		positionSide := "LONG"
		if posAmt < 0 {
			positionSide = "SHORT"
		}
		for _, ord := range frontendOrders {
			if !strings.EqualFold(ord.Coin, position.Coin) {
				continue
			}
			// 只看触发类或显式标记为TP/SL的委托
			if !(ord.IsTrigger || ord.IsPositionTpSl) {
				continue
			}
			triggerPx := ord.TriggerPx
			if triggerPx <= 0 {
				continue
			}

			orderKind := detectOrderKind(ord, positionSide)

			// 新增兜底：部分触发单（尤其是 reduce-only）可能没有TP/SL标记
			if orderKind == "" && triggerPx > 0 && (ord.ReduceOnly || ord.IsTrigger || ord.IsPositionTpSl) {
				log.Printf("📋 订单 OID=%d 无法直接识别，使用价格启发式 (OrderType='%s', IsPositionTpSl=%t, TriggerCondition='%s', ReduceOnly=%t)",
					ord.Oid, ord.OrderType, ord.IsPositionTpSl, ord.TriggerCondition, ord.ReduceOnly)
				if kind := t.classifyOrderByPriceHeuristic(ord, positionSide, triggerPx); kind != "" {
					orderKind = kind
					log.Printf("✅ 订单 OID=%d 启发式分类成功: %s", ord.Oid, orderKind)
				}
			}

			if orderKind == "" {
				log.Printf("📋 跳过订单 OID=%d: 无法确定订单类型 (OrderType='%s', IsPositionTpSl=%t, TriggerCondition='%s', ReduceOnly=%t)",
					ord.Oid, ord.OrderType, ord.IsPositionTpSl, ord.TriggerCondition, ord.ReduceOnly)
				continue
			}

			// 对于无法确定类型的订单，使用启发式方法判断
			if orderKind == "unknown" {
				log.Printf("📋 订单 OID=%d 需要启发式判断 (OrderType='%s', TriggerCondition='%s')",
					ord.Oid, ord.OrderType, ord.TriggerCondition)
				if kind := t.classifyOrderByPriceHeuristic(ord, positionSide, triggerPx); kind != "" {
					orderKind = kind
					log.Printf("✅ 订单 OID=%d 启发式判断成功: %s", ord.Oid, orderKind)
				} else {
					log.Printf("❌ 订单 OID=%d 启发式判断失败，跳过", ord.Oid)
					continue // 启发式判断也失败，跳过
				}
			} else {
				log.Printf("📋 订单 OID=%d 直接识别: %s (OrderType='%s')", ord.Oid, orderKind, ord.OrderType)
			}

			if positionSide == "LONG" {
				if orderKind == "sl" {
					// price向下触发，取最靠近当前价格的最高触发价
					if stopLossPx == 0 || triggerPx > stopLossPx {
						stopLossPx = triggerPx
					}
				} else {
					// take profit 向上触发，取最靠近当前价格的最低触发价
					if takeProfitPx == 0 || triggerPx < takeProfitPx {
						takeProfitPx = triggerPx
					}
				}
			} else { // SHORT
				if orderKind == "sl" {
					// price向上触发，取最靠近当前价格的最低触发价
					if stopLossPx == 0 || triggerPx < stopLossPx {
						stopLossPx = triggerPx
					}
				} else {
					// take profit 向下触发，取最靠近当前价格的最高触发价
					if takeProfitPx == 0 || triggerPx > takeProfitPx {
						takeProfitPx = triggerPx
					}
				}
			}

			// 记录该订单已被明确分类，避免 fallback 重复使用
			classifiedOrderKinds[ord.Oid] = orderKind
		}
		if stopLossPx > 0 {
			posMap["stopLoss"] = stopLossPx
		}
		if takeProfitPx > 0 {
			posMap["takeProfit"] = takeProfitPx
		}

		// 🔄 兜底：如果仍未识别出止盈/止损，再尝试根据 reduce-only 限价挂单推断
		if (stopLossPx == 0 || takeProfitPx == 0) && len(openOrders) > 0 {
			refPx := entryPrice
			if refPx == 0 {
				refPx = markPrice
			}

			bestSL := stopLossPx
			bestTP := takeProfitPx

			for _, ord := range openOrders {
				if !strings.EqualFold(ord.Coin, position.Coin) {
					continue
				}

				// 避免把已经识别为 TP/SL 的触发单再次用来兜底
				if _, exists := classifiedOrderKinds[ord.Oid]; exists {
					continue
				}

				side := strings.ToUpper(ord.Side)

				// 判断是否为减少仓位方向的单子（无 reduceOnly 字段时的近似判断）
				isClosing := (posAmt > 0 && side == "A") || (posAmt < 0 && side == "B")
				if !isClosing {
					continue
				}

				px := ord.LimitPx
				if px <= 0 {
					continue
				}

				if posAmt > 0 { // LONG: 卖出平仓
					if px < refPx { // 更低价格 -> 更像止损，取最接近参考价的最高价
						if bestSL == 0 || px > bestSL {
							bestSL = px
						}
					} else if px > refPx { // 更高价格 -> 更像止盈，取最接近的最低价
						if bestTP == 0 || px < bestTP {
							bestTP = px
						}
					}
				} else { // SHORT: 买入平仓
					if px > refPx { // 更高价格 -> 止损，取最接近的最低价
						if bestSL == 0 || px < bestSL {
							bestSL = px
						}
					} else if px < refPx { // 更低价格 -> 止盈，取最接近的最高价
						if bestTP == 0 || px > bestTP {
							bestTP = px
						}
					}
				}
			}

			if stopLossPx == 0 && bestSL > 0 {
				stopLossPx = bestSL
				posMap["stopLoss"] = stopLossPx
				log.Printf("✅ %s 通过限价挂单兜底识别到止损价: %.4f", symbol, stopLossPx)
			}
			if takeProfitPx == 0 && bestTP > 0 {
				takeProfitPx = bestTP
				posMap["takeProfit"] = takeProfitPx
				log.Printf("✅ %s 通过限价挂单兜底识别到止盈价: %.4f", symbol, takeProfitPx)
			}
		}

		result = append(result, posMap)
	}

	return result, nil
}

// SetMarginMode 设置仓位模式 (在SetLeverage时一并设置)
func (t *HyperliquidTrader) SetMarginMode(symbol string, isCrossMargin bool) error {
	// Hyperliquid的仓位模式在SetLeverage时设置，这里只记录
	t.isCrossMargin = isCrossMargin
	marginModeStr := "全仓"
	if !isCrossMargin {
		marginModeStr = "逐仓"
	}
	log.Printf("  ✓ %s 将使用 %s 模式", symbol, marginModeStr)
	return nil
}

// SetLeverage 设置杠杆
func (t *HyperliquidTrader) SetLeverage(symbol string, leverage int) error {
	coin, err := t.resolveCoin(symbol, "")
	if err != nil {
		return err
	}

	// 自动尝试开启 DEX 抽象（仅对 HIP-3 资产），失败不阻塞下单
	if t.isStockAsset(coin) {
		t.enableDexAbstractionOnce()
	}

	isCross := t.isCrossMargin
	if t.isStockAsset(coin) && t.requiresIsolated(coin) {
		isCross = false
		log.Printf("⚙️ %s 要求逐仓模式，自动切换为逐仓", coin)
	}

	// 调用UpdateLeverage (leverage int, name string, isCross bool)
	// 第三个参数: true=全仓模式, false=逐仓模式
	_, err = t.exchange.UpdateLeverage(t.ctx, leverage, coin, isCross)
	if err != nil {
		return fmt.Errorf("设置杠杆失败: %w", err)
	}

	log.Printf("  ✓ %s 杠杆已切换为 %dx (isCross=%t)", symbol, leverage, isCross)
	return nil
}

// loadCollateralInfo 拉取各 dex 的抵押资产映射及 spot token 信息
func loadCollateralInfo(testnet bool) (map[string]int, map[int]hyperliquid.SpotTokenInfo, *hyperliquid.SpotMeta, error) {
	// 仅加载主 dex，减少启动阶段 Info API 调用（非主 dex 暂不支持）
	dexes := []string{""}
	dexCollateral := make(map[string]int)

	// fetch spot meta once
	spotMeta, err := fetchSpotMeta(testnet)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("获取 spotMeta 失败: %w", err)
	}
	tokenByIndex := make(map[int]hyperliquid.SpotTokenInfo)
	for _, t := range spotMeta.Tokens {
		tokenByIndex[t.Index] = t
	}

	for _, dex := range dexes {
		raw, err := fetchMetaForDex(testnet, dex)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("获取 meta(dex=%s) 失败: %w", dexLabel(dex), err)
		}
		val, ok := raw["collateralToken"]
		if !ok {
			return nil, nil, nil, fmt.Errorf("meta(dex=%s) 缺少 collateralToken", dexLabel(dex))
		}
		var idx int
		switch v := val.(type) {
		case float64:
			idx = int(v)
		case int:
			idx = v
		default:
			return nil, nil, nil, fmt.Errorf("meta(dex=%s) collateralToken 类型未知: %T", dexLabel(dex), val)
		}
		dexCollateral[dex] = idx

		// 打印日志
		tokenName := "unknown"
		szDec := 0
		if t, ok := tokenByIndex[idx]; ok {
			tokenName = t.Name
			szDec = t.SzDecimals
		}
		log.Printf("💱 DEX=%s collateralToken index=%d name=%s szDecimals=%d", dexLabel(dex), idx, tokenName, szDec)
	}

	return dexCollateral, tokenByIndex, spotMeta, nil
}

// dexForCoin 返回 HIP-3 前缀，普通 perp 为空字符串
func dexForCoin(coin string) string {
	if strings.Contains(coin, ":") {
		parts := strings.SplitN(coin, ":", 2)
		return strings.ToLower(parts[0])
	}
	return ""
}

// spotBalanceByToken 返回指定 tokenIndex 的 total 余额
func (t *HyperliquidTrader) spotBalanceByToken(tokenIndex int) (float64, error) {
	state, err := t.exchange.Info().SpotUserState(t.ctx, t.walletAddr)
	if err != nil {
		return 0, err
	}
	for _, b := range state.Balances {
		if b.Token == tokenIndex {
			f, _ := strconv.ParseFloat(b.Total, 64)
			return f, nil
		}
	}
	return 0, nil
}

// findCollateralPair 查找 collateral/USDC 现货交易对及 midPx
func (t *HyperliquidTrader) findCollateralPair(collIdx int) (assetName string, assetIndex int, mid float64, err error) {
	if t.spotMeta == nil {
		return "", 0, 0, fmt.Errorf("spotMeta 未初始化")
	}
	var target *hyperliquid.SpotAssetInfo
	for i := range t.spotMeta.Universe {
		u := t.spotMeta.Universe[i]
		if len(u.Tokens) != 2 {
			continue
		}
		if u.Tokens[0] == collIdx && u.Tokens[1] == 0 { // base=collateral, quote=USDC(index 0)
			target = &u
			assetIndex = u.Index + 10000 // spot asset offset
			assetName = u.Name
			break
		}
	}
	if target == nil {
		return "", 0, 0, fmt.Errorf("未找到 collateral/USDC 现货对 (token=%d)", collIdx)
	}

	// 获取 midPx via SpotMetaAndAssetCtxs
	spotCtx, err := t.exchange.Info().SpotMetaAndAssetCtxs(t.ctx)
	if err != nil {
		return "", 0, 0, fmt.Errorf("获取 spotMetaAndAssetCtxs 失败: %w", err)
	}
	if target.Index >= len(spotCtx.Ctxs) {
		return "", 0, 0, fmt.Errorf("spot ctx 缺少 index=%d", target.Index)
	}
	ctx := spotCtx.Ctxs[target.Index]
	if ctx.MidPx == nil || *ctx.MidPx == "" {
		return "", 0, 0, fmt.Errorf("spot midPx 缺失 (pair=%s)", target.Name)
	}
	mid, err = strconv.ParseFloat(*ctx.MidPx, 64)
	if err != nil {
		return "", 0, 0, fmt.Errorf("解析 midPx 失败: %w", err)
	}
	return
}

// ensureCollateralAvailable 如抵押资产不足则尝试用 USDC 兑换
func (t *HyperliquidTrader) ensureCollateralAvailable(dex string, neededUsd float64) error {
	if !t.autoCollateralSwap {
		return nil
	}
	collIdx, ok := t.dexCollateral[dex]
	if !ok {
		return fmt.Errorf("未知 dex 抵押资产: %s", dexLabel(dex))
	}
	if collIdx == 0 {
		return nil // USDC 直接返回
	}

	collToken, ok := t.collateralTokens[collIdx]
	if !ok {
		return fmt.Errorf("缺少 collateral token 元信息: idx=%d", collIdx)
	}

	collBal, err := t.spotBalanceByToken(collIdx)
	if err != nil {
		return fmt.Errorf("查询抵押余额失败: %w", err)
	}
	if collBal >= neededUsd {
		return nil
	}

	missing := neededUsd - collBal

	// 查找对 USDC 的现货对
	pairName, assetIndex, mid, err := t.findCollateralPair(collIdx)
	if err != nil {
		return err
	}

	// 稳定币价格保护
	if mid < 0.8 || mid > 1.2 {
		return fmt.Errorf("抵押币 %s midPx=%.4f 超出安全范围，请手动充值", collToken.Name, mid)
	}

	// 需要的 base 数量与 USDC
	needBase := missing * 1.002 // 加一点余量

	// 按 collateral token 的数量精度截断，避免 size 无效
	dec := collToken.SzDecimals
	mult := math.Pow10(dec)
	needBase = math.Floor(needBase*mult) / mult
	if needBase <= 0 {
		needBase = 1 / mult // 至少一个最小单位
	}

	needUsdc := needBase * mid * (1 + t.maxSwapSlippage)

	usdcBal, err := t.spotBalanceByToken(0)
	if err != nil {
		return fmt.Errorf("查询USDC余额失败: %w", err)
	}

	// 若 spot USDC 不足，尝试从 perp withdrawable 转回 spot
	if usdcBal < needUsdc {
		required := needUsdc - usdcBal
		state, err := t.exchange.Info().UserState(t.ctx, t.walletAddr)
		if err != nil {
			return fmt.Errorf("查询可提余额失败: %w", err)
		}
		withdrawable, _ := strconv.ParseFloat(state.Withdrawable, 64)
		if withdrawable < required {
			return fmt.Errorf("USDC余额不足以兑换抵押资产，需要 %.4f USDC，当前 %.4f (含可提 %.4f)", needUsdc, usdcBal, withdrawable)
		}
		// 转 perp -> spot
		transferAmt := required * 1.01 // 多转一点防止精度
		if transferAmt > withdrawable {
			transferAmt = required
		}
		log.Printf("💱 perp->spot USDC 转账以补足兑换: %.4f", transferAmt)
		if _, err := t.exchange.UsdClassTransfer(t.ctx, transferAmt, false); err != nil {
			return fmt.Errorf("perp 转 spot 失败: %w", err)
		}
		// 重新查询 spot USDC
		usdcBal, err = t.spotBalanceByToken(0)
		if err != nil {
			return fmt.Errorf("转账后查询USDC余额失败: %w", err)
		}
		if usdcBal < needUsdc {
			return fmt.Errorf("转账后 USDC 仍不足兑换抵押资产，需要 %.4f USDC，当前 %.4f", needUsdc, usdcBal)
		}
	}

	// 下 IOC 现货买单
	log.Printf("💱 自动兑换抵押资产: dex=%s collateral=%s needBase=%.6f mid=%.6f limit=%.6f usdcCost<=%.4f",
		dexLabel(dex), collToken.Name, needBase, mid, mid*(1+t.maxSwapSlippage), needUsdc)

	filled, err := t.placeSpotMarketBuy(pairName, assetIndex, needBase, mid*(1+t.maxSwapSlippage), collToken.SzDecimals)
	if err != nil {
		return fmt.Errorf("抵押兑换下单失败: %w", err)
	}
	if filled < needBase*t.minFillRatio {
		return fmt.Errorf("抵押兑换成交不足 (filled %.6f / %.6f)", filled, needBase)
	}

	return nil
}

// placeSpotMarketBuy 使用 IOC 限价模拟市价买入 base（支付 USDC）
func (t *HyperliquidTrader) placeSpotMarketBuy(pairName string, assetIndex int, sizeBase float64, limitPx float64, baseSzDecimals int) (float64, error) {
	// 避免 float_to_wire 精度报错：数量截断到 base sz，价格按 tick 截断
	sizeBase = roundToDecimalsLocal(sizeBase, baseSzDecimals)
	tickDecimals := 6 - baseSzDecimals
	if tickDecimals < 0 {
		tickDecimals = 0
	}
	limitPx = roundToDecimalsLocal(limitPx, tickDecimals)

	order := hyperliquid.CreateOrderRequest{
		Coin:       pairName,
		IsBuy:      true,
		Size:       sizeBase,
		Price:      limitPx,
		ReduceOnly: false,
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifIoc},
		},
	}

	res, err := t.exchange.Order(t.ctx, order, nil)
	if err != nil {
		return 0, err
	}

	if res.Filled != nil {
		filledSz, _ := strconv.ParseFloat(res.Filled.TotalSz, 64)
		return filledSz, nil
	}
	if res.Resting != nil {
		return 0, fmt.Errorf("现货IOC订单未完全成交 (resting)")
	}
	return 0, fmt.Errorf("现货订单未知状态")
}

// enableDexAbstractionOnce 尝试开启 HIP-3 DEX 抽象模式（agent 签名），失败不阻塞
func (t *HyperliquidTrader) enableDexAbstractionOnce() {
	t.abstractionOnce.Do(func() {
		if t.agentPrivateKey == nil || t.apiBaseURL == "" {
			log.Printf("⚠️ 跳过开启 DEX 抽象：缺少 agent 私钥或 API 地址")
			return
		}

		action := struct {
			Type string `json:"type" msgpack:"type"`
		}{
			Type: "agentEnableDexAbstraction",
		}

		var expiresAfterMs int64 = int64(10 * time.Minute / time.Millisecond)
		nonce := time.Now().UnixMilli()
		sig, err := hyperliquid.SignL1Action(
			t.agentPrivateKey,
			action,
			"", // vault address 为空
			nonce,
			&expiresAfterMs, // expiresAfter
			!t.testnet,      // isMainnet
		)
		if err != nil {
			log.Printf("⚠️ 开启 DEX 抽象签名失败: %v", err)
			return
		}

		body := map[string]interface{}{
			"action":    action,
			"signature": sig,
			"nonce":     nonce,
		}
		payload, _ := json.Marshal(body)

		req, err := http.NewRequest("POST", t.apiBaseURL+"/exchange", bytes.NewBuffer(payload))
		if err != nil {
			log.Printf("⚠️ 创建 DEX 抽象请求失败: %v", err)
			return
		}
		req.Header.Set("Content-Type", "application/json")

		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			log.Printf("⚠️ DEX 抽象请求失败: %v", err)
			return
		}
		defer resp.Body.Close()

		respBody, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			log.Printf("⚠️ DEX 抽象开启失败，状态码=%d，响应=%s", resp.StatusCode, string(respBody))
			return
		}

		log.Printf("✅ 已尝试开启 DEX 抽象模式，响应: %s", strings.TrimSpace(string(respBody)))
	})
}

// OpenLong 开多仓
func (t *HyperliquidTrader) OpenLong(symbol string, quantity float64, leverage int) (map[string]interface{}, error) {
	// 📋 [订单执行流程] 开多仓流程开始
	log.Printf("🚀 [订单执行流程] ===== 开多仓执行流程开始 =====")
	log.Printf("🚀 [订单执行流程] 输入参数:")
	log.Printf("🚀 [订单执行流程] symbol: %s", symbol)
	log.Printf("🚀 [订单执行流程] quantity: %.8f", quantity)
	log.Printf("🚀 [订单执行流程] leverage: %d", leverage)
	log.Printf("🚀 [订单执行流程] 开始时间: %s", time.Now().Format("2006-01-02 15:04:05.000"))

	// 先取消该币种的所有委托单
	log.Printf("📋 [订单执行流程] 步骤1: 取消旧委托单")
	if err := t.CancelAllOrders(symbol); err != nil {
		log.Printf("  ⚠ 取消旧委托单失败: %v", err)
	} else {
		log.Printf("✅ [订单执行流程] 步骤1完成: 旧委托单已取消")
	}

	// 设置杠杆
	log.Printf("📋 [订单执行流程] 步骤2: 设置杠杆")
	if err := t.SetLeverage(symbol, leverage); err != nil {
		log.Printf("❌ [订单执行流程] 步骤2失败: 杠杆设置失败: %v", err)
		return nil, err
	} else {
		log.Printf("✅ [订单执行流程] 步骤2完成: 杠杆已设置为 %dx", leverage)
	}

	// Hyperliquid symbol格式
	log.Printf("📋 [订单执行流程] 步骤3: 解析币种符号")
	coin, err := t.resolveCoin(symbol, "")
	if err != nil {
		log.Printf("❌ [订单执行流程] 步骤3失败: 币种解析失败: %v", err)
		return nil, err
	} else {
		log.Printf("✅ [订单执行流程] 步骤3完成: 币种符号已解析为 %s", coin)
	}

	// 获取当前价格（用于市价单）
	log.Printf("📋 [订单执行流程] 步骤4: 获取市场价格")
	price, err := t.GetMarketPrice(symbol)
	if err != nil {
		log.Printf("❌ [订单执行流程] 步骤4失败: 获取市场价格失败: %v", err)
		return nil, err
	} else {
		log.Printf("✅ [订单执行流程] 步骤4完成: 市场价格获取成功 %.8f", price)
	}

	// 获取API精度信息
	szDecimals := t.getSzDecimals(coin)
	maxPriceDecimals, hasPxDecimals := t.getPxDecimals(coin)

	// ⚠️ 关键：根据币种精度要求，四舍五入数量
	roundedQuantity := t.roundToSzDecimals(coin, quantity)
	log.Printf("  📏 数量精度处理: %.8f -> %.8f (szDecimals=%d)", quantity, roundedQuantity, szDecimals)

	// ⚠️ 关键：价格精度处理（5位有效数字 + maxDecimals = 6 - szDecimals）
	var aggressivePrice float64
	var priceMultiplier float64

	// 根据资产类型调整激进定价策略
	if t.isStockAsset(coin) {
		priceMultiplier = 1.01 // 与加密资产保持一致，避免偏差过大
		log.Printf("🎯 [HIP-3] 股票资产使用标准定价策略: 1.01倍")
	} else {
		priceMultiplier = 1.01 // 加密货币使用原策略
		log.Printf("📈 [HIP-3] 加密货币使用标准定价策略: 1.01倍")
	}

	slippage := math.Abs(priceMultiplier - 1.0)
	aggressivePrice = t.iocLimitPriceForCoin(coin, true, price, slippage)
	log.Printf("  💰 价格精度处理: %.8f * %.3f -> %.8f -> %.8f", price, priceMultiplier, price*priceMultiplier, aggressivePrice)

	// 价格预验证
	validatedPrice, err := t.validateOrderPrice(coin, aggressivePrice, true)
	if err != nil {
		return nil, fmt.Errorf("价格验证失败: %w", err)
	}

	// 📋 [订单参数完整打印] 开多仓订单构建参数详细记录
	log.Printf("🔍 [完整订单参数] ===== 开多仓订单参数详情 =====")
	log.Printf("🔍 [完整订单参数] 基础信息:")
	log.Printf("  symbol: %s", symbol)
	log.Printf("  coin: %s", coin)
	log.Printf("  side: BUY")
	log.Printf("  orderType: LIMIT")
	log.Printf("  timeInForce: IOC")
	log.Printf("")
	log.Printf("🔍 [完整订单参数] 价格信息:")
	log.Printf("  marketPrice: %.8f", price)
	log.Printf("  priceMultiplier: %.3f", priceMultiplier)
	log.Printf("  calculatedPrice: %.8f", price*priceMultiplier)
	log.Printf("  aggressivePrice: %.8f", aggressivePrice)
	log.Printf("  validatedPrice: %.8f", validatedPrice)
	log.Printf("")
	log.Printf("🔍 [完整订单参数] 数量信息:")
	log.Printf("  originalQuantity: %.8f", quantity)
	log.Printf("  roundedQuantity: %.8f", roundedQuantity)
	log.Printf("")
	log.Printf("🔍 [完整订单参数] 精度信息:")
	log.Printf("  szDecimals: %d", szDecimals)
	log.Printf("  maxPriceDecimals: %d", maxPriceDecimals)
	log.Printf("  hasPxDecimals(meta): %t", hasPxDecimals)
	log.Printf("")
	log.Printf("🔍 [完整订单参数] 资产信息:")
	log.Printf("  isStockAsset: %t", t.isStockAsset(coin))
	log.Printf("  leverage: %d", leverage)
	log.Printf("")

	// 保证抵押资产充足（可能自动用 USDC 兑换）
	positionValue := roundedQuantity * validatedPrice
	neededMargin := positionValue / float64(leverage)
	if err := t.ensureCollateralAvailable(dexForCoin(coin), neededMargin); err != nil {
		return nil, fmt.Errorf("抵押资产检查失败: %w", err)
	}

	log.Printf("🔍 [完整订单参数] 账户信息: (信息通过外部API获取)")
	log.Printf("🔍 [完整订单参数] ================================")

	// 创建市价买入订单（使用IOC limit order with aggressive price）
	order := hyperliquid.CreateOrderRequest{
		Coin:  coin,
		IsBuy: true,
		Size:  roundedQuantity, // 使用四舍五入后的数量
		Price: validatedPrice,  // 使用验证后的价格（对股票会进行截断）
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{
				Tif: hyperliquid.TifIoc, // Immediate or Cancel (类似市价单)
			},
		},
		ReduceOnly: false,
	}

	// 📋 [订单结构完整打印] 最终订单结构详情
	log.Printf("🔍 [订单结构] ===== 最终订单结构详情 =====")
	log.Printf("🔍 [订单结构] order.Coin: %s", order.Coin)
	log.Printf("🔍 [订单结构] order.IsBuy: %t", order.IsBuy)
	log.Printf("🔍 [订单结构] order.Size: %.8f", order.Size)
	log.Printf("🔍 [订单结构] order.Price: %.8f", order.Price)
	log.Printf("🔍 [订单结构] order.OrderType.Limit.Tif: %s", string(order.OrderType.Limit.Tif))
	log.Printf("🔍 [订单结构] order.ReduceOnly: %t", order.ReduceOnly)
	log.Printf("🔍 [订单结构] ================================")

	// 📋 [API调用] 记录API调用开始时间
	apiCallStart := time.Now()
	log.Printf("🚀 [API调用] 开始执行订单API调用...")
	log.Printf("🚀 [API调用] 调用时间: %s", apiCallStart.Format("2006-01-02 15:04:05.000"))

	// ✅ 单次执行下单，失败直接返回，便于定位问题
	_, err = t.exchange.Order(t.ctx, order, nil) // builder 暂停

	// 📋 [API调用] 记录API调用结束时间和结果
	apiCallEnd := time.Now()
	apiCallDuration := apiCallEnd.Sub(apiCallStart)
	log.Printf("🏁 [API调用] API调用完成")
	log.Printf("🏁 [API调用] 结束时间: %s", apiCallEnd.Format("2006-01-02 15:04:05.000"))
	log.Printf("🏁 [API调用] 耗时: %v", apiCallDuration)
	log.Printf("🏁 [API调用] 结果: %s", map[bool]string{true: "成功", false: "失败"}[err == nil])

	if err != nil {
		// 额外打印参考价对比，便于排查 “reference price” 相关错误
		if refPrice, refErr := t.GetMarketPrice(symbol); refErr == nil && refPrice > 0 {
			deviation := (order.Price - refPrice) / refPrice * 100
			log.Printf("📊 [价格对比] 订单价格=%.8f, 参考价格=%.8f, 偏差=%.2f%%", order.Price, refPrice, deviation)
		} else if refErr != nil {
			log.Printf("⚠️ [价格对比] 获取参考价格失败: %v", refErr)
		}

		log.Printf("❌ [API调用] 失败原因: %v", err)
		log.Printf("❌ [订单执行流程] 最终步骤失败: API调用失败")
		log.Printf("🚀 [订单执行流程] ===== 开多仓执行流程失败 =====")
		return nil, fmt.Errorf("开多仓失败: %w", err)
	}

	log.Printf("✓ 开多仓成功: %s 数量: %.4f", symbol, roundedQuantity)

	// 📋 [订单执行流程] 开多仓流程成功完成
	log.Printf("🏁 [订单执行流程] 步骤6: 订单执行成功")
	log.Printf("🏁 [订单执行流程] 成交信息:")
	log.Printf("🏁 [订单执行流程] symbol: %s", symbol)
	log.Printf("🏁 [订单执行流程] quantity: %.4f", roundedQuantity)
	log.Printf("🏁 [订单执行流程] 成交价格: %.8f", order.Price)
	log.Printf("🏁 [订单执行流程] 结束时间: %s", time.Now().Format("2006-01-02 15:04:05.000"))
	log.Printf("🏁 [订单执行流程] ===== 开多仓执行流程成功完成 =====")

	result := make(map[string]interface{})
	result["orderId"] = 0 // Hyperliquid没有返回order ID
	result["symbol"] = symbol
	result["status"] = "FILLED"

	return result, nil
}

// OpenShort 开空仓
func (t *HyperliquidTrader) OpenShort(symbol string, quantity float64, leverage int) (map[string]interface{}, error) {
	// 📋 [订单执行流程] 开空仓流程开始
	log.Printf("🚀 [订单执行流程] ===== 开空仓执行流程开始 =====")
	log.Printf("🚀 [订单执行流程] 输入参数:")
	log.Printf("🚀 [订单执行流程] symbol: %s", symbol)
	log.Printf("🚀 [订单执行流程] quantity: %.8f", quantity)
	log.Printf("🚀 [订单执行流程] leverage: %d", leverage)
	log.Printf("🚀 [订单执行流程] 开始时间: %s", time.Now().Format("2006-01-02 15:04:05.000"))

	// 先取消该币种的所有委托单
	log.Printf("📋 [订单执行流程] 步骤1: 取消旧委托单")
	if err := t.CancelAllOrders(symbol); err != nil {
		log.Printf("  ⚠ 取消旧委托单失败: %v", err)
	} else {
		log.Printf("✅ [订单执行流程] 步骤1完成: 旧委托单已取消")
	}

	// 设置杠杆
	log.Printf("📋 [订单执行流程] 步骤2: 设置杠杆")
	if err := t.SetLeverage(symbol, leverage); err != nil {
		log.Printf("❌ [订单执行流程] 步骤2失败: 杠杆设置失败: %v", err)
		return nil, err
	} else {
		log.Printf("✅ [订单执行流程] 步骤2完成: 杠杆已设置为 %dx", leverage)
	}

	// Hyperliquid symbol格式
	log.Printf("📋 [订单执行流程] 步骤3: 解析币种符号")
	coin, err := t.resolveCoin(symbol, "")
	if err != nil {
		log.Printf("❌ [订单执行流程] 步骤3失败: 币种解析失败: %v", err)
		return nil, err
	} else {
		log.Printf("✅ [订单执行流程] 步骤3完成: 币种符号已解析为 %s", coin)
	}

	// 获取当前价格
	log.Printf("📋 [订单执行流程] 步骤4: 获取市场价格")
	price, err := t.GetMarketPrice(symbol)
	if err != nil {
		log.Printf("❌ [订单执行流程] 步骤4失败: 获取市场价格失败: %v", err)
		return nil, err
	} else {
		log.Printf("✅ [订单执行流程] 步骤4完成: 市场价格获取成功 %.8f", price)
	}

	// 获取API精度信息
	szDecimals := t.getSzDecimals(coin)
	maxPriceDecimals, hasPxDecimals := t.getPxDecimals(coin)

	// ⚠️ 关键：根据币种精度要求，四舍五入数量
	roundedQuantity := t.roundToSzDecimals(coin, quantity)
	log.Printf("  📏 数量精度处理: %.8f -> %.8f (szDecimals=%d)", quantity, roundedQuantity, szDecimals)

	// ⚠️ 关键：价格精度处理（5位有效数字 + maxDecimals = 6 - szDecimals）
	var aggressivePrice float64
	var priceMultiplier float64

	// 根据资产类型调整激进定价策略
	if t.isStockAsset(coin) {
		priceMultiplier = 0.99 // 与加密资产保持一致
		log.Printf("🎯 [HIP-3] 股票资产使用标准定价策略: 0.99倍")
	} else {
		priceMultiplier = 0.99 // 加密货币使用原策略
		log.Printf("📈 [HIP-3] 加密货币使用标准定价策略: 0.99倍")
	}

	slippage := math.Abs(priceMultiplier - 1.0)
	aggressivePrice = t.iocLimitPriceForCoin(coin, false, price, slippage)
	log.Printf("  💰 价格精度处理: %.8f * %.3f -> %.8f -> %.8f", price, priceMultiplier, price*priceMultiplier, aggressivePrice)

	// 价格预验证
	validatedPrice, err := t.validateOrderPrice(coin, aggressivePrice, false)
	if err != nil {
		return nil, fmt.Errorf("价格验证失败: %w", err)
	}

	// 📋 [订单参数完整打印] 开空仓订单构建参数详细记录
	log.Printf("🔍 [完整订单参数] ===== 开空仓订单参数详情 =====")
	log.Printf("🔍 [完整订单参数] 基础信息:")
	log.Printf("  symbol: %s", symbol)
	log.Printf("  coin: %s", coin)
	log.Printf("  side: SELL")
	log.Printf("  orderType: LIMIT")
	log.Printf("  timeInForce: IOC")
	log.Printf("")
	log.Printf("🔍 [完整订单参数] 价格信息:")
	log.Printf("  marketPrice: %.8f", price)
	log.Printf("  priceMultiplier: %.3f", priceMultiplier)
	log.Printf("  calculatedPrice: %.8f", price*priceMultiplier)
	log.Printf("  aggressivePrice: %.8f", aggressivePrice)
	log.Printf("  validatedPrice: %.8f", validatedPrice)
	log.Printf("")
	log.Printf("🔍 [完整订单参数] 数量信息:")
	log.Printf("  originalQuantity: %.8f", quantity)
	log.Printf("  roundedQuantity: %.8f", roundedQuantity)
	log.Printf("")
	log.Printf("🔍 [完整订单参数] 精度信息:")
	log.Printf("  szDecimals: %d", szDecimals)
	log.Printf("  maxPriceDecimals: %d", maxPriceDecimals)
	log.Printf("  hasPxDecimals(meta): %t", hasPxDecimals)
	log.Printf("")
	log.Printf("🔍 [完整订单参数] 资产信息:")
	log.Printf("  isStockAsset: %t", t.isStockAsset(coin))
	log.Printf("  leverage: %d", leverage)
	log.Printf("")
	// 保证抵押资产充足（可能自动用 USDC 兑换）
	positionValue := roundedQuantity * validatedPrice
	neededMargin := positionValue / float64(leverage)
	if err := t.ensureCollateralAvailable(dexForCoin(coin), neededMargin); err != nil {
		return nil, fmt.Errorf("抵押资产检查失败: %w", err)
	}

	log.Printf("🔍 [完整订单参数] 账户信息: (信息通过外部API获取)")
	log.Printf("🔍 [完整订单参数] ================================")

	// 创建市价卖出订单
	order := hyperliquid.CreateOrderRequest{
		Coin:  coin,
		IsBuy: false,
		Size:  roundedQuantity, // 使用四舍五入后的数量
		Price: validatedPrice,  // 使用验证后的价格（对股票会进行截断）
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{
				Tif: hyperliquid.TifIoc,
			},
		},
		ReduceOnly: false,
	}

	// 📋 [订单结构完整打印] 最终订单结构详情
	log.Printf("🔍 [订单结构] ===== 最终订单结构详情 =====")
	log.Printf("🔍 [订单结构] order.Coin: %s", order.Coin)
	log.Printf("🔍 [订单结构] order.IsBuy: %t", order.IsBuy)
	log.Printf("🔍 [订单结构] order.Size: %.8f", order.Size)
	log.Printf("🔍 [订单结构] order.Price: %.8f", order.Price)
	log.Printf("🔍 [订单结构] order.OrderType.Limit.Tif: %s", string(order.OrderType.Limit.Tif))
	log.Printf("🔍 [订单结构] order.ReduceOnly: %t", order.ReduceOnly)
	log.Printf("🔍 [订单结构] ================================")

	// 📋 [API调用] 记录API调用开始时间
	apiCallStart := time.Now()
	log.Printf("🚀 [API调用] 开始执行订单API调用...")
	log.Printf("🚀 [API调用] 调用时间: %s", apiCallStart.Format("2006-01-02 15:04:05.000"))

	// ✅ 单次执行下单，失败直接返回，便于定位问题
	_, err = t.exchange.Order(t.ctx, order, nil) // builder 暂停

	// 📋 [API调用] 记录API调用结束时间和结果
	apiCallEnd := time.Now()
	apiCallDuration := apiCallEnd.Sub(apiCallStart)
	log.Printf("🏁 [API调用] API调用完成")
	log.Printf("🏁 [API调用] 结束时间: %s", apiCallEnd.Format("2006-01-02 15:04:05.000"))
	log.Printf("🏁 [API调用] 耗时: %v", apiCallDuration)
	log.Printf("🏁 [API调用] 结果: %s", map[bool]string{true: "成功", false: "失败"}[err == nil])

	if err != nil {
		// 额外打印参考价对比，便于排查 “reference price” 相关错误
		if refPrice, refErr := t.GetMarketPrice(symbol); refErr == nil && refPrice > 0 {
			deviation := (order.Price - refPrice) / refPrice * 100
			log.Printf("📊 [价格对比] 订单价格=%.8f, 参考价格=%.8f, 偏差=%.2f%%", order.Price, refPrice, deviation)
		} else if refErr != nil {
			log.Printf("⚠️ [价格对比] 获取参考价格失败: %v", refErr)
		}

		log.Printf("❌ [API调用] 失败原因: %v", err)
		log.Printf("❌ [订单执行流程] 最终步骤失败: API调用失败")
		log.Printf("🚀 [订单执行流程] ===== 开空仓执行流程失败 =====")
		return nil, fmt.Errorf("开空仓失败: %w", err)
	}

	log.Printf("✓ 开空仓成功: %s 数量: %.4f", symbol, roundedQuantity)

	// 📋 [订单执行流程] 开空仓流程成功完成
	log.Printf("🏁 [订单执行流程] 步骤6: 订单执行成功")
	log.Printf("🏁 [订单执行流程] 成交信息:")
	log.Printf("🏁 [订单执行流程] symbol: %s", symbol)
	log.Printf("🏁 [订单执行流程] quantity: %.4f", roundedQuantity)
	log.Printf("🏁 [订单执行流程] 成交价格: %.8f", order.Price)
	log.Printf("🏁 [订单执行流程] 结束时间: %s", time.Now().Format("2006-01-02 15:04:05.000"))
	log.Printf("🏁 [订单执行流程] ===== 开空仓执行流程成功完成 =====")

	result := make(map[string]interface{})
	result["orderId"] = 0
	result["symbol"] = symbol
	result["status"] = "FILLED"

	return result, nil
}

// CloseLong 平多仓
func (t *HyperliquidTrader) CloseLong(symbol string, quantity float64) (map[string]interface{}, error) {
	// 如果数量为0，获取当前持仓数量
	if quantity == 0 {
		positions, err := t.GetPositions()
		if err != nil {
			return nil, err
		}

		for _, pos := range positions {
			if pos["symbol"] == symbol && pos["side"] == "long" {
				quantity = pos["positionAmt"].(float64)
				break
			}
		}

		if quantity == 0 {
			return nil, fmt.Errorf("没有找到 %s 的多仓", symbol)
		}
	}

	// Hyperliquid symbol格式
	coin, err := t.resolveCoin(symbol, "")
	if err != nil {
		return nil, err
	}

	// 获取当前价格
	price, err := t.GetMarketPrice(symbol)
	if err != nil {
		return nil, err
	}

	// ⚠️ 关键：根据币种精度要求，向上取整数量，避免减仓不足留下残余
	roundedQuantity := t.roundToSzDecimalsCeil(coin, quantity)
	log.Printf("  📏 数量精度处理: %.8f -> %.8f (szDecimals=%d)", quantity, roundedQuantity, t.getSzDecimals(coin))

	// ⚠️ 关键：价格也需要处理为5位有效数字
	aggressivePrice := t.iocLimitPriceForCoin(coin, false, price, 0.01)
	log.Printf("  💰 价格精度处理: %.8f -> %.8f", price*0.99, aggressivePrice)

	// 创建平仓订单（卖出 + ReduceOnly）
	order := hyperliquid.CreateOrderRequest{
		Coin:  coin,
		IsBuy: false,
		Size:  roundedQuantity, // 使用四舍五入后的数量
		Price: aggressivePrice, // 使用处理后的价格
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{
				Tif: hyperliquid.TifIoc,
			},
		},
		ReduceOnly: true, // 只平仓，不开新仓
	}

	_, err = t.exchange.Order(t.ctx, order, nil) // builder 暂停
	if err != nil {
		return nil, fmt.Errorf("平多仓失败: %w", err)
	}

	log.Printf("✓ 平多仓成功: %s 数量: %.4f", symbol, roundedQuantity)

	// 平仓后取消该币种的所有挂单
	if err := t.CancelAllOrders(symbol); err != nil {
		log.Printf("  ⚠ 取消挂单失败: %v", err)
	}

	result := make(map[string]interface{})
	result["orderId"] = 0
	result["symbol"] = symbol
	result["status"] = "FILLED"

	return result, nil
}

// CloseShort 平空仓
func (t *HyperliquidTrader) CloseShort(symbol string, quantity float64) (map[string]interface{}, error) {
	// 如果数量为0，获取当前持仓数量
	if quantity == 0 {
		positions, err := t.GetPositions()
		if err != nil {
			return nil, err
		}

		for _, pos := range positions {
			if pos["symbol"] == symbol && pos["side"] == "short" {
				quantity = pos["positionAmt"].(float64)
				break
			}
		}

		if quantity == 0 {
			return nil, fmt.Errorf("没有找到 %s 的空仓", symbol)
		}
	}

	// Hyperliquid symbol格式
	coin, err := t.resolveCoin(symbol, "")
	if err != nil {
		return nil, err
	}

	// 获取当前价格
	price, err := t.GetMarketPrice(symbol)
	if err != nil {
		return nil, err
	}

	// ⚠️ 关键：根据币种精度要求，向上取整数量，避免减仓不足留下残余
	roundedQuantity := t.roundToSzDecimalsCeil(coin, quantity)
	log.Printf("  📏 数量精度处理: %.8f -> %.8f (szDecimals=%d)", quantity, roundedQuantity, t.getSzDecimals(coin))

	// ⚠️ 关键：价格也需要处理为5位有效数字
	aggressivePrice := t.iocLimitPriceForCoin(coin, true, price, 0.01)
	log.Printf("  💰 价格精度处理: %.8f -> %.8f", price*1.01, aggressivePrice)

	// 创建平仓订单（买入 + ReduceOnly）
	order := hyperliquid.CreateOrderRequest{
		Coin:  coin,
		IsBuy: true,
		Size:  roundedQuantity, // 使用四舍五入后的数量
		Price: aggressivePrice, // 使用处理后的价格
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{
				Tif: hyperliquid.TifIoc,
			},
		},
		ReduceOnly: true,
	}

	_, err = t.exchange.Order(t.ctx, order, nil) // builder 暂停
	if err != nil {
		return nil, fmt.Errorf("平空仓失败: %w", err)
	}

	log.Printf("✓ 平空仓成功: %s 数量: %.4f", symbol, roundedQuantity)

	// 平仓后取消该币种的所有挂单
	if err := t.CancelAllOrders(symbol); err != nil {
		log.Printf("  ⚠ 取消挂单失败: %v", err)
	}

	result := make(map[string]interface{})
	result["orderId"] = 0
	result["symbol"] = symbol
	result["status"] = "FILLED"

	return result, nil
}

// CancelStopOrders 取消该币种的止盈/止

// CancelStopLossOrders 仅取消止损单（实时查询并分类）
func (t *HyperliquidTrader) CancelStopLossOrders(symbol string) error {
	positionSide, err := t.getPositionSide(symbol)
	if err != nil {
		return err
	}

	triggerOrders, err := t.triggerOrdersBySymbol(symbol)
	if err != nil {
		return fmt.Errorf("获取触发挂单失败: %w", err)
	}

	if len(triggerOrders) == 0 {
		log.Printf("  ℹ %s 无触发挂单", symbol)
		return nil
	}

	log.Printf("  🔍 %s 发现 %d 个触发挂单，开始分类...", symbol, len(triggerOrders))

	coin, err := t.resolveCoin(symbol, "")
	if err != nil {
		return err
	}
	canceled := 0
	for _, ord := range triggerOrders {
		// 详细分类日志
		orderKind := classifyTpSl(ord, positionSide)
		log.Printf("    📋 订单 OID=%d: OrderType='%s', IsPositionTpSl=%t, TriggerCondition='%s', 分类结果='%s'",
			ord.Oid, ord.OrderType, ord.IsPositionTpSl, ord.TriggerCondition, orderKind)

		if orderKind != "sl" {
			log.Printf("      ⏭️  跳过非止损单 (%s)", orderKind)
			continue
		}

		log.Printf("      🎯 准备取消止损单 (OID=%d)", ord.Oid)
		if _, err := t.exchange.Cancel(t.ctx, coin, ord.Oid); err != nil {
			log.Printf("      ⚠ 取消止损单失败 (oid=%d): %v", ord.Oid, err)
			continue
		}
		log.Printf("      ✅ 成功取消止损单 (OID=%d)", ord.Oid)
		canceled++
	}

	if canceled == 0 {
		log.Printf("  ℹ %s 未找到可取消的止损单", symbol)
	} else {
		log.Printf("  ✓ 已取消 %s 的 %d 个止损单", symbol, canceled)
	}

	t.clearOrderRef(symbol, true)
	return nil
}

// CancelTakeProfitOrders 仅取消止盈单（实时查询并分类）
func (t *HyperliquidTrader) CancelTakeProfitOrders(symbol string) error {
	positionSide, err := t.getPositionSide(symbol)
	if err != nil {
		return err
	}

	triggerOrders, err := t.triggerOrdersBySymbol(symbol)
	if err != nil {
		return fmt.Errorf("获取触发挂单失败: %w", err)
	}

	coin, err := t.resolveCoin(symbol, "")
	if err != nil {
		return err
	}
	canceled := 0
	for _, ord := range triggerOrders {
		if classifyTpSl(ord, positionSide) != "tp" {
			continue
		}
		if _, err := t.exchange.Cancel(t.ctx, coin, ord.Oid); err != nil {
			log.Printf("  ⚠ 取消止盈单失败 (oid=%d): %v", ord.Oid, err)
			continue
		}
		canceled++
	}

	if canceled == 0 {
		log.Printf("  ℹ %s 未找到可取消的止盈单", symbol)
	} else {
		log.Printf("  ✓ 已取消 %s 的 %d 个止盈单", symbol, canceled)
	}

	t.clearOrderRef(symbol, false)
	return nil
}

// CancelAllOrders 取消该币种的所有挂单
func (t *HyperliquidTrader) CancelAllOrders(symbol string) error {
	coin, err := t.resolveCoin(symbol, "")
	if err != nil {
		return err
	}

	// 获取所有挂单
	openOrders, err := t.exchange.Info().OpenOrders(t.ctx, t.walletAddr)
	if err != nil {
		return fmt.Errorf("获取挂单失败: %w", err)
	}

	// 取消该币种的所有挂单
	for _, order := range openOrders {
		if order.Coin == coin {
			_, err := t.exchange.Cancel(t.ctx, coin, order.Oid)
			if err != nil {
				log.Printf("  ⚠ 取消订单失败 (oid=%d): %v", order.Oid, err)
			}
		}
	}

	log.Printf("  ✓ 已取消 %s 的所有挂单", symbol)
	t.clearOrderRef(symbol, true)
	t.clearOrderRef(symbol, false)
	return nil
}

// CancelStopOrders 取消该币种的止盈/止损单（用于调整止盈止损位置）
func (t *HyperliquidTrader) CancelStopOrders(symbol string) error {
	coin, err := t.resolveCoin(symbol, "")
	if err != nil {
		return err
	}

	// 获取所有挂单
	openOrders, err := t.exchange.Info().OpenOrders(t.ctx, t.walletAddr)
	if err != nil {
		return fmt.Errorf("获取挂单失败: %w", err)
	}

	// 注意：Hyperliquid SDK 的 OpenOrder 结构不暴露 trigger 字段
	// 因此暂时取消该币种的所有挂单（包括止盈止损单）
	// 这是安全的，因为在设置新的止盈止损之前，应该清理所有旧订单
	canceledCount := 0
	for _, order := range openOrders {
		if order.Coin == coin {
			_, err := t.exchange.Cancel(t.ctx, coin, order.Oid)
			if err != nil {
				log.Printf("  ⚠ 取消订单失败 (oid=%d): %v", order.Oid, err)
				continue
			}
			canceledCount++
		}
	}

	if canceledCount == 0 {
		log.Printf("  ℹ %s 没有挂单需要取消", symbol)
	} else {
		log.Printf("  ✓ 已取消 %s 的 %d 个挂单（包括止盈/止损单）", symbol, canceledCount)
	}

	t.clearOrderRef(symbol, true)
	t.clearOrderRef(symbol, false)

	return nil
}

// cancelOrderByRef 使用已知的 oid 或 cloid 取消单个挂单
func (t *HyperliquidTrader) cancelOrderByRef(symbol string, ref orderRef) error {
	coin := convertSymbolToHyperliquid(symbol)

	if ref.cloid != "" {
		if _, err := t.exchange.CancelByCloid(t.ctx, coin, ref.cloid); err != nil {
			return err
		}
		log.Printf("  ✓ 已通过 cloid 取消 %s 的挂单 (cloid=%s)", symbol, ref.cloid)
		return nil
	}

	if ref.oid > 0 {
		if _, err := t.exchange.Cancel(t.ctx, coin, ref.oid); err != nil {
			return err
		}
		log.Printf("  ✓ 已取消 %s 的挂单 (oid=%d)", symbol, ref.oid)
		return nil
	}

	return fmt.Errorf("未找到 %s 的订单标识，无法取消", symbol)
}

// buildCloid 生成可追踪的 cloid，用于后续精准取消挂单
func (t *HyperliquidTrader) buildCloid(symbol, kind string) string {
	b := make([]byte, 16) // 16 bytes => 32 hex chars
	if _, err := rand.Read(b); err != nil {
		// 理论上不会失败，失败则退化为时间戳
		return fmt.Sprintf("%s%x", kind, time.Now().UnixNano())
	}
	hexPart := hex.EncodeToString(b)
	return "0x" + hexPart
}

func (t *HyperliquidTrader) rememberStopLossOrder(symbol string, status hyperliquid.OrderStatus, cloid string) {
	t.orderMu.Lock()
	defer t.orderMu.Unlock()
	t.stopLossOrders[symbol] = orderRef{
		oid:   extractOid(status),
		cloid: cloid,
	}
}

func (t *HyperliquidTrader) rememberTakeProfitOrder(symbol string, status hyperliquid.OrderStatus, cloid string) {
	t.orderMu.Lock()
	defer t.orderMu.Unlock()
	t.takeProfitOrders[symbol] = orderRef{
		oid:   extractOid(status),
		cloid: cloid,
	}
}

func (t *HyperliquidTrader) getOrderRef(symbol string, isStopLoss bool) (orderRef, bool) {
	t.orderMu.Lock()
	defer t.orderMu.Unlock()
	if isStopLoss {
		ref, ok := t.stopLossOrders[symbol]
		return ref, ok
	}
	ref, ok := t.takeProfitOrders[symbol]
	return ref, ok
}

func (t *HyperliquidTrader) clearOrderRef(symbol string, isStopLoss bool) {
	t.orderMu.Lock()
	defer t.orderMu.Unlock()
	if isStopLoss {
		delete(t.stopLossOrders, symbol)
		return
	}
	delete(t.takeProfitOrders, symbol)
}

func extractOid(status hyperliquid.OrderStatus) int64 {
	if status.Resting != nil {
		return status.Resting.Oid
	}
	if status.Filled != nil {
		return int64(status.Filled.Oid)
	}
	return 0
}

// triggerOrdersBySymbol 获取当前币种的触发类挂单（仅 ReduceOnly）
func (t *HyperliquidTrader) triggerOrdersBySymbol(symbol string) ([]hyperliquid.FrontendOpenOrder, error) {
	coin := convertSymbolToHyperliquid(symbol)
	orders, err := t.exchange.Info().FrontendOpenOrders(t.ctx, t.walletAddr)
	if err != nil {
		return nil, err
	}

	var filtered []hyperliquid.FrontendOpenOrder
	for _, ord := range orders {
		if ord.Coin != coin {
			continue
		}
		if !ord.ReduceOnly {
			continue
		}
		if !(ord.IsTrigger || ord.IsPositionTpSl) {
			continue
		}
		filtered = append(filtered, ord)
	}
	return filtered, nil
}

// ListActiveTpSlOrders 列出某个币种的当前触发类止盈/止损单（仅ReduceOnly）
func (t *HyperliquidTrader) ListActiveTpSlOrders(symbol string) ([]hyperliquid.FrontendOpenOrder, error) {
	return t.triggerOrdersBySymbol(symbol)
}

// classifyTpSl 统一的订单类型分类函数
// 封装 detectOrderKind，提供更清晰的接口
func classifyTpSl(ord hyperliquid.FrontendOpenOrder, positionSide string) string {
	return detectOrderKind(ord, positionSide)
}

// detectOrderKind 智能检测订单类型（止盈/止损）
// 使用多层检测策略，基于 v0.24.0 SDK 的特性进行优化
func detectOrderKind(ord hyperliquid.FrontendOpenOrder, positionSide string) string {
	// 第1层：直接检查 OrderType 字段（最高优先级）
	if kind := checkOrderTypeField(ord); kind != "" {
		return kind
	}

	// 第2层：对于明确标记为TP/SL的订单，使用触发条件判断
	if ord.IsPositionTpSl {
		if kind := checkTriggerCondition(ord, positionSide); kind != "" {
			return kind
		}
		// 无法通过触发条件判断，标记为需要启发式处理
		return "unknown"
	}

	// 第3层：对于非TP/SL标记的触发单，尝试触发条件判断
	if ord.IsTrigger {
		if kind := checkTriggerCondition(ord, positionSide); kind != "" {
			return kind
		}
	}

	// 完全无法判断
	return ""
}

// checkOrderTypeField 检查 OrderType 字段
func checkOrderTypeField(ord hyperliquid.FrontendOpenOrder) string {
	orderType := strings.ToLower(strings.TrimSpace(ord.OrderType))

	// 精确匹配（最高优先级）
	switch orderType {
	case "tp":
		return "tp"
	case "sl":
		return "sl"
	}

	// 包含匹配（中等优先级）
	switch {
	case strings.Contains(orderType, "tp") && !strings.Contains(orderType, "sl"):
		return "tp"
	case strings.Contains(orderType, "sl") && !strings.Contains(orderType, "tp"):
		return "sl"
	}

	// 特殊值处理
	switch orderType {
	case "takeprofit", "take_profit":
		return "tp"
	case "stoploss", "stop_loss":
		return "sl"
	}

	return ""
}

// checkTriggerCondition 通过触发条件判断订单类型
func checkTriggerCondition(ord hyperliquid.FrontendOpenOrder, positionSide string) string {
	cond := normalizeTriggerCond(ord.TriggerCondition)
	if cond == "" {
		return ""
	}

	return classifyByCond(strings.ToUpper(positionSide), cond, ord.IsTrigger)
}

func normalizeTriggerCond(cond string) string {
	// Hyperliquid 返回的 trigger 条件常见格式：
	// "mark price <= trigger px", "last price >= trigger px" 等
	// 这里做包含匹配，避免因为前缀文本导致解析失败。
	c := strings.TrimSpace(strings.ToLower(cond))

	// 先做语义包含匹配
	if strings.Contains(c, "<=") || strings.Contains(c, "lte") || strings.Contains(c, "below") {
		return "<="
	}
	if strings.Contains(c, ">=") || strings.Contains(c, "gte") || strings.Contains(c, "above") {
		return ">="
	}

	// 再做单字符匹配
	if strings.Contains(c, "<") {
		return "<"
	}
	if strings.Contains(c, ">") {
		return ">"
	}

	return ""
}

func classifyByCond(side, cond string, isTrigger bool) string {
	isDown := cond == "<" || cond == "<="
	isUp := cond == ">" || cond == ">="

	if !isDown && !isUp {
		// 修复：对于模糊的触发条件，不要轻易分类为止损单
		// 返回空字符串，让上层逻辑使用更可靠的方式判断
		return ""
	}

	switch side {
	case "LONG":
		if isDown {
			return "sl"
		}
		if isUp {
			return "tp"
		}
	case "SHORT":
		if isUp {
			return "sl"
		}
		if isDown {
			return "tp"
		}
	}
	return ""
}

// getPositionSide 获取持仓方向（LONG/SHORT）
func (t *HyperliquidTrader) getPositionSide(symbol string) (string, error) {
	positions, err := t.GetPositions()
	if err != nil {
		return "", err
	}
	for _, pos := range positions {
		sym, _ := pos["symbol"].(string)
		side, _ := pos["side"].(string)
		if sym == symbol && side != "" {
			return strings.ToUpper(side), nil
		}
	}
	return "", fmt.Errorf("持仓不存在: %s", symbol)
}

// GetMarketPrice 获取市场价格
func (t *HyperliquidTrader) GetMarketPrice(symbol string) (float64, error) {
	coin, err := t.resolveCoin(symbol, "")
	if err != nil {
		return 0, err
	}

	// HIP-3 / 非加密资产优先用 recentTrades（AllMids 默认不包含股票/商品）
	if t.isStockAsset(coin) {
		// 1) metaAndAssetCtxs -> midPx/markPx/oraclePx
		if priceFloat, err := t.fetchHip3PriceFromAssetCtx(coin); err == nil {
			return priceFloat, nil
		} else {
			log.Printf("⚠️ metaAndAssetCtxs 获取价格失败: %v", err)
		}

		// 2) recentTrades 作为兜底
		if priceFloat, err := t.fetchPriceFromRecentTrades(coin); err == nil {
			return priceFloat, nil
		} else {
			log.Printf("⚠️ recentTrades 获取价格失败: %v", err)
		}

		// 3) 再尝试 allMids（先 WS 后 HTTP）
		if priceFloat, err := t.getMidPrice(coin); err == nil {
			log.Printf("🔄 使用 allMids 获取价格成功: %s = %.6f", coin, priceFloat)
			return priceFloat, nil
		} else {
			log.Printf("⚠️ allMids 获取价格失败: %v", err)
		}
	}

	// 加载 allMids（优先 WS，失败再 HTTP）
	if priceFloat, err := t.getMidPrice(coin); err == nil {
		return priceFloat, nil
	} else {
		log.Printf("⚠️ allMids 获取价格失败: %v", err)
	}

	// 使用 recentTrades 兜底获取最新成交价
	if priceFloat, err := t.fetchPriceFromRecentTrades(coin); err == nil {
		log.Printf("🔄 使用 recentTrades 获取价格成功: %s = %.6f", coin, priceFloat)
		return priceFloat, nil
	} else {
		log.Printf("⚠️ recentTrades 获取价格失败: %v", err)
	}

	return 0, fmt.Errorf("未找到 %s 的价格", coin)
}

// SetStopLoss 设置止损单
func (t *HyperliquidTrader) SetStopLoss(symbol string, positionSide string, quantity, stopPrice float64) error {
	coin := convertSymbolToHyperliquid(symbol)

	isBuy := positionSide == "SHORT" // 空仓止损=买入，多仓止损=卖出
	slCloid := t.buildCloid(symbol, "sl")

	// ⚠️ 关键：根据币种精度要求，四舍五入数量
	roundedQuantity := t.roundToSzDecimals(coin, quantity)

	// ⚠️ 关键：价格精度处理
	roundedStopPrice := t.roundPriceForCoin(coin, stopPrice, false)

	// 创建止损单（Trigger Order）
	order := hyperliquid.CreateOrderRequest{
		Coin:  coin,
		IsBuy: isBuy,
		Size:  roundedQuantity,  // 使用四舍五入后的数量
		Price: roundedStopPrice, // 使用处理后的价格
		OrderType: hyperliquid.OrderType{
			Trigger: &hyperliquid.TriggerOrderType{
				TriggerPx: roundedStopPrice,
				IsMarket:  true,
				Tpsl:      "sl", // stop loss
			},
		},
		ReduceOnly:    true,
		ClientOrderID: &slCloid,
	}

	status, err := t.exchange.Order(t.ctx, order, nil) // builder 暂停
	if err != nil {
		return fmt.Errorf("设置止损失败: %w", err)
	}

	t.rememberStopLossOrder(symbol, status, slCloid)
	log.Printf("  止损价设置: %.4f (cloid=%s oid=%d)", roundedStopPrice, slCloid, extractOid(status))
	return nil
}

// SetTakeProfit 设置止盈单
func (t *HyperliquidTrader) SetTakeProfit(symbol string, positionSide string, quantity, takeProfitPrice float64) error {
	coin := convertSymbolToHyperliquid(symbol)

	isBuy := positionSide == "SHORT" // 空仓止盈=买入，多仓止盈=卖出

	// ⚠️ 关键：根据币种精度要求，四舍五入数量
	roundedQuantity := t.roundToSzDecimals(coin, quantity)

	// ⚠️ 关键：价格精度处理
	roundedTakeProfitPrice := t.roundPriceForCoin(coin, takeProfitPrice, false)

	// 使用 Trigger 订单设置止盈
	tpCloid := t.buildCloid(symbol, "tp")

	order := hyperliquid.CreateOrderRequest{
		Coin:  coin,
		IsBuy: isBuy,
		Size:  roundedQuantity,        // 使用四舍五入后的数量
		Price: roundedTakeProfitPrice, // 兼容性保留
		OrderType: hyperliquid.OrderType{
			Trigger: &hyperliquid.TriggerOrderType{
				TriggerPx: roundedTakeProfitPrice,
				IsMarket:  true,
				Tpsl:      "tp", // take profit
			},
		},
		ReduceOnly:    true,
		ClientOrderID: &tpCloid,
	}

	status, err := t.exchange.Order(t.ctx, order, nil) // builder 暂停
	if err != nil {
		return fmt.Errorf("设置止盈失败: %w", err)
	}

	t.rememberTakeProfitOrder(symbol, status, tpCloid)
	log.Printf("  止盈价设置: %.4f (cloid=%s oid=%d)", roundedTakeProfitPrice, tpCloid, extractOid(status))
	return nil
}

// FormatQuantity 格式化数量到正确的精度
func (t *HyperliquidTrader) FormatQuantity(symbol string, quantity float64) (string, error) {
	coin := convertSymbolToHyperliquid(symbol)
	szDecimals := t.getSzDecimals(coin)

	// 使用szDecimals格式化数量
	formatStr := fmt.Sprintf("%%.%df", szDecimals)
	return fmt.Sprintf(formatStr, quantity), nil
}

// getSzDecimals 获取币种的数量精度
func (t *HyperliquidTrader) getSzDecimals(coin string) int {
	normalizedCoin := normalizeHip3Symbol(coin)

	// 优先处理常规加密资产（无冒号符号）- 使用 Meta.Universe 中的精度
	if !strings.Contains(normalizedCoin, ":") {
		if t.meta != nil && t.meta.Universe != nil {
			for _, asset := range t.meta.Universe {
				if strings.EqualFold(asset.Name, normalizedCoin) {
					return asset.SzDecimals
				}
			}
		}
		log.Printf("❌ [Hyperliquid] Meta.Universe 未找到 %s 的 SzDecimals（crypto 资产）", normalizedCoin)
		panic(fmt.Sprintf("SzDecimals not found in Meta.Universe for %s. Meta may be stale or API unavailable.", coin))
	}

	// 首先检查 HIP-3 缓存
	if t.hip3Meta != nil {
		if asset, ok := t.hip3Meta[normalizedCoin]; ok {
			log.Printf("✅ [HIP-3 API] %s 使用缓存的 SzDecimals: %d", normalizedCoin, asset.SzDecimals)
			return asset.SzDecimals
		}
	}

	// 如果未缓存，尝试刷新获取
	if norm, _, err := t.fetchPerpMetaAsset(coin, true); err == nil && norm != "" {
		if asset, ok := t.hip3Meta[norm]; ok {
			log.Printf("✅ [HIP-3 API] %s 使用刷新的 SzDecimals: %d", norm, asset.SzDecimals)
			return asset.SzDecimals
		}
	} else if err != nil {
		log.Printf("❌ [HIP-3 API] 获取 %s SzDecimals 失败: %v", coin, err)
	}

	// 不使用任何默认值 - 如果 API 无法提供数据，这是一个严重的配置错误
	log.Printf("🚨 [HIP-3 API] 严重错误: 无法从 API 获取 %s (%s) 的 SzDecimals", coin, normalizedCoin)
	panic(fmt.Sprintf("SzDecimals not found in HIP-3 API response for %s. API configuration may be incorrect.", coin))
}

// GetMeta 获取meta信息
func (t *HyperliquidTrader) GetMeta() *hyperliquid.Meta {
	return t.meta
}

// GetAllAssets 获取所有资产信息
func (t *HyperliquidTrader) GetAllAssets() ([]interface{}, error) {
	if t.meta == nil {
		return nil, fmt.Errorf("meta信息为空")
	}

	if t.meta.Universe == nil {
		return nil, fmt.Errorf("资产信息为空")
	}

	// 转换为通用接口类型
	var result []interface{}
	for _, asset := range t.meta.Universe {
		result = append(result, asset)
	}

	return result, nil
}

// roundToSzDecimals 将数量四舍五入到正确的精度
func (t *HyperliquidTrader) roundToSzDecimals(coin string, quantity float64) float64 {
	szDecimals := t.getSzDecimals(coin)

	// 计算倍数（10^szDecimals）
	multiplier := 1.0
	for i := 0; i < szDecimals; i++ {
		multiplier *= 10.0
	}

	// 截断到步长，避免向上取整导致无效数量
	return math.Floor(quantity*multiplier) / multiplier
}

// roundToSzDecimalsCeil 将数量向上取整到步长，避免平仓时截断留下残余
func (t *HyperliquidTrader) roundToSzDecimalsCeil(coin string, quantity float64) float64 {
	szDecimals := t.getSzDecimals(coin)

	// 计算倍数（10^szDecimals）
	multiplier := 1.0
	for i := 0; i < szDecimals; i++ {
		multiplier *= 10.0
	}

	return math.Ceil(quantity*multiplier) / multiplier
}

// getPxDecimals 计算价格允许的小数位（遵循官方 Tick & Lot 规则）并标记来源
// Hyperliquid 文档: 价格最多 5 位有效数字，且小数位 <= MAX_DECIMALS - szDecimals（perp: MAX_DECIMALS=6）。
// 官方接口未提供 pxDecimals 字段，所有资产统一按该规则推导。
func (t *HyperliquidTrader) getPxDecimals(coin string) (int, bool) {
	maxDecimals := t.getMaxPriceDecimals(coin)
	return maxDecimals, false // 基于官方规则推导，无 API pxDecimals
}

// getMaxPriceDecimals 根据 Tick & Lot 规则计算允许的最大小数位
// NOTE: perp 基数为 6；若未来支持现货，需要将 baseDecimals 调整为 8。
func (t *HyperliquidTrader) getMaxPriceDecimals(coin string) int {
	const baseDecimals = 6 // perp 市场，TODO(spot): 现货改为 8
	szDecimals := t.getSzDecimals(coin)
	maxDecimals := baseDecimals - szDecimals
	if maxDecimals < 0 {
		return 0
	}
	return maxDecimals
}

// isStockAsset 检测是否为 HIP-3 股票资产
func (t *HyperliquidTrader) isStockAsset(coin string) bool {
	normalizedCoin := normalizeHip3Symbol(coin)
	isStock := strings.Contains(normalizedCoin, ":")

	if isStock {
		log.Printf("🔍 [HIP-3] 检测到股票资产: %s", normalizedCoin)
	}

	return isStock
}

// requiresIsolated 检测资产是否需要逐仓（部分 HIP-3 资产 marginMode=strictIsolated）
func (t *HyperliquidTrader) requiresIsolated(coin string) bool {
	norm := normalizeHip3Symbol(coin)

	if asset, ok := t.hip3Meta[norm]; ok {
		if asset.OnlyIsolated || strings.EqualFold(asset.MarginMode, "strictIsolated") {
			return true
		}
	}

	// 未缓存时尝试拉取并更新
	if _, asset, err := t.fetchPerpMetaAsset(norm, true); err == nil && asset != nil {
		t.hip3Meta[norm] = *asset
		if asset.OnlyIsolated || strings.EqualFold(asset.MarginMode, "strictIsolated") {
			return true
		}
	}

	return false
}

// logPriceDetails 详细记录价格处理信息用于调试
func (t *HyperliquidTrader) logPriceDetails(symbol, coin string, price float64, context string) {
	log.Printf("🔍 [%s] 价格详情 for %s/%s:", context, symbol, coin)
	log.Printf("   • 原始价格: %.8f", price)

	log.Printf("   • maxDecimals(6-sz): %d 位小数", t.getMaxPriceDecimals(coin))

	sigfigPrice := t.roundPriceToSigfigs(price, false)
	tickDecimals := t.getPriceDecimals(coin)
	log.Printf("   • 5位有效数字舍入价格: %.8f", sigfigPrice)
	log.Printf("   • tick: %d 位小数 (6 - szDecimals)", tickDecimals)
}

// getPriceDecimals 根据 Hyperliquid 规则推导价格小数位 (max(0, 6 - szDecimals))
func (t *HyperliquidTrader) getPriceDecimals(coin string) int {
	return t.getMaxPriceDecimals(coin)
}

// roundPriceForCoin 根据 Tick & Lot 规则处理价格；truncate=true 时截断到步长
func (t *HyperliquidTrader) roundPriceForCoin(coin string, price float64, truncate bool) float64 {
	if price == 0 {
		return 0
	}

	// 整数价格允许无需有效数字限制
	if price == math.Trunc(price) {
		return price
	}

	// 先应用 5 位有效数字
	sigPrice := t.roundPriceToSigfigs(price, truncate)

	// 再应用 tick：小数位 = min(官方规则, recentTrades 推断值)
	priceDecimals, _ := t.getPxDecimals(coin)
	multiplier := math.Pow10(priceDecimals)
	var finalPrice float64
	if truncate {
		finalPrice = math.Floor(sigPrice*multiplier) / multiplier
	} else {
		finalPrice = math.Round(sigPrice*multiplier) / multiplier
	}

	log.Printf("📊 [PriceFmt] 5 sigfig + tick(%d 位): %.8f -> %.8f -> %.8f",
		priceDecimals, price, sigPrice, finalPrice)
	return finalPrice
}

// iocLimitPriceForCoin 生成符合 Hyperliquid tick/lot 规则的 IOC 限价（用于模拟市价单）
//
// 优先使用 SDK 的 SlippagePrice（与 Hyperliquid 官方规则保持一致），
// 避免本地截断/浮点误差导致偶发 "Price must be divisible by tick size"。
// 当 coin 未被 SDK 正确识别（例如映射缺失导致默认落到 BTC）时，回退到本地 roundPriceForCoin。
func (t *HyperliquidTrader) iocLimitPriceForCoin(coin string, isBuy bool, marketPrice float64, slippage float64) float64 {
	if marketPrice == 0 {
		return 0
	}
	if slippage < 0 {
		slippage = 0
	}

	if t.exchange != nil {
		asset := t.exchange.Info().NameToAsset(coin)
		if asset != 0 || strings.EqualFold(coin, "BTC") {
			px := marketPrice
			if p, err := t.exchange.SlippagePrice(t.ctx, coin, isBuy, slippage, &px); err == nil && p > 0 {
				return p
			}
		}
	}

	multiplier := 1.0
	if isBuy {
		multiplier = 1 + slippage
	} else {
		multiplier = 1 - slippage
	}
	return t.roundPriceForCoin(coin, marketPrice*multiplier, true)
}

// roundPriceToSigfigs 将价格四舍五入到5位有效数字
// Hyperliquid要求价格使用5位有效数字（significant figures）
func (t *HyperliquidTrader) roundPriceToSigfigs(price float64, truncate bool) float64 {
	if price == 0 {
		return 0
	}

	const sigfigs = 5 // 默认有效数字

	// 计算价格的数量级
	magnitude := math.Abs(price)

	// 计算需要的倍数
	multiplier := 1.0
	for magnitude >= 10 {
		magnitude /= 10
		multiplier /= 10
	}
	for magnitude < 1 {
		magnitude *= 10
		multiplier *= 10
	}

	// 应用有效数字精度
	for i := 0; i < sigfigs-1; i++ {
		multiplier *= 10
	}

	// 四舍五入或截断
	if truncate {
		return math.Floor(price*multiplier) / multiplier
	}
	return math.Round(price*multiplier) / multiplier
}

// roundPriceForStock [已废弃] 此函数不再被调用
// 股票和加密货币现在统一使用 roundPriceToSigfigs()
// 保留此函数仅作为参考，后续可删除
func (t *HyperliquidTrader) roundPriceForStock(price float64, truncate bool) float64 {
	if price == 0 {
		return 0
	}

	// 强制使用2位小数，覆盖动态逻辑，确保符合Hyperliquid步长要求
	multiplier := 100.0 // 强制2位小数
	var result float64
	if truncate {
		result = math.Floor(price*multiplier) / multiplier
	} else {
		result = math.Round(price*multiplier) / multiplier
	}

	log.Printf("📐 [HIP-3] 股票价格强制2位小数舍入: %.8f -> %.8f", price, result)
	return result
}

// validateOrderPrice 验证订单价格是否合理
func (t *HyperliquidTrader) validateOrderPrice(coin string, price float64, isBuy bool) (float64, error) {
	// Get current market price for validation
	symbol := convertSymbolFromHyperliquid(coin)
	marketPrice, err := t.GetMarketPrice(symbol)
	if err != nil {
		log.Printf("❌ [HIP-3] 无法获取市场价格进行验证: %v", err)
		return price, fmt.Errorf("failed to get market price for validation: %w", err)
	}

	// Check price deviation limits
	maxDeviation := 0.10 // 10% max deviation for crypto
	if t.isStockAsset(coin) {
		maxDeviation = 0.15 // 15% for stocks (放宽限制以提高成功率)
	}

	deviation := math.Abs(price-marketPrice) / marketPrice
	if deviation > maxDeviation {
		log.Printf("❌ [HIP-3] 价格偏差过大: 市场价格=%.6f, 订单价格=%.6f, 偏差=%.2f%% > 限制%.1f%%",
			marketPrice, price, deviation*100, maxDeviation*100)
		return price, fmt.Errorf("price deviation %.2f%% exceeds maximum %.1f%% for %s",
			deviation*100, maxDeviation*100, coin)
	}

	log.Printf("✅ [HIP-3] 价格验证通过: 市场价格=%.6f, 订单价格=%.6f, 偏差=%.2f%%",
		marketPrice, price, deviation*100)

	// 统一使用 5 位有效数字规则，不再对股票做额外的 2 位小数截断
	// 价格精度已在 roundPriceForCoin() 中统一处理
	return price, nil
}

// validatePriceStep [已废弃] 此函数不再被调用
// 股票和加密货币现在统一使用 5 位有效数字规则
// 保留此函数仅作为参考，后续可删除
func (t *HyperliquidTrader) validatePriceStep(coin string, price float64) (float64, error) {
	if t.isStockAsset(coin) {
		// 先截断到2位小数，确保符合步长要求
		truncatedPrice := math.Floor(price*100) / 100 // 使用Floor截断，不是Round四舍五入
		remainder := math.Mod(truncatedPrice*100, 1)
		if remainder > 1e-10 {
			log.Printf("❌ [HIP-3] 股票价格步长验证失败: %s 价格=%.8f, 余数=%.10f", coin, price, remainder)
			return price, fmt.Errorf("股票价格步长验证失败: %s 价格 %.8f 必须是0.01的整数倍 (当前余数: %.10f)", coin, price, remainder)
		}
		log.Printf("✅ [HIP-3] 股票价格步长验证通过: %s %.8f -> %.8f (截断)", coin, price, truncatedPrice)
		return truncatedPrice, nil // 返回截断后的价格用于订单
	}
	return price, nil
}

// executeOrderWithRetry 执行带重试机制的订单（针对股票价格验证失败）
func (t *HyperliquidTrader) executeOrderWithRetry(order *hyperliquid.CreateOrderRequest, maxRetries int) error {
	// 📋 [重试参数完整打印] 重试机制启动时记录完整参数
	log.Printf("🔄 [重试参数] ===== executeOrderWithRetry 启动参数 =====")
	log.Printf("🔄 [重试参数] order.Coin: %s", order.Coin)
	log.Printf("🔄 [重试参数] order.IsBuy: %t", order.IsBuy)
	log.Printf("🔄 [重试参数] order.Size: %.8f", order.Size)
	log.Printf("🔄 [重试参数] order.Price: %.8f", order.Price)
	log.Printf("🔄 [重试参数] order.OrderType.Limit.Tif: %s", string(order.OrderType.Limit.Tif))
	log.Printf("🔄 [重试参数] order.ReduceOnly: %t", order.ReduceOnly)
	log.Printf("🔄 [重试参数] maxRetries: %d", maxRetries)
	log.Printf("🔄 [重试参数] isStockAsset: %t", t.isStockAsset(order.Coin))
	log.Printf("🔄 [重试参数] szDecimals: %d", t.getSzDecimals(order.Coin))
	maxPxDecimals, hasPxDecimals := t.getPxDecimals(order.Coin)
	log.Printf("🔄 [重试参数] maxPriceDecimals: %d", maxPxDecimals)
	log.Printf("🔄 [重试参数] hasPxDecimals(meta): %t", hasPxDecimals)
	log.Printf("🔄 [重试参数] 重试开始时间: %s", time.Now().Format("2006-01-02 15:04:05.000"))
	log.Printf("🔄 [重试参数] =======================================")

	for attempt := 0; attempt < maxRetries; attempt++ {
		// 📋 [重试详情] 每次重试的详细信息
		log.Printf("🔄 [重试详情] ===== 第 %d 次重试开始 =====", attempt+1)
		log.Printf("🔄 [重试详情] attempt: %d/%d", attempt+1, maxRetries)
		log.Printf("🔄 [重试详情] 当前价格: %.8f", order.Price)
		log.Printf("🔄 [重试详情] 订单方向: %s", map[bool]string{true: "BUY", false: "SELL"}[order.IsBuy])
		log.Printf("🔄 [重试详情] 重试时间: %s", time.Now().Format("2006-01-02 15:04:05.000"))

		if attempt > 0 {
			log.Printf("🔄 [重试详情] 订单重试第 %d 次: %s 价格=%.8f", attempt+1, order.Coin, order.Price)
		}

		// 📋 [API调用] 重试中的API调用
		apiCallStart := time.Now()
		log.Printf("🚀 [重试API] 第 %d 次API调用开始...", attempt+1)
		log.Printf("🚀 [重试API] 调用时间: %s", apiCallStart.Format("2006-01-02 15:04:05.000"))

		// 📊 [关键价格对比] 获取当前市场价格并与订单价格对比
		if refPrice, refErr := t.GetMarketPrice(convertSymbolFromHyperliquid(order.Coin)); refErr == nil {
			deviation := (order.Price - refPrice) / refPrice * 100
			log.Printf("📊 [价格对比] 市场参考价: %.4f, 订单价格: %.4f, 偏差: %.2f%%", refPrice, order.Price, deviation)
		} else {
			log.Printf("⚠️ [价格对比] 无法获取市场参考价: %v", refErr)
		}

		// 执行订单
		_, err := t.exchange.Order(t.ctx, *order, nil) // builder 暂停

		apiCallEnd := time.Now()
		apiCallDuration := apiCallEnd.Sub(apiCallStart)
		log.Printf("🏁 [重试API] 第 %d 次API调用完成", attempt+1)
		log.Printf("🏁 [重试API] 耗时: %v", apiCallDuration)

		if err == nil {
			log.Printf("✅ [重试成功] 订单执行成功: %s @ %.8f (尝试次数: %d)", order.Coin, order.Price, attempt+1)
			log.Printf("✅ [重试成功] 总重试次数: %d", attempt+1)
			log.Printf("✅ [重试成功] 最终成交价格: %.8f", order.Price)
			log.Printf("🔄 [重试详情] ===== 第 %d 次重试成功 =====", attempt+1)
			return nil // 成功
		}

		// 📋 [错误分析] 详细的错误分析
		log.Printf("❌ [重试失败] 第 %d 次重试失败", attempt+1)
		log.Printf("❌ [重试失败] 失败原因: %v", err)
		log.Printf("❌ [重试失败] 完整错误: %+v", err)
		log.Printf("❌ [重试失败] 错误类型: %T", err)
		log.Printf("❌ [重试失败] 失败时间: %s", time.Now().Format("2006-01-02 15:04:05.000"))

		// 检查是否是价格相关错误且是股票资产
		errStr := strings.ToLower(err.Error())
		isPriceError := strings.Contains(errStr, "invalid price") ||
			strings.Contains(errStr, "price precision") ||
			strings.Contains(errStr, "price step")

		log.Printf("🔍 [错误分析] 错误类型分析:")
		log.Printf("🔍 [错误分析] isPriceError: %t", isPriceError)
		log.Printf("🔍 [错误分析] isStockAsset: %t", t.isStockAsset(order.Coin))
		log.Printf("🔍 [错误分析] canRetry: %t", isPriceError && t.isStockAsset(order.Coin) && attempt < maxRetries-1)
		log.Printf("🔍 [错误分析] 当前重试次数: %d/%d", attempt+1, maxRetries)

		if isPriceError && t.isStockAsset(order.Coin) && attempt < maxRetries-1 {
			log.Printf("⚠️ [价格调整] 股票价格错误，尝试调整价格重试: %v", err)

			// 记录价格调整前的状态
			oldPrice := order.Price
			log.Printf("🔧 [价格调整] 调整前价格: %.8f", oldPrice)

			// 调整价格（增加更保守的偏差）
			newPrice := t.adjustPriceForRetry(order.Coin, order.Price, attempt+1, order.IsBuy)

			log.Printf("🔧 [价格调整] 价格调整详情:")
			log.Printf("🔧 [价格调整] oldPrice: %.8f", oldPrice)
			log.Printf("🔧 [价格调整] newPrice: %.8f", newPrice)
			log.Printf("🔧 [价格调整] attempt: %d", attempt+1)
			log.Printf("🔧 [价格调整] isBuy: %t", order.IsBuy)
			log.Printf("🔧 [价格调整] priceChanged: %t", newPrice != oldPrice)

			if newPrice != order.Price {
				order.Price = newPrice
				log.Printf("🔧 [价格调整] 价格已更新: %.8f -> %.8f", oldPrice, newPrice)
				log.Printf("🔄 [重试详情] ===== 第 %d 次重试结束，准备第 %d 次重试 =====", attempt+1, attempt+2)
				continue // 重试
			} else {
				log.Printf("❌ [价格调整] 无法进一步调整价格，返回错误")
				log.Printf("❌ [价格调整] 调整失败原因: newPrice等于oldPrice")
				log.Printf("🔄 [重试详情] ===== 第 %d 次重试失败，无法继续 =====", attempt+1)
				break
			}
		}

		// 非价格错误或已达到最大重试次数
		log.Printf("❌ [重试终止] 订单执行失败: %v", err)
		log.Printf("❌ [重试终止] 失败原因: 非价格错误或已达到最大重试次数")
		log.Printf("❌ [重试终止] 总重试次数: %d/%d", attempt+1, maxRetries)
		log.Printf("🔄 [重试详情] ===== 重试机制失败终止 =====")
		return err
	}

	log.Printf("❌ [重试耗尽] 所有重试尝试均失败")
	log.Printf("❌ [重试耗尽] 最大重试次数: %d", maxRetries)
	log.Printf("❌ [重试耗尽] 最终错误: order failed after %d attempts", maxRetries)
	return fmt.Errorf("order failed after %d attempts", maxRetries)
}

// adjustPriceForRetry 为重试调整价格（逐步增加偏差）
func (t *HyperliquidTrader) adjustPriceForRetry(coin string, currentPrice float64, attempt int, isBuy bool) float64 {
	// 基础偏差，每次重试增加
	var additionalDeviation float64
	switch attempt {
	case 1:
		additionalDeviation = 0.01 // 1%
	case 2:
		additionalDeviation = 0.02 // 2%
	case 3:
		additionalDeviation = 0.03 // 3%
	default:
		additionalDeviation = 0.04 // 4%
	}

	var adjustedPrice float64
	if isBuy {
		// 买单：提高价格以增加成交概率
		adjustedPrice = currentPrice * (1.0 + additionalDeviation)
		log.Printf("🔧 [HIP-3] 买单价格调整: %.8f * %.3f -> %.8f", currentPrice, 1.0+additionalDeviation, adjustedPrice)
	} else {
		// 卖单：降低价格以增加成交概率
		adjustedPrice = currentPrice * (1.0 - additionalDeviation)
		log.Printf("🔧 [HIP-3] 卖单价格调整: %.8f * %.3f -> %.8f", currentPrice, 1.0-additionalDeviation, adjustedPrice)
	}

	// 使用 roundPriceForCoin 统一处理（缺省回退 5 位有效数字）
	result := t.roundPriceForCoin(coin, adjustedPrice, false)

	log.Printf("📐 [HIP-3] 重试价格舍入: %.8f -> %.8f", adjustedPrice, result)
	return result
}

// diagnosePriceIssues 价格处理诊断工具
func (t *HyperliquidTrader) diagnosePriceIssues(symbol string) error {
	log.Printf("🧪 [HIP-3] 开始价格处理诊断: %s", symbol)

	// Test symbol resolution
	coin, err := t.resolveCoin(symbol, "")
	if err != nil {
		log.Printf("❌ 符号解析失败: %s -> %v", symbol, err)
		return fmt.Errorf("symbol resolution failed: %w", err)
	}
	log.Printf("✅ 符号解析: %s -> %s", symbol, coin)

	// Test asset type detection
	isStock := t.isStockAsset(coin)
	log.Printf("🔍 资产类型检测: %s -> 股票=%t", coin, isStock)

	// Test price fetching from multiple sources
	prices := make(map[string]float64)

	// Test allMids (WS 优先)
	if price, err := t.getMidPrice(coin); err == nil {
		prices["AllMids"] = price
		log.Printf("📊 AllMids价格: %s = %.6f", coin, price)
	} else {
		log.Printf("⚠️ AllMids 获取失败: %v", err)
	}

	// Test recentTrades (for stocks)
	if isStock {
		if price, err := t.fetchPriceFromRecentTrades(coin); err == nil {
			prices["recentTrades"] = price
			log.Printf("📈 recentTrades价格: %s = %.6f", coin, price)
		} else {
			log.Printf("⚠️ recentTrades API失败: %v", err)
		}
	}

	if len(prices) == 0 {
		return fmt.Errorf("所有价格源都失败 for %s", coin)
	}

	// Test precision handling
	for source, price := range prices {
		roundedPrice := t.roundPriceForCoin(coin, price, false)
		log.Printf("📐 [%s] 价格舍入: %.6f -> %.6f", source, price, roundedPrice)
	}

	// Test precision detection
	if pxDec, ok := t.getPxDecimals(coin); ok {
		log.Printf("🎯 价格精度: %d 位小数", pxDec)
	} else {
		log.Printf("📊 价格精度: 未找到，使用默认策略")
	}

	log.Printf("✅ [HIP-3] 价格处理诊断完成")
	return nil
}

// convertSymbolToHyperliquid 将标准symbol转换为Hyperliquid格式
// 例如: "BTCUSDT" -> "BTC"
func convertSymbolToHyperliquid(symbol string) string {
	// 去掉USDT后缀
	if len(symbol) > 4 && symbol[len(symbol)-4:] == "USDT" {
		return symbol[:len(symbol)-4]
	}
	return symbol
}

// convertSymbolFromHyperliquid 将Hyperliquid格式转换回标准symbol
// 例如: "BTC" -> "BTCUSDT", "xyz:TSLA" -> "xyz:TSLA"
func convertSymbolFromHyperliquid(coin string) string {
	// 对于HIP-3股票资产，直接返回
	if strings.Contains(coin, ":") {
		return coin
	}

	// 对于加密货币，添加USDT后缀
	return coin + "USDT"
}

// resolveCoin 支持HIP-3股票符号（带冒号），优先直接匹配，否则尝试通过AllMids匹配后缀
// assetType: "crypto" 或其它（非空优先使用）；如果为空，按符号形态推断
func (t *HyperliquidTrader) resolveCoin(symbol string, assetType string) (string, error) {
	// 先处理显式符号
	coin := convertSymbolToHyperliquid(symbol)
	if strings.Contains(coin, ":") {
		// 预先构建 assetId 映射，防止 SDK 将未知资产映射到 0(BTC)
		norm := normalizeHip3Symbol(coin)
		var ensureErr error
		if _, ok := t.assetMap[norm]; !ok {
			ensureErr = t.ensureAssetMap()
			if ensureErr != nil {
				log.Printf("⚠️ 无法更新资产映射: %v", ensureErr)
			}
		}
		if _, ok := t.assetMap[norm]; !ok {
			if ensureErr != nil {
				return "", fmt.Errorf("初始化 HIP-3 资产映射失败(%s): %w", coin, ensureErr)
			}
			return "", fmt.Errorf("HIP-3 资产映射缺失: %s", coin)
		}
		return coin, nil
	}

	loweredAssetType := strings.ToLower(strings.TrimSpace(assetType))
	// crypto 类型：仅走 AllMids / 直接匹配，不做 HIP-3 映射
	if loweredAssetType == "crypto" || loweredAssetType == "" {
		allMids, err := t.getAllMidsMap()
		if err != nil {
			return "", fmt.Errorf("获取交易对列表失败: %w", err)
		}

		if _, ok := allMids[coin]; ok {
			return coin, nil
		}
		for k := range allMids {
			if strings.EqualFold(k, coin) {
				return k, nil
			}
		}

		// crypto 未匹配到时，尝试通过 allPerpMetas 解析 HIP-3 / 其它 dex 资产（如 hyna:LIGHTER）
		if coinFromInfo, err := t.resolveFromInfoAPI(coin, false); err == nil && coinFromInfo != "" {
			log.Printf("🔄 InfoAPI 匹配到扩展资产: %s (请求符号: %s)", coinFromInfo, symbol)
			return coinFromInfo, nil
		}

		return "", fmt.Errorf("未找到交易对: %s", symbol)
	}

	// 查询所有mid价格以获取有效交易对列表
	allMids, err := t.getAllMidsMap()
	if err != nil {
		return "", fmt.Errorf("获取交易对列表失败: %w", err)
	}

	// 直接匹配（如纯币种）
	if _, ok := allMids[coin]; ok {
		return coin, nil
	}
	// 大小写不敏感匹配完整键
	for k := range allMids {
		if strings.EqualFold(k, coin) {
			return k, nil
		}
	}

	// 若纯币种未命中，再尝试 InfoAPI 做 HIP-3 匹配（股票等非加密资产）
	if coinFromInfo, err := t.resolveFromInfoAPI(coin, false); err == nil && coinFromInfo != "" {
		log.Printf("🔄 InfoAPI 匹配到资产: %s (请求符号: %s)", coinFromInfo, symbol)
		return coinFromInfo, nil
	} else if err != nil {
		log.Printf("⚠️ InfoAPI 匹配失败: %v", err)
	}

	// 尝试匹配带前缀的HIP-3资产（形如 xyz:TSLA）
	for k := range allMids {
		if !strings.Contains(k, ":") {
			continue
		}
		parts := strings.SplitN(k, ":", 2)
		if len(parts) != 2 {
			continue
		}
		if strings.EqualFold(parts[1], coin) {
			return k, nil
		}
	}

	// 兜底：使用缓存的Meta.Universe做HIP-3股票匹配（AllMids可能未包含新股票）
	if t.meta != nil {
		for _, asset := range t.meta.Universe {
			name := asset.Name
			if !strings.Contains(name, ":") {
				continue
			}
			parts := strings.SplitN(name, ":", 2)
			if len(parts) != 2 {
				continue
			}
			if strings.EqualFold(parts[1], coin) {
				log.Printf("🔄 使用Meta.Universe匹配到HIP-3股票: %s (请求符号: %s)", name, symbol)
				return name, nil
			}
		}
	}

	// 再次尝试：刷新Meta后重试（防止启动后新增股票或Meta为空）
	if t.exchange != nil {
		if refreshedMeta, err := t.exchange.Info().Meta(t.ctx); err == nil && refreshedMeta != nil {
			t.meta = refreshedMeta
			for _, asset := range refreshedMeta.Universe {
				name := asset.Name
				if !strings.Contains(name, ":") {
					continue
				}
				parts := strings.SplitN(name, ":", 2)
				if len(parts) != 2 {
					continue
				}
				if strings.EqualFold(parts[1], coin) {
					log.Printf("🔄 Meta刷新后匹配到HIP-3股票: %s (请求符号: %s)", name, symbol)
					return name, nil
				}
			}
		} else if err != nil {
			log.Printf("⚠️ 刷新Meta失败: %v", err)
		}
	}

	// 使用 Info API 直接拉取 allPerpMetas 兜底（与 /stocks 列表一致，支持 HIP-3 股票）
	if coinFromInfo, err := t.resolveFromInfoAPI(coin, false); err == nil && coinFromInfo != "" {
		log.Printf("🔄 InfoAPI 匹配到HIP-3股票: %s (请求符号: %s)", coinFromInfo, symbol)
		return coinFromInfo, nil
	} else if err != nil {
		log.Printf("⚠️ InfoAPI 匹配失败: %v", err)
	}

	return "", fmt.Errorf("未找到交易对: %s", symbol)
}

// ensureAssetMap 构建 HIP-3 股票的 assetId 映射，避免 SDK 将未知资产映射到 0 (BTC)
func (t *HyperliquidTrader) ensureAssetMap() error {
	if t.exchange == nil {
		return fmt.Errorf("exchange 未初始化")
	}

	// 始终重建映射，避免进程长时间运行后使用了旧公式
	t.assetMap = make(map[string]int)

	// 使用 allPerpMetas 获取全量资产列表（包含 HIP-3 股票），并按官方规则推导 assetId
	payload := []byte(`{"type":"allPerpMetas"}`)
	req, err := http.NewRequest("POST", infoAPIURL(t.testnet), bytes.NewBuffer(payload))
	if err != nil {
		return fmt.Errorf("创建 InfoAPI 请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "NOFX-Hyperliquid-AssetMap")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("调用 InfoAPI 失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("读取 InfoAPI 响应失败: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("InfoAPI 返回错误状态码 %d: %s", resp.StatusCode, string(body))
	}

	var metas []PerpMetaLite
	if err := json.Unmarshal(body, &metas); err != nil {
		return fmt.Errorf("解析 InfoAPI 响应失败: %w", err)
	}

	// 重置 HIP-3 meta 缓存（仅针对带冒号资产）
	t.hip3Meta = make(map[string]PerpMetaAssetLite)

	hipCount := 0
	for dexIdx, meta := range metas {
		// 官方/社区通用映射：assetId = 100000 + dexIdx*10000 + idx
		base := 100000 + dexIdx*10000
		for idx, asset := range meta.Universe {
			name := normalizeHip3Symbol(asset.Name)
			if !strings.Contains(name, ":") {
				continue // 仅处理 HIP-3 / 股票、商品等带前缀资产
			}
			assetId := base + idx
			t.assetMap[name] = assetId
			t.hip3Meta[name] = asset
			hipCount++
		}
		log.Printf("🔍 dex %d 资产数=%d，基准=%d", dexIdx, len(meta.Universe), base)
	}

	if hipCount == 0 {
		return fmt.Errorf("allPerpMetas 未返回任何 HIP-3 资产")
	}

	log.Printf("✅ 构建资产映射完成: HIP-3 资产 %d 个，公式 assetId=100000 + dexIdx*10000 + idx", hipCount)

	// 同步到 SDK
	return t.applyAssetMapToSDK()
}

// applyAssetMapToSDK 将本地 assetMap 同步到 SDK Info 内部的 nameToCoin / coinToAsset 映射
func (t *HyperliquidTrader) applyAssetMapToSDK() error {
	info := t.exchange.Info()
	if info == nil {
		return fmt.Errorf("info 未初始化")
	}

	v := reflect.ValueOf(info).Elem()

	nameToCoinField := v.FieldByName("nameToCoin")
	coinToAssetField := v.FieldByName("coinToAsset")
	assetToDecimalField := v.FieldByName("assetToDecimal")

	if !nameToCoinField.IsValid() || !coinToAssetField.IsValid() || !assetToDecimalField.IsValid() {
		return fmt.Errorf("无法访问 SDK 内部映射")
	}

	nameToCoin := reflect.NewAt(nameToCoinField.Type(), unsafe.Pointer(nameToCoinField.UnsafeAddr())).Elem()
	coinToAsset := reflect.NewAt(coinToAssetField.Type(), unsafe.Pointer(coinToAssetField.UnsafeAddr())).Elem()
	assetToDecimal := reflect.NewAt(assetToDecimalField.Type(), unsafe.Pointer(assetToDecimalField.UnsafeAddr())).Elem()

	for name, assetId := range t.assetMap {
		nameVal := reflect.ValueOf(name)
		assetVal := reflect.ValueOf(assetId)
		nameToCoin.SetMapIndex(nameVal, nameVal)
		coinToAsset.SetMapIndex(nameVal, assetVal)

		// 补充数量精度，默认为 3（稳妥值）；如果从 hip3Meta 已知则使用其 SzDecimals
		szDec := 3
		if asset, ok := t.hip3Meta[name]; ok {
			szDec = asset.SzDecimals
		}
		assetToDecimal.SetMapIndex(assetVal, reflect.ValueOf(szDec))
	}

	return nil
}

// parsePositionSzi 解析持仓数量字符串，增强错误处理
func (t *HyperliquidTrader) parsePositionSzi(szi string) (float64, error) {
	// 去除空格和特殊字符
	szi = strings.TrimSpace(szi)
	if szi == "" || szi == "0" {
		return 0, nil
	}

	// 处理可能的科学计数法
	posAmt, err := strconv.ParseFloat(szi, 64)
	if err != nil {
		log.Printf("⚠️ 解析持仓数量失败: %s, 错误: %v", szi, err)
		return 0, fmt.Errorf("解析持仓数量失败: %w", err)
	}

	return posAmt, nil
}

// absFloat 返回浮点数的绝对值
func absFloat(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// roundToDecimalsLocal 截断/四舍五入到指定位数（四舍五入）
func roundToDecimalsLocal(v float64, decimals int) float64 {
	m := math.Pow10(decimals)
	return math.Round(v*m) / m
}

// classifyOrderByPriceHeuristic 基于价格启发式判断订单类型
// 当其他方法无法确定时，使用触发价格与当前价格的相对关系来判断
func (t *HyperliquidTrader) classifyOrderByPriceHeuristic(ord hyperliquid.FrontendOpenOrder, positionSide string, triggerPx float64) string {
	// 获取当前市场价格
	allMids, err := t.getAllMidsMap()
	if err != nil {
		log.Printf("⚠️ 获取市场价格失败，无法进行启发式判断: %v", err)
		return ""
	}

	priceStr, ok := allMids[ord.Coin]
	if !ok {
		log.Printf("⚠️ 找不到币种 %s 的市场价格", ord.Coin)
		return ""
	}

	currentPx, err := strconv.ParseFloat(priceStr, 64)
	if err != nil {
		log.Printf("⚠️ 解析市场价格失败: %v", err)
		return ""
	}

	// 基于持仓方向和价格相对关系判断
	switch strings.ToUpper(positionSide) {
	case "LONG":
		if triggerPx < currentPx {
			log.Printf("📊 启发式判断: 多头订单 触发价(%.4f) < 当前价(%.4f) = 止损", triggerPx, currentPx)
			return "sl"
		} else if triggerPx > currentPx {
			log.Printf("📊 启发式判断: 多头订单 触发价(%.4f) > 当前价(%.4f) = 止盈", triggerPx, currentPx)
			return "tp"
		}
	case "SHORT":
		if triggerPx > currentPx {
			log.Printf("📊 启发式判断: 空头订单 触发价(%.4f) > 当前价(%.4f) = 止损", triggerPx, currentPx)
			return "sl"
		} else if triggerPx < currentPx {
			log.Printf("📊 启发式判断: 空头订单 触发价(%.4f) < 当前价(%.4f) = 止盈", triggerPx, currentPx)
			return "tp"
		}
	}

	// 价格等于当前价格，无法判断
	log.Printf("⚠️ 启发式判断失败: 触发价(%.4f) == 当前价(%.4f)，无法区分止盈止损", triggerPx, currentPx)
	return ""
}

// hijackNameToAsset 覆盖 SDK 的 NameToAsset，优先使用本地 assetMap 解决 HIP-3 映射缺失问题
func (t *HyperliquidTrader) hijackNameToAsset() {
	// 已通过 applyAssetMapToSDK 写入 SDK 内部 map，不再需要显式覆盖方法
}
