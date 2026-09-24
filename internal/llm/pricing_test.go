package llm

import (
	"math"
	"testing"
)

func TestEstimateCostBillsThoughtsAsOutput(t *testing.T) {
	u := TokenUsage{PromptTokens: 1_000_000, CandidateTokens: 500_000, ThoughtsTokens: 500_000}
	cost, ok := EstimateCost("gemini-3.1-flash-lite", u)
	if !ok {
		t.Fatal("gemini-3.1-flash-lite should be priced")
	}
	if math.Abs(cost-1.75) > 1e-9 { // $0.25 input + 1M output × $1.50
		t.Errorf("cost = %v, want 1.75", cost)
	}
}

func TestEstimateCost35(t *testing.T) {
	cost, ok := EstimateCost("gemini-3.5-flash-lite", TokenUsage{PromptTokens: 1_000_000, CandidateTokens: 1_000_000})
	if !ok || math.Abs(cost-2.80) > 1e-9 {
		t.Errorf("cost = %v ok = %v, want 2.80", cost, ok)
	}
}

func TestEstimateCostUnknownModel(t *testing.T) {
	if _, ok := EstimateCost("gemini-9-ultra", TokenUsage{PromptTokens: 10}); ok {
		t.Error("unknown model should not be priced")
	}
	if IsPriced("gemini-9-ultra") {
		t.Error("IsPriced should be false for an unknown model")
	}
}

func TestEstimateCostFlashModels(t *testing.T) {
	// 1M in + 1M out: 3.5 Flash is the dearest, 3.8 Flash the cheapest of the
	// full Flash models while its promotional rate lasts (see pricing.go).
	u := TokenUsage{PromptTokens: 1_000_000, CandidateTokens: 1_000_000}
	for model, want := range map[string]float64{
		"gemini-3.5-flash": 10.50,
		"gemini-3.8-flash": 4.50,
	} {
		got, ok := EstimateCost(model, u)
		if !ok || math.Abs(got-want) > 1e-9 {
			t.Errorf("%s: cost = %v ok = %v, want %v", model, got, ok, want)
		}
	}
}
