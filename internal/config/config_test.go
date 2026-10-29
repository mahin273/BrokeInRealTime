package config

import (
	"math"
	"testing"
)

func TestDefaultConfigCostCalculation(t *testing.T) {
	cfg := DefaultConfig()

	// Test Gemini 3.8 Flash:
	// input: 1M tokens ($0.15)
	// cache: 1M tokens ($0.0375)
	// output: 1M tokens ($0.60)
	// total = 0.15 + 0.0375 + 0.60 = 0.7875
	// rawCost = 2M * 0.15 + 1M * 0.60 = 0.30 + 0.60 = 0.90
	// savings = 0.90 - 0.7875 = 0.1125
	totalCost, rawCost, savings := cfg.CalculateCost("gemini-3.8-flash", 1000000, 1000000, 1000000)

	expectedTotal := 0.7875
	expectedRaw := 0.90
	expectedSavings := 0.1125

	if math.Abs(totalCost-expectedTotal) > 0.0001 {
		t.Errorf("expected totalCost %f, got %f", expectedTotal, totalCost)
	}
	if math.Abs(rawCost-expectedRaw) > 0.0001 {
		t.Errorf("expected rawCost %f, got %f", expectedRaw, rawCost)
	}
	if math.Abs(savings-expectedSavings) > 0.0001 {
		t.Errorf("expected savings %f, got %f", expectedSavings, savings)
	}
}

func TestTieredPricing(t *testing.T) {
	cfg := DefaultConfig()

	// Test Gemini 3.8 Pro with > 128k prompt tokens
	// 200k input, 0 cache, 10k output
	// rate > 128k: $2.50 / 1M input, $10.00 / 1M output
	// input: (200k/1M)*2.50 = 0.50
	// output: (10k/1M)*10.00 = 0.10
	// total = 0.60
	totalCost, _, _ := cfg.CalculateCost("gemini-3.8-pro", 200000, 0, 10000)

	expectedTotal := 0.60
	if math.Abs(totalCost-expectedTotal) > 0.0001 {
		t.Errorf("expected tiered totalCost %f, got %f", expectedTotal, totalCost)
	}
}
