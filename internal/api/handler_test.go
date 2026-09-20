package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/patlivet/domain-suggestion-engine/internal/algorithmic"
	"github.com/patlivet/domain-suggestion-engine/internal/dnscheck"
	"github.com/patlivet/domain-suggestion-engine/internal/scorer"
)

func newTestHandler(t *testing.T) *Handler {
	t.Helper()
	h, err := NewHandler(Config{
		GeminiAPIKey:     "test-key",
		GeminiModel:      "gemini-2.5-flash-lite",
		CacheSize:        10,
		LLMShare:         0.60,
		CommonWordSlots:  2,
		AlgoEnabled:      true,
		ActiveGenerators: []string{"hacks"},
		AllGenerators:    []string{"hacks"},
		Version:          "test",
		BuiltAt:          "now",
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	return h
}

func post(h *Handler, body string, query string) *httptest.ResponseRecorder {
	url := "/suggest"
	if query != "" {
		url += "?" + query
	}
	req := httptest.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func get(h *Handler, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// --- validation ---

func TestMissingInput(t *testing.T) {
	h := newTestHandler(t)
	w := post(h, `{}`, "")
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["code"] != CodeMissingInput {
		t.Errorf("want code=%s, got %s", CodeMissingInput, resp["code"])
	}
}

func TestInputTooLong(t *testing.T) {
	h := newTestHandler(t)
	long := strings.Repeat("a", 501)
	w := post(h, `{"input":"`+long+`"}`, "")
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("want 422, got %d", w.Code)
	}
}

func TestAmbiguousTLDFilter(t *testing.T) {
	h := newTestHandler(t)
	w := post(h, `{"input":"coffee","tld_filter":{"category":"classic","list":["com"]}}`, "")
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["code"] != CodeAmbiguousTLDFilter {
		t.Errorf("want code=%s, got %s", CodeAmbiguousTLDFilter, resp["code"])
	}
}

func TestUnknownTLDInList(t *testing.T) {
	h := newTestHandler(t)
	w := post(h, `{"input":"coffee","tld_filter":{"list":["fakemadeuptld999"]}}`, "")
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("want 422, got %d", w.Code)
	}
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["code"] != CodeUnknownTLD {
		t.Errorf("want code=%s, got %v", CodeUnknownTLD, resp["code"])
	}
}

func TestDefaultCount(t *testing.T) {
	// count defaults to 20 — not tested here since LLM is mocked; just check no panic
	h := newTestHandler(t)
	// use a known-working list filter to exercise the path without LLM
	w := post(h, `{"input":"coffee","count":0}`, "")
	// will likely fail at LLM (no real key), but should not 500 on count=0
	// just check we get a valid JSON response
	if w.Body.Len() == 0 {
		t.Error("expected non-empty response body")
	}
}

// --- GET /tlds/categories ---

func TestCategoriesEndpoint(t *testing.T) {
	h := newTestHandler(t)
	w := get(h, "/tlds/categories")
	if w.Code != http.StatusOK {
		t.Errorf("want 200, got %d", w.Code)
	}
	var resp CategoriesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Categories) < 5 {
		t.Errorf("expected at least 5 categories, got %d", len(resp.Categories))
	}
	// verify default category is present and non-empty
	found := false
	for _, cat := range resp.Categories {
		if cat.Name == "default" {
			found = true
			if cat.Count < 100 {
				t.Errorf("default category count %d, want >= 100", cat.Count)
			}
		}
	}
	if !found {
		t.Error("default category not found in response")
	}
}

func TestCategoriesCacheHeader(t *testing.T) {
	h := newTestHandler(t)
	w := get(h, "/tlds/categories")
	if !strings.Contains(w.Header().Get("Cache-Control"), "max-age") {
		t.Errorf("expected Cache-Control with max-age, got %q", w.Header().Get("Cache-Control"))
	}
}

// --- GET /health ---

func TestHealthEndpointDegraded(t *testing.T) {
	// handler with no API key set should report llm_key check failed
	h, _ := NewHandler(Config{
		GeminiAPIKey:     "", // intentionally empty
		GeminiModel:      "gemini-2.5-flash-lite-preview-06-17",
		CacheSize:        10,
		AlgoEnabled:      true,
		ActiveGenerators: []string{"direct"},
		AllGenerators:    []string{"direct"},
	})
	w := get(h, "/health")
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("want 503 when API key missing, got %d", w.Code)
	}
	var resp HealthResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Status != "degraded" {
		t.Errorf("want status=degraded, got %s", resp.Status)
	}
	if resp.Checks["llm_key"].OK {
		t.Error("llm_key check should fail when key is empty")
	}
}

func TestHealthEndpointOK(t *testing.T) {
	h := newTestHandler(t) // has test-key set
	w := get(h, "/health")
	if w.Code != http.StatusOK {
		t.Errorf("want 200, got %d", w.Code)
	}
	var resp HealthResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Status != "ok" {
		t.Errorf("want status=ok, got %s: checks=%v", resp.Status, resp.Checks)
	}
}

// --- GET /config ---

func TestConfigEndpoint(t *testing.T) {
	h := newTestHandler(t)
	w := get(h, "/config")
	if w.Code != http.StatusOK {
		t.Errorf("want 200, got %d", w.Code)
	}
	var resp ConfigResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.LLM.Model == "" {
		t.Error("model name should be set")
	}
	if !resp.LLM.APIKeySet {
		t.Error("api_key_set should be true when key is provided")
	}
	if resp.LLM.LLMShare != 0.60 {
		t.Errorf("unexpected llm_share: %f", resp.LLM.LLMShare)
	}
	if resp.Ranking.CommonWordSlots != 2 {
		t.Errorf("unexpected common_word_slots: %d", resp.Ranking.CommonWordSlots)
	}
	if !resp.Algo.Enabled {
		t.Error("algo should be enabled")
	}
	if len(resp.Algo.ActiveGenerators) == 0 {
		t.Error("active_generators should be non-empty")
	}
	if resp.TLDRegistry.ICANNTLDCount < 1000 {
		t.Errorf("icann_tld_count %d, want >= 1000", resp.TLDRegistry.ICANNTLDCount)
	}
	if resp.TLDRegistry.PSLDate == "" {
		t.Error("psl_date should be set")
	}
	if resp.Build.Version != "test" {
		t.Errorf("unexpected version: %s", resp.Build.Version)
	}
	if len(resp.LLM.Variants) == 0 {
		t.Error("llm variants should be non-empty")
	}
}

func TestConfigNeverExposesAPIKey(t *testing.T) {
	h := newTestHandler(t)
	w := get(h, "/config")
	body := w.Body.String()
	if strings.Contains(body, "test-key") {
		t.Error("config response must not include the API key value")
	}
}

// --- not found / method not allowed ---

func TestNotFound(t *testing.T) {
	h := newTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/unknown", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
}

func TestWrongMethodOnSuggest(t *testing.T) {
	h := newTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/suggest", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("GET /suggest should 404, got %d", w.Code)
	}
}

// --- input validation edge cases ---

func TestInvalidJSON(t *testing.T) {
	h := newTestHandler(t)
	w := post(h, `not json at all`, "")
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400 for invalid JSON, got %d", w.Code)
	}
}

func TestInputExactlyAtMaxLength(t *testing.T) {
	h := newTestHandler(t)
	exact := strings.Repeat("a", 500)
	w := post(h, `{"input":"`+exact+`"}`, "")
	// LLM will fail (fake key) but should not be a validation error
	if w.Code == http.StatusUnprocessableEntity {
		t.Error("input of exactly 500 chars should not be rejected as too long")
	}
}

func TestCountClampedToMax(t *testing.T) {
	h := newTestHandler(t)
	// count > 100 is silently clamped; request should be accepted (may fail at LLM)
	w := post(h, `{"input":"coffee","count":9999}`, "")
	if w.Code == http.StatusBadRequest || w.Code == http.StatusUnprocessableEntity {
		t.Errorf("count=9999 should be clamped not rejected; got %d: %s", w.Code, w.Body)
	}
}

func TestTLDFilterByCategory(t *testing.T) {
	h := newTestHandler(t)
	w := post(h, `{"input":"coffee","tld_filter":{"category":"classic"}}`, "")
	// classic category exists so this should not be a validation error
	if w.Code == http.StatusBadRequest || w.Code == http.StatusUnprocessableEntity {
		t.Errorf("valid category filter should not be rejected; got %d: %s", w.Code, w.Body)
	}
}

func TestTLDFilterUnknownCategory(t *testing.T) {
	h := newTestHandler(t)
	w := post(h, `{"input":"coffee","tld_filter":{"category":"nonexistentcategory999"}}`, "")
	if w.Code != http.StatusBadRequest {
		t.Errorf("unknown category should return 400, got %d", w.Code)
	}
}

func TestTLDFilterByValidList(t *testing.T) {
	h := newTestHandler(t)
	w := post(h, `{"input":"coffee","tld_filter":{"list":["com","io"]}}`, "")
	if w.Code == http.StatusBadRequest || w.Code == http.StatusUnprocessableEntity {
		t.Errorf("valid TLD list should not be rejected; got %d: %s", w.Code, w.Body)
	}
}

func TestTLDFilterMultipleUnknownTLDs(t *testing.T) {
	h := newTestHandler(t)
	w := post(h, `{"input":"coffee","tld_filter":{"list":["fakea","fakeb"]}}`, "")
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("want 422, got %d", w.Code)
	}
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	details, ok := resp["details"].([]interface{})
	if !ok || len(details) < 2 {
		t.Errorf("response should list all unknown TLDs in details; got %v", resp)
	}
}

// --- debug mode ---

func TestDebugModeReturnsTLDsAndGenerators(t *testing.T) {
	h := newTestHandler(t)
	// use TLD list filter so we don't need LLM to succeed
	w := post(h, `{"input":"studio","tld_filter":{"list":["io","com"]}}`, "debug=true")
	if w.Code == http.StatusOK {
		var resp SuggestResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if len(resp.TLDsUsed) == 0 {
			t.Error("debug mode should populate tlds_used")
		}
		// active_generators may be empty if LLM failed and partial, that's ok
	}
	// if LLM fails, that's fine — we're testing the debug flag wiring not LLM
}

// --- response structure ---

func TestResponseContentType(t *testing.T) {
	h := newTestHandler(t)
	w := get(h, "/tlds/categories")
	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Errorf("want application/json Content-Type, got %q", ct)
	}
}

func TestHealthContentType(t *testing.T) {
	h := newTestHandler(t)
	w := get(h, "/health")
	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Errorf("want application/json Content-Type, got %q", ct)
	}
}

func TestHealthChecksAllPresent(t *testing.T) {
	h := newTestHandler(t)
	w := get(h, "/health")
	var resp HealthResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	for _, key := range []string{"tld_registry", "llm_key", "cache", "generators"} {
		if _, ok := resp.Checks[key]; !ok {
			t.Errorf("health response missing check %q", key)
		}
	}
}

func TestAlgoDisabledHealth(t *testing.T) {
	h, _ := NewHandler(Config{
		GeminiAPIKey:     "test-key",
		GeminiModel:      "gemini-2.5-flash-lite",
		CacheSize:        10,
		AlgoEnabled:      true,
		ActiveGenerators: []string{}, // enabled but no generators → degraded
		AllGenerators:    []string{"hacks"},
	})
	w := get(h, "/health")
	var resp HealthResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Checks["generators"].OK {
		t.Error("generators check should fail when algo enabled but no generators active")
	}
}

func TestCacheSizeZeroDisablesCache(t *testing.T) {
	h, err := NewHandler(Config{
		GeminiAPIKey:     "test-key",
		GeminiModel:      "gemini-2.5-flash-lite",
		CacheSize:        0,
		LLMShare:         0.60,
		AlgoEnabled:      true,
		ActiveGenerators: []string{"hacks"},
		AllGenerators:    []string{"hacks"},
		Version:          "test",
		BuiltAt:          "now",
	})
	if err != nil {
		t.Fatalf("NewHandler with CacheSize 0: %v", err)
	}
	if h.cache != nil {
		t.Error("cache should be nil when CacheSize is 0")
	}
	var resp ConfigResponse
	if err := json.Unmarshal(get(h, "/config").Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Cache.Enabled {
		t.Error("/config should report the cache as disabled")
	}
	body := `{"input":"coffee shop"}`
	for i := 0; i < 2; i++ {
		w := post(h, body, "")
		if w.Code != http.StatusOK {
			t.Fatalf("request %d: want 200, got %d", i, w.Code)
		}
		if w.Header().Get("Age") != "" {
			t.Errorf("request %d served from cache (Age header set)", i)
		}
	}
}

func TestConfigReportsCacheEnabledByDefault(t *testing.T) {
	h := newTestHandler(t)
	var resp ConfigResponse
	if err := json.Unmarshal(get(h, "/config").Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.Cache.Enabled {
		t.Error("/config should report the cache as enabled when CacheSize > 0")
	}
}

func TestNewHandlerRejectsBadCommonWordSlots(t *testing.T) {
	for _, n := range []int{-1, 11} {
		if _, err := NewHandler(Config{GeminiAPIKey: "k", GeminiModel: "m", CommonWordSlots: n}); err == nil {
			t.Errorf("CommonWordSlots %d: want error", n)
		}
	}
}

func TestDiversityCapScaling(t *testing.T) {
	makePool := func(tld string, n int) []scorer.ScoredCandidate {
		var res []scorer.ScoredCandidate
		for i := 0; i < n; i++ {
			res = append(res, scorer.ScoredCandidate{
				Candidate: algorithmic.Candidate{SLD: "name", TLD: tld, Source: "llm"},
				Score:     1.0 - float64(i)*0.01,
			})
		}
		return res
	}

	// 1 TLD: should not cap at 25% (for count 10, cap would be 3). Should return all 10.
	pool1 := makePool("com", 10)
	got1 := diversityCap(pool1, 10, 1)
	if len(got1) != 10 {
		t.Errorf("diversityCap with 1 TLD: want 10, got %d", len(got1))
	}

	// 2 TLDs: cap at 75% -> max 8 for count 10
	pool2 := append(makePool("com", 10), makePool("io", 10)...)
	got2 := diversityCap(pool2, 10, 2)
	comCount, ioCount := 0, 0
	for _, sc := range got2 {
		switch sc.TLD {
		case "com":
			comCount++
		case "io":
			ioCount++
		}
	}
	if comCount > 8 || ioCount > 8 {
		t.Errorf("diversityCap with 2 TLDs: wanted <= 8 per TLD, got com=%d io=%d", comCount, ioCount)
	}

	// 4+ TLDs: cap at 25% -> max 3 for count 10
	pool4 := append(makePool("com", 10), makePool("io", 10)...)
	pool4 = append(pool4, makePool("ai", 10)...)
	pool4 = append(pool4, makePool("app", 10)...)
	got4 := diversityCap(pool4, 10, 4)
	counts := make(map[string]int)
	for _, sc := range got4 {
		counts[sc.TLD]++
	}
	for tld, c := range counts {
		if c > 3 {
			t.Errorf("diversityCap with 4 TLDs: wanted <= 3 for %s, got %d", tld, c)
		}
	}
}

func TestTierBalanceSortsCorrectly(t *testing.T) {
	pool := []scorer.ScoredCandidate{
		{Candidate: algorithmic.Candidate{SLD: "a", TLD: "com", Source: "llm"}, Score: 0.5},
		{Candidate: algorithmic.Candidate{SLD: "b", TLD: "com", Source: "llm"}, Score: 0.9},
		{Candidate: algorithmic.Candidate{SLD: "c", TLD: "com", Source: "algo"}, Score: 0.8},
		{Candidate: algorithmic.Candidate{SLD: "d", TLD: "com", Source: "algo"}, Score: 0.3},
	}
	out := tierBalance(pool, 4, 0.5)
	for i := 1; i < len(out); i++ {
		if out[i].Score > out[i-1].Score {
			t.Errorf("tierBalance output not sorted descending: %v > %v", out[i].Score, out[i-1].Score)
		}
	}
}

func TestCheckAvailabilityFlagDisabledByDefault(t *testing.T) {
	h := newTestHandler(t)
	h.dnsLookup = func(ctx context.Context, name string) dnscheck.Outcome {
		return dnscheck.Free
	}
	w := post(h, `{"input":"coffee shop"}`, "")
	var resp SuggestResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Suggestions) > 0 && resp.Suggestions[0].Available != nil {
		t.Error("expected available field to be nil when check_availability is false")
	}
}

func TestCheckAvailabilityFlagEnabled(t *testing.T) {
	h := newTestHandler(t)
	h.dnsLookup = func(ctx context.Context, name string) dnscheck.Outcome {
		return dnscheck.Free
	}
	w := post(h, `{"input":"coffee shop", "check_availability": true}`, "")
	var resp SuggestResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Suggestions) > 0 {
		if resp.Suggestions[0].Available == nil {
			t.Fatal("expected available field to be non-nil when check_availability is true")
		}
		if !*resp.Suggestions[0].Available {
			t.Errorf("expected available to be true, got %v", *resp.Suggestions[0].Available)
		}
	}
}

func TestStreamSuggestWithoutDNSCheck(t *testing.T) {
	h := newTestHandler(t)
	dnsCalled := false
	h.dnsLookup = func(ctx context.Context, name string) dnscheck.Outcome {
		dnsCalled = true
		return dnscheck.Free
	}

	req := httptest.NewRequest(http.MethodPost, "/suggest/stream", strings.NewReader(`{"input":"coffee shop","check_availability":false}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("want Content-Type text/event-stream, got %s", ct)
	}
	body := w.Body.String()
	if !strings.Contains(body, "event: suggestions") {
		t.Error("expected suggestions event in stream output")
	}
	if !strings.Contains(body, "event: done") {
		t.Error("expected done event in stream output")
	}
	if strings.Contains(body, "event: availability") {
		t.Error("did not expect availability events when check_availability is false")
	}
	if dnsCalled {
		t.Error("dnsLookup should never be called when check_availability is false")
	}
}

func TestStreamSuggestWithDNSCheckEnabled(t *testing.T) {
	h := newTestHandler(t)
	h.dnsLookup = func(ctx context.Context, name string) dnscheck.Outcome {
		return dnscheck.Free
	}

	req := httptest.NewRequest(http.MethodPost, "/suggest/stream", strings.NewReader(`{"input":"coffee shop","check_availability":true}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "event: suggestions") {
		t.Error("expected suggestions event in stream output")
	}
	if !strings.Contains(body, "event: availability") {
		t.Error("expected availability events when check_availability is true")
	}
	if !strings.Contains(body, "event: done") {
		t.Error("expected done event in stream output")
	}
}

func TestStreamSuggestViaAcceptHeader(t *testing.T) {
	h := newTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/suggest", strings.NewReader(`{"input":"coffee shop"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("want Content-Type text/event-stream, got %s", ct)
	}
	body := w.Body.String()
	if !strings.Contains(body, "event: suggestions") || !strings.Contains(body, "event: done") {
		t.Errorf("unexpected body format: %s", body)
	}
}

func TestConfigCustomLLMVariants(t *testing.T) {
	cfg := Config{
		GeminiAPIKey: "test-key",
		GeminiModel:  "test-model",
		LLMVariants:  []string{"evocative"},
	}
	h, err := NewHandler(cfg)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	w := get(h, "/config")
	var resp ConfigResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.LLM.Variants) != 1 || resp.LLM.Variants[0] != "evocative" {
		t.Errorf("expected variants ['evocative'], got %v", resp.LLM.Variants)
	}
}

func TestSuggestRequestInvalidVariants(t *testing.T) {
	h := newTestHandler(t)
	w := post(h, `{"input":"coffee shop","variants":["bad_variant"]}`, "")
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400 for invalid variants, got %d", w.Code)
	}
}


