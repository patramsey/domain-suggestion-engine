package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/patlivet/domain-suggestion-engine/internal/algorithmic"
	"github.com/patlivet/domain-suggestion-engine/internal/wordlist"
)

const geminiBase = "https://generativelanguage.googleapis.com/v1beta/models/"

// rateLimitError is returned by call() when Gemini responds with HTTP 429.
type rateLimitError struct{ msg string }

func (e *rateLimitError) Error() string { return e.msg }

// Client calls the Gemini API to generate domain name suggestions.
type Client struct {
	apiKey      string
	model       string
	httpClient  *http.Client
	Temperature float64 // generation temperature; defaults to 0.9 if zero
	// ThinkingLevel is the Gemini thinkingLevel ("minimal", "low", "medium",
	// "high"); defaults to "minimal" if empty.
	ThinkingLevel string
	// Variants defines the active creative variants to run in parallel.
	// Defaults to [VariantEvocative, VariantWordplay, VariantCrafted] if nil/empty.
	Variants []Variant
}

// NewClient creates a Gemini client. model is the full model ID (e.g. "gemini-3.5-flash-lite").
// The HTTP client has no timeout — callers pass a context with the desired deadline.
func NewClient(apiKey, model string) *Client {
	return &Client{
		apiKey:      apiKey,
		model:       model,
		httpClient:  &http.Client{},
		Temperature: 1.0,
	}
}

func (c *Client) endpoint() string {
	return geminiBase + c.model + ":generateContent"
}

func (c *Client) temperature() float64 {
	if c.Temperature == 0 {
		return 0.9
	}
	return c.Temperature
}

func (c *Client) thinkingLevel() string {
	if c.ThinkingLevel == "" {
		return "minimal"
	}
	return c.ThinkingLevel
}

func (c *Client) variants() []Variant {
	if len(c.Variants) > 0 {
		return c.Variants
	}
	return llmVariants
}

// EvalGenerate runs the full generation pipeline with a custom system prompt.
// variantOverrides optionally replaces the per-variant instructions; must be
// len(c.variants()) if non-nil. Intended for prompt experimentation only.
// The Funnel reports how the raw responses were filtered down to candidates.
func (c *Client) EvalGenerate(ctx context.Context, customSystem string, rawInput string, tokens []string, allTLDs []string, tldSet map[string]struct{}, count int, variantOverrides []string) ([]algorithmic.Candidate, TokenUsage, Funnel, error) {
	activeVars := c.variants()
	variantCount := int(math.Ceil(float64(count) / float64(len(activeVars))))

	type vResult struct {
		r   callResult
		err error
	}

	results := make([]vResult, len(activeVars))
	var wg sync.WaitGroup

	for i, v := range activeVars {
		wg.Add(1)
		go func(i int, v Variant) {
			defer wg.Done()
			_, user := BuildRequest(rawInput, tokens, allTLDs, variantCount, nil, nil)
			if len(variantOverrides) == len(activeVars) {
				user += variantOverrides[i]
			} else {
				user += variantInstruction(v)
			}
			r, err := c.callWithRetry(ctx, customSystem, user)
			results[i] = vResult{r, err}
		}(i, v)
	}

	wg.Wait()

	var perVariant [][]rawPair
	var totalUsage TokenUsage
	var calls Funnel
	for _, res := range results {
		totalUsage = totalUsage.add(res.r.usage)
		calls.Requested += overRequest(variantCount)
		if res.err != nil {
			calls.FailedCalls++
			continue
		}
		perVariant = append(perVariant, res.r.pairs)
		if res.r.partial {
			calls.PartialParses++
		}
		if res.r.maxTokens {
			calls.MaxTokenStops++
		}
	}
	cands, funnel := rankedCandidatesWithFunnel(perVariant, tldSet)
	funnel = funnel.Add(calls)
	if len(perVariant) == 0 {
		return nil, totalUsage, funnel, fmt.Errorf("all llm variants failed")
	}
	return cands, totalUsage, funnel, nil
}

// TokenUsage holds token counts from a Gemini API call (or the sum across retries).
type TokenUsage struct {
	PromptTokens    int
	CandidateTokens int
	ThoughtsTokens  int // billed as output; not included in CandidateTokens
	TotalTokens     int
}

func (a TokenUsage) add(b TokenUsage) TokenUsage {
	return TokenUsage{
		PromptTokens:    a.PromptTokens + b.PromptTokens,
		CandidateTokens: a.CandidateTokens + b.CandidateTokens,
		ThoughtsTokens:  a.ThoughtsTokens + b.ThoughtsTokens,
		TotalTokens:     a.TotalTokens + b.TotalTokens,
	}
}

var llmVariants = []Variant{VariantEvocative, VariantWordplay, VariantCrafted}

// Generate runs parallel LLM calls with different creative variants and merges results.
// If variantOverrides are provided, they take precedence over c.Variants / default variants.
// Each variant requests count/N suggestions so total budget ≈ count×3 (or count if 1 variant).
func (c *Client) Generate(ctx context.Context, rawInput string, tokens []string, tlds []string, tldSet map[string]struct{}, count int, unavailable, inspireFrom []string, variantOverrides ...Variant) ([]algorithmic.Candidate, TokenUsage, error) {
	activeVars := c.variants()
	if len(variantOverrides) > 0 {
		activeVars = variantOverrides
	}
	variantCount := int(math.Ceil(float64(count) / float64(len(activeVars))))

	type variantResult struct {
		pairs []rawPair
		usage TokenUsage
		err   error
	}

	results := make([]variantResult, len(activeVars))
	var wg sync.WaitGroup

	for i, v := range activeVars {
		wg.Add(1)
		go func(i int, v Variant) {
			defer wg.Done()
			system, user := BuildRequest(rawInput, tokens, tlds, variantCount, unavailable, inspireFrom)
			user += variantInstruction(v)
			r, err := c.callWithRetry(ctx, system, user)
			results[i] = variantResult{r.pairs, r.usage, err}
		}(i, v)
	}

	wg.Wait()

	var perVariant [][]rawPair
	var allPairs []rawPair // used only for the >50% invalid check
	var totalUsage TokenUsage
	successCount := 0
	for i, r := range results {
		totalUsage = totalUsage.add(r.usage)
		if r.err != nil {
			slog.Warn("llm variant failed", "variant", activeVars[i].String(), "err", r.err)
			continue
		}
		perVariant = append(perVariant, r.pairs)
		allPairs = append(allPairs, r.pairs...)
		successCount++
	}

	if successCount == 0 {
		return nil, totalUsage, fmt.Errorf("all llm variants failed")
	}

	// retry once if >50% of merged pairs are invalid
	_, invalid := parseAndValidate(allPairs, tldSet)
	if len(allPairs) > 0 && float64(len(invalid))/float64(len(allPairs)) > 0.5 {
		badTLDs := hallucinatedTLDs(invalid, tldSet)
		retrySystem, retryUser := BuildRetryRequest(rawInput, tokens, tlds, count, badTLDs, unavailable, inspireFrom)
		retry, retryErr := c.call(ctx, retrySystem, retryUser)
		totalUsage = totalUsage.add(retry.usage)
		if retryErr == nil {
			retryValid, _ := parseAndValidate(retry.pairs, tldSet)
			if len(retryValid) > len(rankedCandidates(perVariant, tldSet)) {
				perVariant = [][]rawPair{retry.pairs}
			}
		}
	}

	return rankedCandidates(perVariant, tldSet), totalUsage, nil
}

// --- Gemini API types ---

type geminiRequest struct {
	SystemInstruction *geminiContent  `json:"systemInstruction,omitempty"`
	Contents          []geminiContent `json:"contents"`
	GenerationConfig  *genConfig      `json:"generationConfig,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text string `json:"text"`
}

type genConfig struct {
	Temperature      float64      `json:"temperature"`
	ThinkingConfig   *thinkingCfg `json:"thinkingConfig,omitempty"`
	ResponseMIMEType string       `json:"responseMimeType,omitempty"`
}

// thinkingCfg uses thinkingLevel rather than the legacy thinkingBudget:
// gemini-3.5+ rejects thinkingBudget with a 400, and 3.1 accepts both.
type thinkingCfg struct {
	ThinkingLevel string `json:"thinkingLevel"`
}

type geminiUsage struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	ThoughtsTokenCount   int `json:"thoughtsTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

type geminiResponse struct {
	Candidates []struct {
		Content      geminiContent `json:"content"`
		FinishReason string        `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata geminiUsage `json:"usageMetadata"`
}

// callWithRetry wraps call with retry logic:
//   - 429 rate limit: up to 3 attempts with 500ms / 1s backoff
//   - other errors: 1 retry after 100ms
//
// The returned usage is summed across all attempts.
func (c *Client) callWithRetry(ctx context.Context, system, user string) (callResult, error) {
	var totalUsage TokenUsage
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			var delay time.Duration
			if errors.As(lastErr, new(*rateLimitError)) {
				delay = time.Duration(500<<(attempt-1)) * time.Millisecond // 500ms, 1s
			} else {
				if attempt > 1 {
					break // non-429: only one retry
				}
				delay = 100 * time.Millisecond
			}
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return callResult{usage: totalUsage}, ctx.Err()
			}
		}
		r, err := c.call(ctx, system, user)
		totalUsage = totalUsage.add(r.usage)
		if err == nil {
			r.usage = totalUsage
			return r, nil
		}
		if ctx.Err() != nil {
			return callResult{usage: totalUsage}, ctx.Err()
		}
		lastErr = err
		slog.Warn("llm call failed", "attempt", attempt+1, "err", err)
	}
	return callResult{usage: totalUsage}, lastErr
}

// callResult is the outcome of one Gemini call: the parsed pairs, token usage
// and flags describing how cleanly the response came back.
type callResult struct {
	pairs     []rawPair
	usage     TokenUsage
	partial   bool // pairs recovered from malformed/truncated JSON
	maxTokens bool // generation stopped at the output token limit
}

func (c *Client) call(ctx context.Context, system, user string) (callResult, error) {
	req := geminiRequest{
		SystemInstruction: &geminiContent{
			Parts: []geminiPart{{Text: system}},
		},
		Contents: []geminiContent{
			{Role: "user", Parts: []geminiPart{{Text: user}}},
		},
		GenerationConfig: &genConfig{
			Temperature:      c.temperature(),
			ThinkingConfig:   &thinkingCfg{ThinkingLevel: c.thinkingLevel()},
			ResponseMIMEType: "application/json",
		},
	}

	respBody, err := c.postGemini(ctx, req)
	if err != nil {
		return callResult{}, err
	}
	return interpretResponse(respBody)
}

// interpretResponse parses a successful Gemini response body into pairs,
// usage and response-quality flags. On a parse failure the usage is still
// returned so the tokens are accounted for.
func interpretResponse(respBody []byte) (callResult, error) {
	var gemResp geminiResponse
	if err := json.Unmarshal(respBody, &gemResp); err != nil {
		return callResult{}, fmt.Errorf("unmarshal response: %w", err)
	}
	if len(gemResp.Candidates) == 0 {
		return callResult{}, fmt.Errorf("no candidates in response")
	}

	r := callResult{
		usage: TokenUsage{
			PromptTokens:    gemResp.UsageMetadata.PromptTokenCount,
			CandidateTokens: gemResp.UsageMetadata.CandidatesTokenCount,
			ThoughtsTokens:  gemResp.UsageMetadata.ThoughtsTokenCount,
			TotalTokens:     gemResp.UsageMetadata.TotalTokenCount,
		},
		maxTokens: gemResp.Candidates[0].FinishReason == "MAX_TOKENS",
	}

	// collect all text parts (thinking models may split thinking/answer across parts)
	var combined strings.Builder
	for _, part := range gemResp.Candidates[0].Content.Parts {
		if part.Text != "" {
			combined.WriteString(part.Text)
			combined.WriteByte('\n')
		}
	}
	text := combined.String()
	var err error
	r.pairs, r.partial, err = extractJSONWithInfo(text)
	if err != nil {
		slog.Warn("llm response parse failed", "err", err, "response_preview", truncate(text, 300))
	}
	return r, err
}

// rawPair is a parsed (sld, tld) pair from the LLM response.
type rawPair struct {
	SLD string `json:"sld"`
	TLD string `json:"tld"`
}

func extractJSON(text string) ([]rawPair, error) {
	pairs, _, err := extractJSONWithInfo(text)
	return pairs, err
}

// extractJSONWithInfo is extractJSON that also reports whether the pairs had
// to be recovered from a malformed or truncated array.
func extractJSONWithInfo(text string) (pairs []rawPair, partial bool, err error) {
	text = stripFences(text)
	match := findFirstJSONArray(text)
	if match != "" {
		if json.Unmarshal([]byte(match), &pairs) == nil {
			return pairs, false, nil
		}
	}
	// Truncated or malformed array — recover individual valid objects.
	if recovered := extractPartialPairs(text); len(recovered) > 0 {
		slog.Warn("llm response partially parsed", "recovered", len(recovered))
		return recovered, true, nil
	}
	return nil, false, fmt.Errorf("no JSON array found in response")
}

// extractPartialPairs scans text for well-formed {"sld":"...","tld":"..."} objects,
// tolerating truncated or otherwise malformed surrounding JSON.
func extractPartialPairs(text string) []rawPair {
	var pairs []rawPair
	i := 0
	for i < len(text) {
		if text[i] != '{' {
			i++
			continue
		}
		// Walk to the matching closing brace, respecting quoted strings.
		j := i
		depth := 0
		inStr := false
		escaped := false
		for j < len(text) {
			ch := text[j]
			if escaped {
				escaped = false
			} else if ch == '\\' && inStr {
				escaped = true
			} else if ch == '"' {
				inStr = !inStr
			} else if !inStr {
				if ch == '{' {
					depth++
				} else if ch == '}' {
					depth--
					if depth == 0 {
						j++
						break
					}
				}
			}
			j++
		}
		if depth == 0 && j > i {
			var p rawPair
			if json.Unmarshal([]byte(text[i:j]), &p) == nil && p.SLD != "" && p.TLD != "" {
				pairs = append(pairs, p)
			}
		}
		i = j
	}
	return pairs
}

// findFirstJSONArray returns the first bracket-balanced [...] substring.
func findFirstJSONArray(s string) string {
	start := strings.Index(s, "[")
	if start == -1 {
		return ""
	}
	depth := 0
	inStr := false
	escape := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if escape {
			escape = false
			continue
		}
		if c == '\\' && inStr {
			escape = true
			continue
		}
		if c == '"' {
			inStr = !inStr
			continue
		}
		if inStr {
			continue
		}
		if c == '[' {
			depth++
		} else if c == ']' {
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}

var sldRe = regexp.MustCompile(`^[a-z]{3,14}$`)

const vowels = "aeiouy"

// looksLikeTruncation returns true for consonant-heavy strings that are likely
// mid-word fragments ("crea", "agenc", "ind"). Real dictionary words are never
// truncations. 3-char strings are exempt — they're treated as abbreviations
// ("dns", "css", "crm") not truncations. For lengths 4-6 not in the dictionary,
// requires at least 20% vowel ratio and at least one vowel.
func looksLikeTruncation(s string) bool {
	n := len(s)
	if n > 6 || n <= 3 {
		return false
	}
	if _, ok := wordlist.Level(s); ok {
		return false
	}
	vowelCount := 0
	for _, c := range s {
		if strings.ContainsRune(vowels, c) {
			vowelCount++
		}
	}
	return vowelCount == 0 || float64(vowelCount)/float64(n) < 0.20
}

func parseAndValidate(pairs []rawPair, tldSet map[string]struct{}) (valid, invalid []rawPair) {
	seenSLD := make(map[string]struct{}, len(pairs))
	for _, p := range pairs {
		if validatePair(p, tldSet) != reasonNone {
			invalid = append(invalid, p)
			continue
		}
		if _, dup := seenSLD[p.SLD]; dup {
			continue // deduplicate by SLD
		}
		seenSLD[p.SLD] = struct{}{}
		valid = append(valid, p)
	}
	return
}

// rankedCandidates validates each variant's pairs separately, assigns a
// within-variant LLMRank (1.0=first, decreasing), then merges with SLD dedup.
// Processing variants in order means variant 1's first occurrence wins on SLD ties.
func rankedCandidates(perVariant [][]rawPair, tldSet map[string]struct{}) []algorithmic.Candidate {
	out, _ := rankedCandidatesWithFunnel(perVariant, tldSet)
	return out
}

func hallucinatedTLDs(pairs []rawPair, tldSet map[string]struct{}) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, p := range pairs {
		if _, allowed := tldSet[p.TLD]; !allowed {
			if _, dup := seen[p.TLD]; !dup {
				seen[p.TLD] = struct{}{}
				out = append(out, p.TLD)
			}
		}
	}
	return out
}

func stripFences(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		// remove opening fence line
		if i := strings.Index(s, "\n"); i != -1 {
			s = s[i+1:]
		}
	}
	if strings.HasSuffix(s, "```") {
		s = s[:strings.LastIndex(s, "```")]
	}
	return strings.TrimSpace(s)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
