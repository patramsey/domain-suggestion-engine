package llm

import (
	"fmt"
	"strings"
	"testing"
)

var testTLDSet = map[string]struct{}{
	"com": {}, "io": {}, "ai": {}, "pizza": {}, "coffee": {}, "studio": {},
}

// --- prompt tests ---

func TestBuildRequestTokenPath(t *testing.T) {
	_, user := BuildRequest("coffee shop", []string{"coffee", "shop"}, []string{"com", "io"}, 20, nil, nil)
	if !strings.Contains(user, "coffee, shop") {
		t.Errorf("user prompt should contain tokens; got: %s", user)
	}
	if !strings.Contains(user, "com, io") {
		t.Errorf("user prompt should contain TLD list; got: %s", user)
	}
	if want := fmt.Sprintf("Generate %d ", overRequest(20)); !strings.Contains(user, want) {
		t.Errorf("user prompt should request %q; got: %s", want, user)
	}
}

func TestBuildRequestFallbackPath(t *testing.T) {
	_, user := BuildRequest("the and a", nil, []string{"com"}, 10, nil, nil)
	if !strings.Contains(user, "the and a") {
		t.Errorf("fallback path should include raw input; got: %s", user)
	}
}

func TestBuildRetryIncludesBadTLDs(t *testing.T) {
	_, user := BuildRetryRequest("coffee", []string{"coffee"}, []string{"com"}, 10, []string{"xyz", "fakeland"}, []string{"taken.com"}, []string{"liked.io"})
	if !strings.Contains(user, "xyz") || !strings.Contains(user, "fakeland") {
		t.Errorf("retry prompt should name bad TLDs; got: %s", user)
	}
	if !strings.Contains(user, "taken.com") {
		t.Errorf("retry prompt should include unavailable domains; got: %s", user)
	}
	if !strings.Contains(user, "liked.io") {
		t.Errorf("retry prompt should include inspire_from domains; got: %s", user)
	}
}

// --- validation tests ---

func TestParseAndValidateHappyPath(t *testing.T) {
	input := []rawPair{
		{SLD: "ember", TLD: "coffee"},
		{SLD: "bloom", TLD: "studio"},
	}
	valid, invalid := parseAndValidate(input, testTLDSet)
	if len(valid) != 2 {
		t.Errorf("expected 2 valid, got %d", len(valid))
	}
	if len(invalid) != 0 {
		t.Errorf("expected 0 invalid, got %d", len(invalid))
	}
}

func TestParseAndValidateDropsHallucinatedTLD(t *testing.T) {
	input := []rawPair{
		{SLD: "ember", TLD: "coffee"},
		{SLD: "hello", TLD: "fakeland"},
	}
	valid, invalid := parseAndValidate(input, testTLDSet)
	if len(valid) != 1 {
		t.Errorf("expected 1 valid, got %d", len(valid))
	}
	if len(invalid) != 1 || invalid[0].TLD != "fakeland" {
		t.Errorf("expected 1 invalid with TLD=fakeland, got %v", invalid)
	}
}

func TestParseAndValidateDropsMalformedSLD(t *testing.T) {
	cases := []rawPair{
		{SLD: "ab", TLD: "com"},                 // too short
		{SLD: "cof3ee", TLD: "com"},             // digit
		{SLD: "my-coffee", TLD: "com"},          // hyphen
		{SLD: "verylongdomainname", TLD: "com"}, // too long
	}
	for _, p := range cases {
		valid, invalid := parseAndValidate([]rawPair{p}, testTLDSet)
		if len(valid) != 0 {
			t.Errorf("SLD %q should be invalid, but passed validation", p.SLD)
		}
		if len(invalid) != 1 {
			t.Errorf("SLD %q should be in invalid list", p.SLD)
		}
	}
}

func TestParseAndValidateDeduplicatesBySLD(t *testing.T) {
	input := []rawPair{
		{SLD: "ember", TLD: "coffee"},
		{SLD: "ember", TLD: "com"}, // duplicate SLD
	}
	valid, _ := parseAndValidate(input, testTLDSet)
	if len(valid) != 1 {
		t.Errorf("duplicate SLD should be deduped; got %d", len(valid))
	}
}

func TestExtractJSONFromCleanArray(t *testing.T) {
	text := `[{"sld": "ember", "tld": "coffee"}, {"sld": "bloom", "tld": "studio"}]`
	pairs, err := extractJSON(text)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pairs) != 2 || pairs[0].SLD != "ember" {
		t.Errorf("unexpected result: %v", pairs)
	}
}

func TestExtractJSONStripsMarkdownFences(t *testing.T) {
	text := "```json\n[{\"sld\": \"ember\", \"tld\": \"coffee\"}]\n```"
	pairs, err := extractJSON(text)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pairs) != 1 || pairs[0].SLD != "ember" {
		t.Errorf("unexpected result: %v", pairs)
	}
}

func TestExtractJSONNoArrayReturnsError(t *testing.T) {
	_, err := extractJSON("Here are some suggestions: ember.coffee, bloom.studio")
	if err == nil {
		t.Fatal("expected error when no JSON array found")
	}
}

func TestExtractJSONIgnoresTrailingCommentary(t *testing.T) {
	text := `[{"sld": "ember", "tld": "coffee"}]
Note: these are creative suggestions based on your input.`
	pairs, err := extractJSON(text)
	if err != nil {
		t.Fatalf("trailing commentary should not break extraction: %v", err)
	}
	if len(pairs) != 1 {
		t.Errorf("expected 1 pair, got %d", len(pairs))
	}
}

func TestExtractJSONWithNestedBracketsInString(t *testing.T) {
	text := `[{"sld": "ember", "tld": "coffee"}, {"sld": "bloom", "tld": "studio"}]`
	pairs, err := extractJSON(text)
	if err != nil || len(pairs) != 2 {
		t.Errorf("unexpected result: err=%v pairs=%v", err, pairs)
	}
}

func TestExtractJSONPrefixedByText(t *testing.T) {
	text := "Here are my suggestions:\n[{\"sld\": \"ember\", \"tld\": \"coffee\"}]"
	pairs, err := extractJSON(text)
	if err != nil {
		t.Fatalf("text before array should be ignored: %v", err)
	}
	if len(pairs) != 1 || pairs[0].SLD != "ember" {
		t.Errorf("unexpected result: %v", pairs)
	}
}

// --- truncation detection ---

func TestLooksLikeTruncationRejectsVowelStarved(t *testing.T) {
	// 4-6 char strings with < 25% vowels should be caught.
	bad := []string{
		"xktz",  // 0/4 vowels = 0%
		"strpd", // 1/5 vowels = 20%
		"brnd",  // 0/4 vowels = 0%
	}
	for _, s := range bad {
		if !looksLikeTruncation(s) {
			t.Errorf("%q should be flagged as truncation", s)
		}
	}
}

func TestLooksLikeTruncationAllowsGoodShortWords(t *testing.T) {
	// Good short words and valid abbreviations should not be flagged.
	good := []string{
		"lex", "rev", "arc", "hub", "dev", "app", "flux", "nova", "stud",
		// 5- and 6-letter real words with single vowel or y
		"craft", "brand", "smart", "fresh", "trend", "swift", "spring", "crypt",
		// 3-char abbreviations are always allowed regardless of vowels
		"dns", "css", "crm", "mkt", "sql",
	}
	for _, s := range good {
		if looksLikeTruncation(s) {
			t.Errorf("%q should not be flagged as truncation", s)
		}
	}
}

func TestLooksLikeTruncationAllowsLongerWords(t *testing.T) {
	// Words > 6 chars are never flagged — long coinages are assumed intentional.
	if looksLikeTruncation("vantastic") {
		t.Error("vantastic (9 chars) should not be flagged")
	}
	if looksLikeTruncation("brandish") {
		t.Error("brandish (8 chars) should not be flagged")
	}
}

func TestParseAndValidateRejectsVowelStarvedSLDs(t *testing.T) {
	input := []rawPair{
		{SLD: "ember", TLD: "coffee"},
		{SLD: "xktz", TLD: "com"}, // 0 vowels — caught
		{SLD: "brnd", TLD: "com"}, // 0 vowels — caught
	}
	valid, _ := parseAndValidate(input, testTLDSet)
	for _, p := range valid {
		if p.SLD == "xktz" || p.SLD == "brnd" {
			t.Errorf("vowel-starved SLD %q should have been rejected", p.SLD)
		}
	}
	if len(valid) != 1 || valid[0].SLD != "ember" {
		t.Errorf("expected only ember to be valid, got %v", valid)
	}
}

// --- prompt structure ---

func TestBuildRequestIncludesContext(t *testing.T) {
	rawInput := "AI-powered legal document review tool for small law firms"
	toks := []string{"legal", "law", "firm"}
	_, user := BuildRequest(rawInput, toks, []string{"com", "ai"}, 10, nil, nil)
	// rawInput is much longer than tokens, so context line should be included
	if !strings.Contains(user, rawInput) {
		t.Errorf("user prompt should include full context when rawInput >> tokens; got: %s", user)
	}
}

func TestBuildRequestOverRequests(t *testing.T) {
	_, user := BuildRequest("pizza", []string{"pizza"}, []string{"com"}, 10, nil, nil)
	if want := fmt.Sprintf("Generate %d ", overRequest(10)); !strings.Contains(user, want) {
		t.Errorf("should request %q; got: %s", want, user)
	}
}

func TestBuildRequestNarrowTLDFilter(t *testing.T) {
	// Single TLD: should not ask for multiple distinct TLDs or restrict to at most twice
	_, user1 := BuildRequest("pizza", []string{"pizza"}, []string{"com"}, 10, nil, nil)
	if strings.Contains(user1, "distinct TLDs") || strings.Contains(user1, "no TLD more than twice") {
		t.Errorf("single TLD request should not contain multi-TLD instructions; got: %s", user1)
	}

	// 2 TLDs: should ask for at least 2 distinct TLDs, but not restrict to at most twice
	_, user2 := BuildRequest("pizza", []string{"pizza"}, []string{"com", "io"}, 10, nil, nil)
	if !strings.Contains(user2, "Use at least 2 distinct TLDs") {
		t.Errorf("expected 'Use at least 2 distinct TLDs' in: %s", user2)
	}
	if strings.Contains(user2, "no TLD more than twice") {
		t.Errorf("2-TLD request with 20 names should not restrict to at most twice; got: %s", user2)
	}
}

func TestVariantInstructionsMatchBatches(t *testing.T) {
	got := VariantInstructions()
	if len(got) != len(llmVariants) {
		t.Fatalf("got %d instructions, want %d", len(got), len(llmVariants))
	}
	for i, v := range llmVariants {
		if got[i] != variantInstruction(v) || got[i] == "" {
			t.Errorf("instruction %d does not match variant %d", i, v)
		}
	}
}

func TestCraftedBriefAsksForGroundedCompounds(t *testing.T) {
	b := variantInstruction(VariantCrafted)
	for _, want := range []string{"compound names", "two short, ordinary English words", "No invented prefixes or suffixes"} {
		if !strings.Contains(b, want) {
			t.Errorf("crafted brief missing %q", want)
		}
	}
}
