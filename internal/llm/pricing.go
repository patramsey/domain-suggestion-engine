package llm

// modelPrice is the paid-tier price in USD per 1M tokens. Thinking tokens are
// billed at the output rate.
type modelPrice struct {
	inputPerM  float64
	outputPerM float64
}

// prices from https://ai.google.dev/gemini-api/docs/pricing (checked 2026-09-24).
// Add a model here before running it; an unlisted model reports no cost.
//
// The 3.6-3.8 Flash rates below are promotional through 2026-12-31 and double
// on 2027-01-01 ($1.50 in / $7.50 out); update them then. The 3.1 Flash-Lite
// input rate is for text, image and video; audio input costs $0.50.
var prices = map[string]modelPrice{
	"gemini-3.1-flash-lite": {inputPerM: 0.25, outputPerM: 1.50},
	"gemini-3.5-flash-lite": {inputPerM: 0.30, outputPerM: 2.50},
	"gemini-3.5-flash":      {inputPerM: 1.50, outputPerM: 9.00},
	"gemini-3.6-flash":      {inputPerM: 0.75, outputPerM: 3.75},
	"gemini-3.7-flash":      {inputPerM: 0.75, outputPerM: 3.75},
	"gemini-3.8-flash":      {inputPerM: 0.75, outputPerM: 3.75},
}

// IsPriced reports whether model has a known price.
func IsPriced(model string) bool {
	_, ok := prices[model]
	return ok
}

// EstimateCost returns the USD cost of u on model, or ok=false when the model
// has no known price. Thinking tokens are billed as output.
func EstimateCost(model string, u TokenUsage) (cost float64, ok bool) {
	p, ok := prices[model]
	if !ok {
		return 0, false
	}
	output := float64(u.CandidateTokens + u.ThoughtsTokens)
	return (float64(u.PromptTokens)*p.inputPerM + output*p.outputPerM) / 1_000_000, true
}
