package main

import "github.com/patlivet/domain-suggestion-engine/internal/llm"

// modelPrice is the paid-tier price in USD per 1M tokens. Thinking tokens are
// billed at the output rate.
type modelPrice struct {
	inputPerM  float64
	outputPerM float64
}

// prices from https://ai.google.dev/gemini-api/docs/pricing (checked 2026-09-18).
// Add a model here before comparing its cost; unlisted models report no cost.
var prices = map[string]modelPrice{
	"gemini-3.1-flash-lite": {inputPerM: 0.25, outputPerM: 1.50},
	"gemini-3.5-flash-lite": {inputPerM: 0.30, outputPerM: 2.50},
}

// estimateCost returns the USD cost of u on model, or ok=false if the model
// has no known price.
func estimateCost(model string, u llm.TokenUsage) (cost float64, ok bool) {
	p, ok := prices[model]
	if !ok {
		return 0, false
	}
	output := float64(u.CandidateTokens + u.ThoughtsTokens)
	return (float64(u.PromptTokens)*p.inputPerM + output*p.outputPerM) / 1_000_000, true
}
