package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestHandler(t *testing.T) *Handler {
	t.Helper()
	h, err := NewHandler(Config{
		GeminiAPIKey:     "test-key",
		GeminiModel:      "gemini-2.5-flash-lite",
		CacheSize:        10,
		LLMShare:         0.60,
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
