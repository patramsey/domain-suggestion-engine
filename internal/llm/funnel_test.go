package llm

import (
	"fmt"
	"strings"
	"testing"

	"github.com/patlivet/domain-suggestion-engine/internal/algorithmic"
)

// --- rankedCandidatesWithFunnel ---

func TestFunnelCountsEachRejectReason(t *testing.T) {
	tldSet := map[string]struct{}{"io": {}, "co": {}}
	v1 := []rawPair{
		{SLD: "forge", TLD: "io"},   // kept
		{SLD: "Forge1", TLD: "io"},  // bad format
		{SLD: "strn", TLD: "io"},    // truncation (vowel-starved)
		{SLD: "cedar", TLD: "xyzq"}, // unknown TLD
		{SLD: "forge", TLD: "co"},   // duplicate within variant
	}
	v2 := []rawPair{
		{SLD: "forge", TLD: "co"}, // duplicate across variants
		{SLD: "relay", TLD: "co"}, // kept
	}

	cands, f := rankedCandidatesWithFunnel([][]rawPair{v1, v2}, tldSet)

	want := Funnel{Returned: 7, BadFormat: 1, Truncated: 1, UnknownTLD: 1, DupInVariant: 1, DupAcrossVariants: 1, Kept: 2}
	if f != want {
		t.Errorf("funnel = %+v, want %+v", f, want)
	}
	if len(cands) != f.Kept {
		t.Errorf("len(cands) = %d, want Kept = %d", len(cands), f.Kept)
	}
}

func TestFunnelOutcomesSumToReturned(t *testing.T) {
	tldSet := map[string]struct{}{"io": {}}
	pairs := []rawPair{{SLD: "forge", TLD: "io"}, {SLD: "a", TLD: "io"}, {SLD: "forge", TLD: "io"}, {SLD: "cedar", TLD: "zz"}}
	_, f := rankedCandidatesWithFunnel([][]rawPair{pairs}, tldSet)
	sum := f.BadFormat + f.Truncated + f.UnknownTLD + f.DupInVariant + f.DupAcrossVariants + f.Kept
	if sum != f.Returned {
		t.Errorf("outcomes sum to %d, want Returned = %d (%+v)", sum, f.Returned, f)
	}
}

// rankedCandidates is a thin wrapper over rankedCandidatesWithFunnel (see
// client.go), so it is exercised with a fixed input and an exact expected
// output rather than by re-deriving the answer from the function under test.
func TestRankedCandidatesFixedOutput(t *testing.T) {
	tldSet := map[string]struct{}{"io": {}, "co": {}}
	v1 := []rawPair{{SLD: "forge", TLD: "io"}, {SLD: "cedar", TLD: "co"}}
	v2 := []rawPair{{SLD: "relay", TLD: "co"}, {SLD: "forge", TLD: "co"}} // forge: dup across variants, dropped

	got := rankedCandidates([][]rawPair{v1, v2}, tldSet)

	want := []algorithmic.Candidate{
		{SLD: "forge", TLD: "io", Source: "llm", LLMRank: 1.0},
		{SLD: "cedar", TLD: "co", Source: "llm", LLMRank: 0.5},
		{SLD: "relay", TLD: "co", Source: "llm", LLMRank: 1.0},
	}
	if len(got) != len(want) {
		t.Fatalf("rankedCandidates returned %d candidates, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("candidate %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestFunnelAdd(t *testing.T) {
	a := Funnel{Requested: 21, Returned: 20, Kept: 18, FailedCalls: 1, PartialParses: 1, MaxTokenStops: 1}
	b := Funnel{Requested: 21, Returned: 10, BadFormat: 2, Kept: 8}
	got := a.Add(b)
	want := Funnel{Requested: 42, Returned: 30, BadFormat: 2, Kept: 26, FailedCalls: 1, PartialParses: 1, MaxTokenStops: 1}
	if got != want {
		t.Errorf("Add = %+v, want %+v", got, want)
	}
}

// --- interpretResponse ---

func TestInterpretResponseParsesThoughtTokens(t *testing.T) {
	body := `{"candidates":[{"content":{"parts":[{"text":"[{\"sld\":\"forge\",\"tld\":\"io\"}]"}]},"finishReason":"STOP"}],
		"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":50,"thoughtsTokenCount":30,"totalTokenCount":180}}`
	r, err := interpretResponse([]byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := TokenUsage{PromptTokens: 100, CandidateTokens: 50, ThoughtsTokens: 30, TotalTokens: 180}
	if r.usage != want {
		t.Errorf("usage = %+v, want %+v", r.usage, want)
	}
	if r.partial || r.maxTokens {
		t.Errorf("clean STOP response flagged partial=%v maxTokens=%v", r.partial, r.maxTokens)
	}
	if len(r.pairs) != 1 || r.pairs[0].SLD != "forge" {
		t.Errorf("pairs = %+v", r.pairs)
	}
}

func TestInterpretResponseFlagsMaxTokensAndPartial(t *testing.T) {
	body := `{"candidates":[{"content":{"parts":[{"text":"[{\"sld\":\"forge\",\"tld\":\"io\"},{\"sld\":\"ced"}]},"finishReason":"MAX_TOKENS"}],
		"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":50,"totalTokenCount":150}}`
	r, err := interpretResponse([]byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !r.maxTokens {
		t.Error("expected maxTokens for finishReason MAX_TOKENS")
	}
	if !r.partial {
		t.Error("expected partial parse for truncated array")
	}
	if len(r.pairs) != 1 {
		t.Errorf("expected 1 recovered pair, got %d", len(r.pairs))
	}
}

func TestInterpretResponseNoCandidates(t *testing.T) {
	if _, err := interpretResponse([]byte(`{"candidates":[]}`)); err == nil {
		t.Error("expected error for response with no candidates")
	}
}

func TestTokenUsageAddIncludesThoughts(t *testing.T) {
	got := TokenUsage{ThoughtsTokens: 5, TotalTokens: 10}.add(TokenUsage{ThoughtsTokens: 7, TotalTokens: 3})
	if got.ThoughtsTokens != 12 || got.TotalTokens != 13 {
		t.Errorf("add = %+v", got)
	}
}

func TestOverRequestMatchesBuildRequest(t *testing.T) {
	_, user := BuildRequest("pizza", []string{"pizza"}, []string{"com"}, 7, nil, nil)
	want := fmt.Sprintf("Generate %d ", overRequest(7))
	if !strings.Contains(user, want) {
		t.Errorf("BuildRequest no longer requests overRequest(7) = %d names; got: %s", overRequest(7), user)
	}
}

// --- PromptFingerprint ---

func TestPromptFingerprintStable(t *testing.T) {
	if PromptFingerprint(SystemPrompt, nil) != PromptFingerprint(SystemPrompt, nil) {
		t.Error("fingerprint not deterministic")
	}
}

func TestPromptFingerprintChangesWithInputs(t *testing.T) {
	base := PromptFingerprint(SystemPrompt, nil)
	if PromptFingerprint(SystemPrompt+" ", nil) == base {
		t.Error("fingerprint unchanged when system prompt changes")
	}
	if PromptFingerprint(SystemPrompt, []string{"a", "b", "c"}) == base {
		t.Error("fingerprint unchanged when variant overrides are supplied")
	}
}
