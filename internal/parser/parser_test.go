package parser

import (
	"testing"
)

// minimal ICANN set for tests
var testICANN = map[string]struct{}{
	"com": {}, "io": {}, "ai": {}, "net": {}, "org": {},
	"pizza": {}, "coffee": {}, "co.uk": {}, "uk": {},
}

func tokens(input string) []string {
	return Parse(input, testICANN)
}

func TestSLDInput(t *testing.T) {
	got := tokens("patspizza.com")
	assertContains(t, got, "pat")
	assertContains(t, got, "pizza")
	assertNotContains(t, got, "com")
}

func TestCamelCase(t *testing.T) {
	got := tokens("PatsPizzeria")
	assertContains(t, got, "pat")
}

func TestKeywords(t *testing.T) {
	got := tokens("coffee shop brooklyn")
	assertContains(t, got, "coffee")
	assertContains(t, got, "shop")
	assertContains(t, got, "brooklyn")
}

func TestStopwordsRemoved(t *testing.T) {
	got := tokens("I make handmade candles")
	assertNotContains(t, got, "i")
	assertNotContains(t, got, "make")
}

func TestAllStopwordsReturnsNil(t *testing.T) {
	got := tokens("the and a")
	if len(got) != 0 {
		t.Errorf("expected empty tokens for all-stopword input, got %v", got)
	}
}

func TestDeduplication(t *testing.T) {
	got := tokens("coffee coffee shop")
	count := 0
	for _, t2 := range got {
		if t2 == "coffee" {
			count++
		}
	}
	if count > 1 {
		t.Errorf("duplicate 'coffee' in output: %v", got)
	}
}

func TestTLDStripping(t *testing.T) {
	got := tokens("mybrand.io")
	assertNotContains(t, got, "io")
}

func TestMultiLevelTLDStripping(t *testing.T) {
	got := tokens("mybrand.co.uk")
	assertNotContains(t, got, "co")
	assertNotContains(t, got, "uk")
}

func TestHyphenatedInput(t *testing.T) {
	got := tokens("eco-friendly")
	assertContains(t, got, "eco")
	assertContains(t, got, "friendly")
}

func TestMaxTokens(t *testing.T) {
	got := tokens("one two three four five six seven eight")
	if len(got) > 8 {
		t.Errorf("too many tokens: %v", got)
	}
}

func assertContains(t *testing.T, tokens []string, want string) {
	t.Helper()
	for _, tok := range tokens {
		if tok == want {
			return
		}
	}
	t.Errorf("tokens %v does not contain %q", tokens, want)
}

func assertNotContains(t *testing.T, tokens []string, unwanted string) {
	t.Helper()
	for _, tok := range tokens {
		if tok == unwanted {
			t.Errorf("tokens %v should not contain %q", tokens, unwanted)
			return
		}
	}
}

// --- new stopwords ---

func TestParticipalAdjectivesFiltered(t *testing.T) {
	for _, word := range []string{"powered", "based", "driven", "focused", "built", "made", "used", "designed", "enabled"} {
		got := tokens(word + " startup")
		assertNotContains(t, got, word)
	}
}

func TestGenericProductNounsFiltered(t *testing.T) {
	for _, word := range []string{"tool", "platform", "service", "solution", "software", "system", "product", "document", "review", "workflow", "dashboard"} {
		got := tokens("coffee " + word)
		assertNotContains(t, got, word)
	}
}

func TestSizeQualifiersFiltered(t *testing.T) {
	got := tokens("small coffee shop for large teams")
	assertNotContains(t, got, "small")
	assertNotContains(t, got, "large")
	assertContains(t, got, "coffee")
	assertContains(t, got, "shop")
}

func TestMissingFunctionWordsFiltered(t *testing.T) {
	got := tokens("app with great features for teams")
	assertNotContains(t, got, "with")
	assertNotContains(t, got, "for")
}

// --- compound word splitting ---

func TestWeekendStaysWhole(t *testing.T) {
	got := tokens("weekend hiking")
	assertNotContains(t, got, "week")
	assertNotContains(t, got, "end")
	assertContains(t, got, "weekend")
}

func TestStartupStaysWhole(t *testing.T) {
	got := tokens("startup incubator")
	assertNotContains(t, got, "start")
	assertContains(t, got, "startup")
}

// --- input format variety ---

func TestNumbersOnlyInput(t *testing.T) {
	got := tokens("12345")
	if len(got) != 0 {
		t.Errorf("numbers-only input should return no tokens, got %v", got)
	}
}

func TestSpecialCharsStripped(t *testing.T) {
	got := tokens("bakery!")
	assertContains(t, got, "bakery")
}

func TestSentenceDescription(t *testing.T) {
	got := tokens("AI-powered legal document review tool for small law firms")
	assertNotContains(t, got, "powered")
	assertNotContains(t, got, "document")
	assertNotContains(t, got, "tool")
	assertNotContains(t, got, "small")
	assertContains(t, got, "legal")
	assertContains(t, got, "law")
}

func TestUnderscoreDelimiter(t *testing.T) {
	got := tokens("photo_studio_nyc")
	assertContains(t, got, "photo")
	assertContains(t, got, "studio")
}

func TestDomainWithTLDStripped(t *testing.T) {
	got := tokens("mybrand.com")
	assertNotContains(t, got, "com")
	assertContains(t, got, "mybrand")
}
