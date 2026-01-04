package decision

import (
	"strings"
	"testing"

	"nofx/market"
)

func TestValidateDecisionUsesMarketPriceForRiskReward(t *testing.T) {
	ctx := &Context{
		Account: AccountInfo{
			TotalEquity: 100,
		},
		BTCETHLeverage:  5,
		AltcoinLeverage: 3,
		MarketDataMap: map[string]*market.Data{
			"ETHUSDT": {CurrentPrice: 3550},
		},
	}

	decision := Decision{
		Symbol:          "ETHUSDT",
		Action:          "open_long",
		Leverage:        3,
		PositionSizeUSD: 120,
		StopLoss:        0,    // 缺少止损
		TakeProfit:      3600, // 仅有止盈
	}

	err := validateDecision(&decision, ctx.Account.TotalEquity, ctx.BTCETHLeverage, ctx.AltcoinLeverage, false)
	if err == nil || !strings.Contains(err.Error(), "止损和止盈必须大于0") {
		t.Fatalf("expected missing tp/sl validation error, got %v", err)
	}
}

func TestExtractDecisions_AllowsEmptyArray(t *testing.T) {
	resp := "<reasoning>\n- no-op\n</reasoning>\n\n<decision>\n```json\n[]\n```\n</decision>"
	decisions, err := extractDecisions(resp)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if len(decisions) != 0 {
		t.Fatalf("expected 0 decisions, got %d", len(decisions))
	}
}

func TestParseFullDecisionResponse_AllowsEmptyArray(t *testing.T) {
	resp := "<reasoning>\n- no-op\n</reasoning>\n\n<decision>\n```json\n[]\n```\n</decision>"
	decision, err := parseFullDecisionResponse(resp, 100, 5, 3, false)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if decision == nil {
		t.Fatalf("expected decision, got nil")
	}
	if len(decision.Decisions) != 0 {
		t.Fatalf("expected 0 decisions, got %d", len(decision.Decisions))
	}
}
