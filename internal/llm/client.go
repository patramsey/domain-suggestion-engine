package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/patlivet/domain-suggestion-engine/internal/algorithmic"
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
}

// NewClient creates a Gemini client. model is the full model ID (e.g. "gemini-3.1-flash-lite").
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

// EvalGenerate runs the full generation pipeline with a custom system prompt.
// variantOverrides optionally replaces the per-variant instructions; must be
// len(llmVariants) if non-nil. Intended for prompt experimentation only.
func (c *Client) EvalGenerate(ctx context.Context, customSystem string, rawInput string, tokens []string, allTLDs []string, tldSet map[string]struct{}, count int, variantOverrides []string) ([]algorithmic.Candidate, TokenUsage, error) {
	variantCount := int(math.Ceil(float64(count) / float64(len(llmVariants))))

	type vResult struct {
		pairs []rawPair
		usage TokenUsage
		err   error
	}

	results := make([]vResult, len(llmVariants))
	var wg sync.WaitGroup

	for i, v := range llmVariants {
		wg.Add(1)
		go func(i int, v Variant) {
			defer wg.Done()
			_, user := BuildRequest(rawInput, tokens, allTLDs, variantCount, nil, nil)
			if len(variantOverrides) == len(llmVariants) {
				user += variantOverrides[i]
			} else {
				user += variantInstruction(v)
			}
			pairs, usage, err := c.callWithRetry(ctx, customSystem, user)
			results[i] = vResult{pairs, usage, err}
		}(i, v)
	}

	wg.Wait()

	var perVariant [][]rawPair
	var totalUsage TokenUsage
	ok := 0
	for _, r := range results {
		totalUsage = totalUsage.add(r.usage)
		if r.err == nil {
			perVariant = append(perVariant, r.pairs)
			ok++
		}
	}
	if ok == 0 {
		return nil, totalUsage, fmt.Errorf("all llm variants failed")
	}
	return rankedCandidates(perVariant, tldSet), totalUsage, nil
}

// TokenUsage holds token counts from a Gemini API call (or the sum across retries).
type TokenUsage struct {
	PromptTokens    int
	CandidateTokens int
	TotalTokens     int
}

func (a TokenUsage) add(b TokenUsage) TokenUsage {
	return TokenUsage{
		PromptTokens:    a.PromptTokens + b.PromptTokens,
		CandidateTokens: a.CandidateTokens + b.CandidateTokens,
		TotalTokens:     a.TotalTokens + b.TotalTokens,
	}
}

var llmVariants = []Variant{VariantEvocative, VariantWordplay, VariantCrafted}

// Generate runs parallel LLM calls with different creative variants and merges results.
// Each variant requests count/N suggestions so total budget ≈ count×3 (same as before).
func (c *Client) Generate(ctx context.Context, rawInput string, tokens []string, tlds []string, tldSet map[string]struct{}, count int, unavailable, inspireFrom []string) ([]algorithmic.Candidate, TokenUsage, error) {
	variantCount := int(math.Ceil(float64(count) / float64(len(llmVariants))))

	type variantResult struct {
		pairs []rawPair
		usage TokenUsage
		err   error
	}

	results := make([]variantResult, len(llmVariants))
	var wg sync.WaitGroup

	for i, v := range llmVariants {
		wg.Add(1)
		go func(i int, v Variant) {
			defer wg.Done()
			system, user := BuildRequest(rawInput, tokens, tlds, variantCount, unavailable, inspireFrom)
			user += variantInstruction(v)
			pairs, usage, err := c.callWithRetry(ctx, system, user)
			results[i] = variantResult{pairs, usage, err}
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
			slog.Warn("llm variant failed", "variant", i, "err", r.err)
			continue
		}
		perVariant = append(perVariant, r.pairs)
		allPairs = append(allPairs, r.pairs...)
		successCount++
	}

	if successCount == 0 {
		return nil, totalUsage, fmt.Errorf("all llm variants failed")
	}

	// retry once if >50% of merged pairs are invalid (hallucinated TLDs)
	_, invalid := parseAndValidate(allPairs, tldSet)
	if len(allPairs) > 0 && float64(len(invalid))/float64(len(allPairs)) > 0.5 {
		badTLDs := uniqueTLDs(invalid)
		retrySystem, retryUser := BuildRetryRequest(rawInput, tokens, tlds, count, badTLDs)
		retryRaw, retryUsage, retryErr := c.call(ctx, retrySystem, retryUser)
		totalUsage = totalUsage.add(retryUsage)
		if retryErr == nil {
			retryValid, _ := parseAndValidate(retryRaw, tldSet)
			if len(retryValid) > len(rankedCandidates(perVariant, tldSet)) {
				perVariant = [][]rawPair{retryRaw}
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

type thinkingCfg struct {
	ThinkingBudget int `json:"thinkingBudget"`
}

type geminiUsage struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

type geminiResponse struct {
	Candidates []struct {
		Content geminiContent `json:"content"`
	} `json:"candidates"`
	UsageMetadata geminiUsage `json:"usageMetadata"`
}

// callWithRetry wraps call with retry logic:
//   - 429 rate limit: up to 3 attempts with 500ms / 1s backoff
//   - other errors: 1 retry after 100ms
func (c *Client) callWithRetry(ctx context.Context, system, user string) ([]rawPair, TokenUsage, error) {
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
				return nil, totalUsage, ctx.Err()
			}
		}
		pairs, usage, err := c.call(ctx, system, user)
		totalUsage = totalUsage.add(usage)
		if err == nil {
			return pairs, totalUsage, nil
		}
		if ctx.Err() != nil {
			return nil, totalUsage, ctx.Err()
		}
		lastErr = err
		slog.Warn("llm call failed", "attempt", attempt+1, "err", err)
	}
	return nil, totalUsage, lastErr
}

func (c *Client) call(ctx context.Context, system, user string) ([]rawPair, TokenUsage, error) {
	req := geminiRequest{
		SystemInstruction: &geminiContent{
			Parts: []geminiPart{{Text: system}},
		},
		Contents: []geminiContent{
			{Role: "user", Parts: []geminiPart{{Text: user}}},
		},
		GenerationConfig: &genConfig{
			Temperature:      c.temperature(),
			ThinkingConfig:   &thinkingCfg{ThinkingBudget: 0},
			ResponseMIMEType: "application/json",
		},
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, TokenUsage{}, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.endpoint()+"?key="+c.apiKey, bytes.NewReader(body))
	if err != nil {
		return nil, TokenUsage{}, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, TokenUsage{}, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, TokenUsage{}, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, TokenUsage{}, &rateLimitError{msg: fmt.Sprintf("rate limited (429): %s", truncate(string(respBody), 200))}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, TokenUsage{}, fmt.Errorf("gemini API status %d: %s", resp.StatusCode, truncate(string(respBody), 200))
	}

	var gemResp geminiResponse
	if err := json.Unmarshal(respBody, &gemResp); err != nil {
		return nil, TokenUsage{}, fmt.Errorf("unmarshal response: %w", err)
	}
	if len(gemResp.Candidates) == 0 {
		return nil, TokenUsage{}, fmt.Errorf("no candidates in response")
	}

	usage := TokenUsage{
		PromptTokens:    gemResp.UsageMetadata.PromptTokenCount,
		CandidateTokens: gemResp.UsageMetadata.CandidatesTokenCount,
		TotalTokens:     gemResp.UsageMetadata.TotalTokenCount,
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
	pairs, err := extractJSON(text)
	if err != nil {
		slog.Warn("llm response parse failed", "err", err, "response_preview", truncate(text, 300))
	}
	return pairs, usage, err
}

// rawPair is a parsed (sld, tld) pair from the LLM response.
type rawPair struct {
	SLD string `json:"sld"`
	TLD string `json:"tld"`
}

func extractJSON(text string) ([]rawPair, error) {
	text = stripFences(text)
	match := findFirstJSONArray(text)
	if match != "" {
		var pairs []rawPair
		if json.Unmarshal([]byte(match), &pairs) == nil {
			return pairs, nil
		}
	}
	// Truncated or malformed array — recover individual valid objects.
	if partial := extractPartialPairs(text); len(partial) > 0 {
		slog.Warn("llm response partially parsed", "recovered", len(partial))
		return partial, nil
	}
	return nil, fmt.Errorf("no JSON array found in response")
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

const vowels = "aeiou"

// looksLikeTruncation returns true for consonant-heavy strings that are likely
// mid-word fragments ("crea", "agenc", "ind"). 3-char strings are exempt —
// they're treated as abbreviations ("dns", "css", "crm") not truncations.
// For lengths 4-6, requires at least 25% vowel ratio.
func looksLikeTruncation(s string) bool {
	n := len(s)
	if n > 6 || n <= 3 {
		return false
	}
	vowelCount := 0
	for _, c := range s {
		if strings.ContainsRune(vowels, c) {
			vowelCount++
		}
	}
	return vowelCount == 0 || float64(vowelCount)/float64(n) < 0.25
}

func parseAndValidate(pairs []rawPair, tldSet map[string]struct{}) (valid, invalid []rawPair) {
	seenSLD := make(map[string]struct{}, len(pairs))
	for _, p := range pairs {
		if !sldRe.MatchString(p.SLD) {
			invalid = append(invalid, p)
			continue
		}
		if looksLikeTruncation(p.SLD) {
			invalid = append(invalid, p)
			continue
		}
		if _, ok := tldSet[p.TLD]; !ok {
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
	seenSLD := make(map[string]struct{})
	var out []algorithmic.Candidate
	for _, pairs := range perVariant {
		valid, _ := parseAndValidate(pairs, tldSet)
		n := len(valid)
		for j, p := range valid {
			if _, dup := seenSLD[p.SLD]; dup {
				continue
			}
			seenSLD[p.SLD] = struct{}{}
			rank := 1.0 - float64(j)/float64(n) // [1/n, 1.0], never 0
			out = append(out, algorithmic.Candidate{
				SLD:     p.SLD,
				TLD:     p.TLD,
				Source:  "llm",
				LLMRank: rank,
			})
		}
	}
	return out
}

func uniqueTLDs(pairs []rawPair) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, p := range pairs {
		if _, ok := seen[p.TLD]; !ok {
			seen[p.TLD] = struct{}{}
			out = append(out, p.TLD)
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
