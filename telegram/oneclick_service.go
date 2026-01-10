package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

type OneClickService struct {
	baseURL string
	jwt     string
	client  *http.Client

	mu          sync.Mutex
	tokens      []oneClickToken
	tokensAt    time.Time
	tokensTTL   time.Duration
	symbolIndex map[string][]oneClickToken
}

type oneClickToken struct {
	AssetID         string  `json:"assetId"`
	Decimals        int     `json:"decimals"`
	Blockchain      string  `json:"blockchain"`
	Symbol          string  `json:"symbol"`
	Price           float64 `json:"price"`
	PriceUpdatedAt  string  `json:"priceUpdatedAt"`
	ContractAddress string  `json:"contractAddress"`
}

type oneClickErrorResponse struct {
	Message       string `json:"message"`
	CorrelationID string `json:"correlationId"`
	Timestamp     string `json:"timestamp"`
	Path          string `json:"path"`
}

type oneClickQuoteRequest struct {
	Dry               bool   `json:"dry"`
	DepositMode       string `json:"depositMode,omitempty"`
	SwapType          string `json:"swapType"`
	SlippageTolerance int    `json:"slippageTolerance"`
	OriginAsset       string `json:"originAsset"`
	DepositType       string `json:"depositType"`
	DestinationAsset  string `json:"destinationAsset"`
	Amount            string `json:"amount"`
	RefundTo          string `json:"refundTo"`
	RefundType        string `json:"refundType"`
	Recipient         string `json:"recipient"`
	RecipientType     string `json:"recipientType"`
	Deadline          string `json:"deadline"`
	QuoteWaitingTime  int    `json:"quoteWaitingTimeMs,omitempty"`
}

type oneClickQuote struct {
	DepositAddress   string `json:"depositAddress,omitempty"`
	DepositMemo      string `json:"depositMemo,omitempty"`
	AmountIn         string `json:"amountIn"`
	AmountInFmt      string `json:"amountInFormatted"`
	AmountInUsd      string `json:"amountInUsd"`
	MinAmountIn      string `json:"minAmountIn"`
	AmountOut        string `json:"amountOut"`
	AmountOutFmt     string `json:"amountOutFormatted"`
	AmountOutUsd     string `json:"amountOutUsd"`
	MinAmountOut     string `json:"minAmountOut"`
	TimeEstimateSec  int    `json:"timeEstimate"`
	Deadline         string `json:"deadline,omitempty"`
	TimeWhenInactive string `json:"timeWhenInactive,omitempty"`
}

type oneClickQuoteResponse struct {
	CorrelationID string               `json:"correlationId"`
	Timestamp     string               `json:"timestamp"`
	Signature     string               `json:"signature"`
	QuoteRequest  oneClickQuoteRequest `json:"quoteRequest"`
	Quote         oneClickQuote        `json:"quote"`
}

type oneClickStatusResponse struct {
	CorrelationID string                `json:"correlationId"`
	Status        string                `json:"status"`
	UpdatedAt     string                `json:"updatedAt"`
	QuoteResponse oneClickQuoteResponse `json:"quoteResponse"`
	SwapDetails   oneClickSwapDetails   `json:"swapDetails"`
}

type oneClickSwapDetails struct {
	DepositedAmount          string `json:"depositedAmount"`
	DepositedAmountFormatted string `json:"depositedAmountFormatted"`
	AmountOut                string `json:"amountOut"`
	AmountOutFormatted       string `json:"amountOutFormatted"`
	RefundedAmount           string `json:"refundedAmount"`
	RefundedAmountFormatted  string `json:"refundedAmountFormatted"`
}

func NewOneClickService(baseURL string, jwt string) *OneClickService {
	u := strings.TrimSpace(baseURL)
	if u == "" {
		u = "https://1click.chaindefuser.com"
	}
	u = strings.TrimRight(u, "/")

	return &OneClickService{
		baseURL: u,
		jwt:     strings.TrimSpace(jwt),
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
		tokensTTL: 10 * time.Minute,
	}
}

func (s *OneClickService) GetTokens(ctx context.Context) ([]oneClickToken, error) {
	s.mu.Lock()
	if time.Since(s.tokensAt) < s.tokensTTL && len(s.tokens) > 0 {
		toks := make([]oneClickToken, len(s.tokens))
		copy(toks, s.tokens)
		s.mu.Unlock()
		return toks, nil
	}
	s.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL+"/v0/tokens", nil)
	if err != nil {
		return nil, fmt.Errorf("创建 1Click tokens 请求失败: %w", err)
	}
	s.applyAuth(req)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 1Click tokens 失败: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var apiErr oneClickErrorResponse
		_ = json.Unmarshal(body, &apiErr)
		if apiErr.Message != "" {
			return nil, fmt.Errorf("1Click tokens API error: %s", apiErr.Message)
		}
		return nil, fmt.Errorf("1Click tokens HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var tokens []oneClickToken
	if err := json.Unmarshal(body, &tokens); err != nil {
		return nil, fmt.Errorf("解析 1Click tokens 失败: %w", err)
	}

	s.mu.Lock()
	s.tokens = tokens
	s.tokensAt = time.Now()
	s.symbolIndex = buildSymbolIndex(tokens)
	s.mu.Unlock()

	return tokens, nil
}

func buildSymbolIndex(tokens []oneClickToken) map[string][]oneClickToken {
	idx := make(map[string][]oneClickToken)
	for _, t := range tokens {
		sym := strings.ToUpper(strings.TrimSpace(t.Symbol))
		if sym == "" {
			continue
		}
		idx[sym] = append(idx[sym], t)
	}
	return idx
}

func (s *OneClickService) GetUSDCTokensByChain(ctx context.Context) (map[string]oneClickToken, error) {
	_, err := s.GetTokens(ctx)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var usdc []oneClickToken
	if s.symbolIndex != nil {
		usdc = s.symbolIndex["USDC"]
	}
	out := make(map[string]oneClickToken)
	for _, tok := range usdc {
		chain := strings.TrimSpace(tok.Blockchain)
		if chain == "" {
			continue
		}
		// 如果同链出现多个 USDC，先取第一个（后续可扩展为让用户选择具体合约）
		if _, exists := out[chain]; !exists {
			out[chain] = tok
		}
	}
	return out, nil
}

func (s *OneClickService) ListUSDCSupportedChains(ctx context.Context) ([]string, error) {
	m, err := s.GetUSDCTokensByChain(ctx)
	if err != nil {
		return nil, err
	}
	chains := make([]string, 0, len(m))
	for chain := range m {
		chains = append(chains, chain)
	}
	sort.Strings(chains)
	return chains, nil
}

func (s *OneClickService) RequestQuote(ctx context.Context, req oneClickQuoteRequest) (*oneClickQuoteResponse, error) {
	// 自动处理 depositMode：先按请求（或默认 SIMPLE），如提示不匹配则改为 MEMO 重试一次。
	resp, err := s.requestQuoteOnce(ctx, req)
	if err == nil {
		return resp, nil
	}

	// 仅对 depositMode 错误做一次自动重试
	if strings.Contains(err.Error(), "Incorrect depositMode") && (req.DepositMode == "" || strings.EqualFold(req.DepositMode, "SIMPLE")) {
		req.DepositMode = "MEMO"
		return s.requestQuoteOnce(ctx, req)
	}

	return nil, err
}

func (s *OneClickService) requestQuoteOnce(ctx context.Context, req oneClickQuoteRequest) (*oneClickQuoteResponse, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("序列化 1Click quote 请求失败: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/v0/quote", bytes.NewBuffer(payload))
	if err != nil {
		return nil, fmt.Errorf("创建 1Click quote 请求失败: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	s.applyAuth(httpReq)

	httpResp, err := s.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("请求 1Click quote 失败: %w", err)
	}
	defer httpResp.Body.Close()

	body, _ := io.ReadAll(httpResp.Body)
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		var apiErr oneClickErrorResponse
		_ = json.Unmarshal(body, &apiErr)
		if apiErr.Message != "" {
			return nil, fmt.Errorf("1Click quote API error: %s", apiErr.Message)
		}
		return nil, fmt.Errorf("1Click quote HTTP %d: %s", httpResp.StatusCode, strings.TrimSpace(string(body)))
	}

	var out oneClickQuoteResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("解析 1Click quote 响应失败: %w", err)
	}
	return &out, nil
}

func (s *OneClickService) GetStatus(ctx context.Context, depositAddress string, depositMemo string) (*oneClickStatusResponse, error) {
	u, err := url.Parse(s.baseURL + "/v0/status")
	if err != nil {
		return nil, fmt.Errorf("构造 1Click status URL 失败: %w", err)
	}
	q := u.Query()
	q.Set("depositAddress", strings.TrimSpace(depositAddress))
	if strings.TrimSpace(depositMemo) != "" {
		q.Set("depositMemo", strings.TrimSpace(depositMemo))
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("创建 1Click status 请求失败: %w", err)
	}
	s.applyAuth(req)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 1Click status 失败: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var apiErr oneClickErrorResponse
		_ = json.Unmarshal(body, &apiErr)
		if apiErr.Message != "" {
			return nil, fmt.Errorf("1Click status API error: %s", apiErr.Message)
		}
		return nil, fmt.Errorf("1Click status HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var out oneClickStatusResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("解析 1Click status 响应失败: %w", err)
	}
	return &out, nil
}

func (s *OneClickService) applyAuth(req *http.Request) {
	if strings.TrimSpace(s.jwt) == "" {
		return
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(s.jwt))
}
