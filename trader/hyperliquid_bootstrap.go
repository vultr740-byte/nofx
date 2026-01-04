package trader

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/sonirico/go-hyperliquid"
	"golang.org/x/sync/singleflight"
)

type hyperliquidBootstrapInfo struct {
	meta          *hyperliquid.Meta
	spotMeta      *hyperliquid.SpotMeta
	dexCollateral map[string]int
	tokenByIndex  map[int]hyperliquid.SpotTokenInfo
}

var hyperliquidBootstrapCache = struct {
	mu        sync.RWMutex
	byTestnet map[bool]*hyperliquidBootstrapInfo
	group     singleflight.Group
}{
	byTestnet: make(map[bool]*hyperliquidBootstrapInfo),
}

func getHyperliquidBootstrapInfo(ctx context.Context, testnet bool) (*hyperliquidBootstrapInfo, error) {
	hyperliquidBootstrapCache.mu.RLock()
	cached := hyperliquidBootstrapCache.byTestnet[testnet]
	hyperliquidBootstrapCache.mu.RUnlock()
	if cached != nil && cached.meta != nil && cached.spotMeta != nil {
		return cached, nil
	}

	key := "mainnet"
	if testnet {
		key = "testnet"
	}

	v, err, _ := hyperliquidBootstrapCache.group.Do(key, func() (any, error) {
		hyperliquidBootstrapCache.mu.RLock()
		cached := hyperliquidBootstrapCache.byTestnet[testnet]
		hyperliquidBootstrapCache.mu.RUnlock()
		if cached != nil && cached.meta != nil && cached.spotMeta != nil {
			return cached, nil
		}

		meta, collateralToken, err := fetchHyperliquidMeta(ctx, testnet)
		if err != nil {
			return nil, err
		}

		spotMeta, err := fetchHyperliquidSpotMeta(ctx, testnet)
		if err != nil {
			return nil, err
		}

		tokenByIndex := make(map[int]hyperliquid.SpotTokenInfo, len(spotMeta.Tokens))
		for _, t := range spotMeta.Tokens {
			tokenByIndex[t.Index] = t
		}

		dexCollateral := map[string]int{"": collateralToken}

		// 打印日志（仅主 dex）
		tokenName := "unknown"
		szDec := 0
		if t, ok := tokenByIndex[collateralToken]; ok {
			tokenName = t.Name
			szDec = t.SzDecimals
		}
		log.Printf("💱 DEX=%s collateralToken index=%d name=%s szDecimals=%d", dexLabel(""), collateralToken, tokenName, szDec)

		info := &hyperliquidBootstrapInfo{
			meta:          meta,
			spotMeta:      spotMeta,
			dexCollateral: dexCollateral,
			tokenByIndex:  tokenByIndex,
		}

		hyperliquidBootstrapCache.mu.Lock()
		hyperliquidBootstrapCache.byTestnet[testnet] = info
		hyperliquidBootstrapCache.mu.Unlock()

		return info, nil
	})
	if err != nil {
		return nil, err
	}
	return v.(*hyperliquidBootstrapInfo), nil
}

var hyperliquidInfoHTTPClient = &http.Client{Timeout: 12 * time.Second}

func fetchHyperliquidMeta(ctx context.Context, testnet bool) (*hyperliquid.Meta, int, error) {
	var lastErr error
	backoff := 600 * time.Millisecond

	for attempt := 0; attempt < 6; attempt++ {
		body, header, status, err := postHyperliquidInfo(ctx, testnet, map[string]any{"type": "meta"})
		if err == nil && status == http.StatusOK {
			meta, err := hyperliquid.ParseMetaResponse(body)
			if err != nil {
				return nil, 0, fmt.Errorf("解析 meta 失败: %w", err)
			}
			collIdx, err := parseHyperliquidCollateralToken(body)
			if err != nil {
				return nil, 0, err
			}
			return meta, collIdx, nil
		}

		if err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf("meta status %d", status)
		}

		sleep := backoff
		if status == http.StatusTooManyRequests {
			sleep = retryAfterOr(header, backoff)
		}

		if err := sleepWithContext(ctx, sleep); err != nil {
			return nil, 0, err
		}
		if backoff < 6*time.Second {
			backoff *= 2
		}
	}

	return nil, 0, lastErr
}

func fetchHyperliquidSpotMeta(ctx context.Context, testnet bool) (*hyperliquid.SpotMeta, error) {
	var lastErr error
	backoff := 600 * time.Millisecond

	for attempt := 0; attempt < 6; attempt++ {
		body, header, status, err := postHyperliquidInfo(ctx, testnet, map[string]any{"type": "spotMeta"})
		if err == nil && status == http.StatusOK {
			var sm hyperliquid.SpotMeta
			if err := json.Unmarshal(body, &sm); err != nil {
				return nil, fmt.Errorf("解析 spotMeta 失败: %w", err)
			}
			return &sm, nil
		}

		if err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf("spotMeta status %d", status)
		}

		sleep := backoff
		if status == http.StatusTooManyRequests {
			sleep = retryAfterOr(header, backoff)
		}

		if err := sleepWithContext(ctx, sleep); err != nil {
			return nil, err
		}
		if backoff < 6*time.Second {
			backoff *= 2
		}
	}

	return nil, lastErr
}

func postHyperliquidInfo(ctx context.Context, testnet bool, payload any) ([]byte, http.Header, int, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, infoAPIURL(testnet), bytes.NewBuffer(body))
	if err != nil {
		return nil, nil, 0, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "NOFX-Hyperliquid-Bootstrap")

	resp, err := hyperliquidInfoHTTPClient.Do(req)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.Header, resp.StatusCode, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return respBody, resp.Header, resp.StatusCode, fmt.Errorf("status %d: %s", resp.StatusCode, string(respBody))
	}

	return respBody, resp.Header, resp.StatusCode, nil
}

func parseHyperliquidCollateralToken(body []byte) (int, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return 0, fmt.Errorf("解析 meta(collateralToken) 失败: %w", err)
	}
	b, ok := raw["collateralToken"]
	if !ok {
		return 0, fmt.Errorf("meta 缺少 collateralToken")
	}

	var idx int
	if err := json.Unmarshal(b, &idx); err == nil {
		return idx, nil
	}

	var f float64
	if err := json.Unmarshal(b, &f); err == nil {
		return int(f), nil
	}

	return 0, fmt.Errorf("meta collateralToken 类型未知: %s", string(b))
}

func retryAfterOr(header http.Header, fallback time.Duration) time.Duration {
	if header == nil {
		return fallback
	}
	ra := header.Get("Retry-After")
	if ra == "" {
		return fallback
	}
	sec, err := strconv.Atoi(ra)
	if err != nil || sec <= 0 {
		return fallback
	}
	return time.Duration(sec) * time.Second
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
