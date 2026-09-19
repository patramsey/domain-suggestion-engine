package main

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/patlivet/domain-suggestion-engine/internal/llm"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// --- parseConfig ---

func TestParseConfigDefaults(t *testing.T) {
	cfg, err := parseConfig(nil, env(nil))
	if err != nil {
		t.Fatal(err)
	}
	want := evalConfig{Model: "gemini-3.5-flash-lite", ThinkingLevel: "minimal", Runs: 1, QuerySet: "core", VariantFilter: "current", Resolver: "1.1.1.1:53"}
	if cfg != want {
		t.Errorf("cfg = %+v, want %+v", cfg, want)
	}
}

func TestParseConfigModelFromEnvFlagWins(t *testing.T) {
	cfg, err := parseConfig(nil, env(map[string]string{"GEMINI_MODEL": "gemini-3.5-flash-lite"}))
	if err != nil || cfg.Model != "gemini-3.5-flash-lite" {
		t.Errorf("env fallback: cfg=%+v err=%v", cfg, err)
	}
	cfg, err = parseConfig([]string{"-model", "gemini-x"}, env(map[string]string{"GEMINI_MODEL": "gemini-3.5-flash-lite"}))
	if err != nil || cfg.Model != "gemini-x" {
		t.Errorf("flag should override env: cfg=%+v err=%v", cfg, err)
	}
}

func TestParseConfigAllFlags(t *testing.T) {
	cfg, err := parseConfig([]string{"-thinking", "low", "-temperature", "0.7", "-runs", "3", "-queries", "all", "-label", "temp-0.7"}, env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ThinkingLevel != "low" || cfg.Temperature != 0.7 || cfg.Runs != 3 || cfg.QuerySet != "all" || cfg.Label != "temp-0.7" {
		t.Errorf("cfg = %+v", cfg)
	}
}

func TestParseConfigRejectsBadValues(t *testing.T) {
	for _, args := range [][]string{
		{"-runs", "0"},
		{"-queries", "nope"},
		{"-thinking", "extreme"},
		{"-temperature", "-1"},
		{"-temperature", "2.5"},
		{"-label", "has space"},
		{"-label", "../escape"},
	} {
		if _, err := parseConfig(args, env(nil)); err == nil {
			t.Errorf("parseConfig(%v) should fail", args)
		}
	}
}

func TestParseConfigVariantDefault(t *testing.T) {
	cfg, err := parseConfig(nil, env(nil))
	if err != nil || cfg.VariantFilter != "current" {
		t.Errorf("cfg=%+v err=%v", cfg, err)
	}
}

func TestSelectVariants(t *testing.T) {
	all := []promptVariant{{name: "current"}, {name: "r1"}, {name: "r2"}}
	got, err := selectVariants(all, "r2,current")
	if err != nil || len(got) != 2 || got[0].name != "current" || got[1].name != "r2" {
		t.Errorf("got %+v err=%v (want definition order current, r2)", got, err)
	}
	if got, err := selectVariants(all, "all"); err != nil || len(got) != 3 {
		t.Errorf("all: got %d err=%v", len(got), err)
	}
	if _, err := selectVariants(all, "nope"); err == nil {
		t.Error("unknown variant should fail")
	}
	if _, err := selectVariants(all, ""); err == nil {
		t.Error("empty filter should fail")
	}
}

// --- pricing ---

func TestEstimateCostKnownModelBillsThoughtsAsOutput(t *testing.T) {
	u := llm.TokenUsage{PromptTokens: 1_000_000, CandidateTokens: 500_000, ThoughtsTokens: 500_000}
	cost, ok := estimateCost("gemini-3.1-flash-lite", u)
	if !ok {
		t.Fatal("3.1-flash-lite should be priced")
	}
	// $0.25 input + 1M output tokens × $1.50
	if math.Abs(cost-1.75) > 1e-9 {
		t.Errorf("cost = %v, want 1.75", cost)
	}
}

func TestEstimateCost35(t *testing.T) {
	cost, ok := estimateCost("gemini-3.5-flash-lite", llm.TokenUsage{PromptTokens: 1_000_000, CandidateTokens: 1_000_000})
	if !ok || math.Abs(cost-2.80) > 1e-9 {
		t.Errorf("cost = %v ok=%v, want 2.80", cost, ok)
	}
}

func TestEstimateCostUnknownModel(t *testing.T) {
	if _, ok := estimateCost("gemini-9-ultra", llm.TokenUsage{PromptTokens: 10}); ok {
		t.Error("unknown model should not be priced")
	}
}

// --- snapshot naming ---

func TestSnapshotNameIncludesMillisAndLabel(t *testing.T) {
	ts := time.Date(2026, 9, 18, 23, 12, 3, 45_000_000, time.UTC)
	if got := snapshotName(ts, "temp-0.7"); got != "eval-results/run-2026-09-18T231203.045-temp-0.7.json" {
		t.Errorf("got %q", got)
	}
	if got := snapshotName(ts, ""); got != "eval-results/run-2026-09-18T231203.045.json" {
		t.Errorf("got %q", got)
	}
}

// --- snapshot config ---

func TestSnapshotConfigRecordsPromptAndSettings(t *testing.T) {
	cfg := evalConfig{Model: "m", ThinkingLevel: "low", Temperature: 0.7, Runs: 2, QuerySet: "core", Label: "x"}
	sc := buildSnapshotConfig(cfg, []promptVariant{{name: "current", system: llm.SystemPrompt, temperature: 1.0}})
	if sc.Model != "m" || sc.ThinkingLevel != "low" || sc.Runs != 2 || sc.QuerySet != "core" || sc.Label != "x" {
		t.Errorf("settings not recorded: %+v", sc)
	}
	v := sc.Variants["current"]
	if v.Temperature != 0.7 {
		t.Errorf("temperature flag should override variant default: %+v", v)
	}
	if v.PromptFingerprint != llm.PromptFingerprint(llm.SystemPrompt, nil) {
		t.Errorf("fingerprint = %q", v.PromptFingerprint)
	}
	if sc.GitCommit == "" || strings.Contains(sc.GitCommit, "\n") {
		t.Errorf("git commit = %q", sc.GitCommit)
	}
}

func TestSnapshotConfigUsesVariantTemperatureWhenFlagUnset(t *testing.T) {
	sc := buildSnapshotConfig(evalConfig{Model: "m", ThinkingLevel: "minimal", Runs: 1, QuerySet: "core"},
		[]promptVariant{{name: "current", system: "s", temperature: 1.0}})
	if sc.Variants["current"].Temperature != 1.0 {
		t.Errorf("got %+v", sc.Variants["current"])
	}
}

func TestParseConfigRescore(t *testing.T) {
	cfg, err := parseConfig([]string{"-rescore", "eval-results/run-x.json"}, env(nil))
	if err != nil || cfg.Rescore != "eval-results/run-x.json" {
		t.Errorf("cfg=%+v err=%v", cfg, err)
	}
}

func TestCandidateVariantsWellFormed(t *testing.T) {
	names := map[string]promptVariant{}
	for _, v := range allVariants {
		if _, dup := names[v.name]; dup {
			t.Fatalf("duplicate variant %q", v.name)
		}
		names[v.name] = v
		if v.variantOverrides != nil && len(v.variantOverrides) != 3 {
			t.Errorf("%s: variantOverrides must be len 3, got %d", v.name, len(v.variantOverrides))
		}
	}
	for _, want := range []string{"current", "r1-briefs", "r2-uncommon", "r3-briefs"} {
		if _, ok := names[want]; !ok {
			t.Errorf("missing variant %q", want)
		}
	}
	r1, r2 := names["r1-briefs"], names["r2-uncommon"]
	if !strings.HasPrefix(r1.system, llm.SystemPrompt) || !strings.HasPrefix(r2.system, r1.system) {
		t.Error("r1 must extend the current system prompt, and r2 must extend r1")
	}
	for i, b := range separatedBriefs {
		if !strings.HasPrefix(b, "\n\n") {
			t.Errorf("brief %d must start with a blank line like the existing variant instructions", i)
		}
	}
}

func TestR3BriefsReplacesOnlyCraftedBrief(t *testing.T) {
	var r1, r3 promptVariant
	for _, v := range allVariants {
		switch v.name {
		case "r1-briefs":
			r1 = v
		case "r3-briefs":
			r3 = v
		}
	}
	if r3.name == "" {
		t.Fatal("missing variant r3-briefs")
	}
	if r3.system != r1.system || r3.temperature != r1.temperature {
		t.Error("r3-briefs must keep r1-briefs' system prompt and temperature")
	}
	if len(r3.variantOverrides) != 3 {
		t.Fatalf("r3-briefs overrides len = %d, want 3", len(r3.variantOverrides))
	}
	if r3.variantOverrides[0] != r1.variantOverrides[0] || r3.variantOverrides[1] != r1.variantOverrides[1] {
		t.Error("r3-briefs must keep the evocative and wordplay briefs unchanged")
	}
	if r3.variantOverrides[2] == r1.variantOverrides[2] {
		t.Error("r3-briefs must replace the crafted brief")
	}
	if strings.Contains(r3.variantOverrides[2], "not one found in a dictionary") {
		t.Error("the new crafted brief must drop the 'new word only' requirement")
	}
	if !strings.HasPrefix(r3.variantOverrides[2], "\n\n") {
		t.Error("the new crafted brief must start with a blank line like the others")
	}
}

func TestCompoundVariants(t *testing.T) {
	got := map[string]promptVariant{}
	for _, v := range allVariants {
		got[v.name] = v
	}
	prod := llm.VariantInstructions()
	c1 := got["c1-grounded"]
	if c1.system != llm.SystemPrompt || len(c1.variantOverrides) != 3 {
		t.Fatalf("c1-grounded: want production system prompt and 3 briefs, got %d briefs", len(c1.variantOverrides))
	}
	if c1.variantOverrides[0] != prod[0] || c1.variantOverrides[1] != prod[1] {
		t.Error("c1-grounded must keep the production evocative and wordplay briefs")
	}
	if c1.variantOverrides[2] != compoundCraftedBrief {
		t.Error("c1-grounded must replace the crafted brief")
	}
	c3 := got["c3-concrete"]
	if c3.system != llm.SystemPrompt || len(c3.variantOverrides) != 3 ||
		c3.variantOverrides[0] != prod[0] || c3.variantOverrides[1] != prod[1] || c3.variantOverrides[2] != concreteCraftedBrief {
		t.Error("c3-concrete must keep production briefs 1–2 and replace the crafted brief")
	}
	c2 := got["c2-mix"]
	if c2.system != llm.SystemPrompt+compoundMix || c2.variantOverrides != nil {
		t.Error("c2-mix: want production briefs and system prompt + compoundMix")
	}
}
