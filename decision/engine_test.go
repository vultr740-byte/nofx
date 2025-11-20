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
		StopLoss:        3400,
		TakeProfit:      3600,
	}

	err := validateDecision(&decision, ctx)
	if err == nil || !strings.Contains(err.Error(), "风险回报比过低") {
		t.Fatalf("expected risk ratio validation error, got %v", err)
	}
}
