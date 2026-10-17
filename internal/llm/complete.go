package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// postGemini sends one generateContent request and returns the raw response
// body. Shared by the suggestion path and Complete.
func (c *Client) postGemini(ctx context.Context, req geminiRequest) ([]byte, error) {
	// Every Gemini call in the process passes through this limit.
	if c.sem != nil {
		select {
		case c.sem <- struct{}{}:
			defer func() { <-c.sem }()
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	// The key goes in a header, not the URL: transport errors (*url.Error)
	// print the full URL, and those errors are logged.
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-goog-api-key", c.apiKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, &rateLimitError{
			msg:        fmt.Sprintf("rate limited (429): %s", truncate(string(respBody), 200)),
			retryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
		}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &statusError{code: resp.StatusCode, msg: truncate(string(respBody), 200)}
	}
	return respBody, nil
}

// Complete sends one prompt and returns the model's text and token usage. It
// asks for a JSON response, like the suggestion path. Used by tools that need
// the model for something other than generating domains (see cmd/judge).
func (c *Client) Complete(ctx context.Context, system, user string) (string, TokenUsage, error) {
	req := geminiRequest{
		SystemInstruction: &geminiContent{Parts: []geminiPart{{Text: system}}},
		Contents:          []geminiContent{{Role: "user", Parts: []geminiPart{{Text: user}}}},
		GenerationConfig: &genConfig{
			Temperature:      c.temperature(),
			ThinkingConfig:   &thinkingCfg{ThinkingLevel: c.thinkingLevel()},
			ResponseMIMEType: "application/json",
		},
	}
	respBody, err := c.postGemini(ctx, req)
	if err != nil {
		return "", TokenUsage{}, err
	}
	var gemResp geminiResponse
	if err := json.Unmarshal(respBody, &gemResp); err != nil {
		return "", TokenUsage{}, fmt.Errorf("unmarshal response: %w", err)
	}
	usage := TokenUsage{
		PromptTokens:    gemResp.UsageMetadata.PromptTokenCount,
		CandidateTokens: gemResp.UsageMetadata.CandidatesTokenCount,
		ThoughtsTokens:  gemResp.UsageMetadata.ThoughtsTokenCount,
		TotalTokens:     gemResp.UsageMetadata.TotalTokenCount,
	}
	if len(gemResp.Candidates) == 0 {
		return "", usage, fmt.Errorf("no candidates in response")
	}
	var text strings.Builder
	for _, part := range gemResp.Candidates[0].Content.Parts {
		if part.Text != "" {
			text.WriteString(part.Text)
		}
	}
	return text.String(), usage, nil
}
