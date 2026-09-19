package main

import (
	"flag"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/patlivet/domain-suggestion-engine/internal/evalset"
	"github.com/patlivet/domain-suggestion-engine/internal/llm"
)

// evalConfig holds the settings for one eval invocation.
type evalConfig struct {
	Model         string
	ThinkingLevel string
	Temperature   float64 // 0 = use each variant's own temperature
	Runs          int
	QuerySet      string
	VariantFilter string // -variant: comma-separated variant names, or "all"
	Label         string
	Rescore       string // snapshot path to re-annotate instead of running the eval
}

var labelRe = regexp.MustCompile(`^[A-Za-z0-9._-]*$`)

var thinkingLevels = map[string]bool{"minimal": true, "low": true, "medium": true, "high": true}

// parseConfig reads flags from args, falling back to GEMINI_MODEL for the model.
func parseConfig(args []string, getenv func(string) string) (evalConfig, error) {
	fs := flag.NewFlagSet("eval", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	model := fs.String("model", "", "Gemini model ID (default $GEMINI_MODEL, else gemini-3.5-flash-lite)")
	thinking := fs.String("thinking", "minimal", "thinkingLevel: minimal, low, medium or high")
	temp := fs.Float64("temperature", 0, "generation temperature, 0–2 (0 = each variant's default)")
	runs := fs.Int("runs", 1, "times to run each query")
	queries := fs.String("queries", "core", "query set: core, hard or all")
	variant := fs.String("variant", "current", "prompt variant(s) to run: comma-separated names, or all")
	label := fs.String("label", "", "short label added to the snapshot filename")
	rescore := fs.String("rescore", "", "re-annotate an existing snapshot with quality metrics (no API calls)")
	if err := fs.Parse(args); err != nil {
		return evalConfig{}, err
	}

	cfg := evalConfig{
		Model:         *model,
		ThinkingLevel: *thinking,
		Temperature:   *temp,
		Runs:          *runs,
		QuerySet:      *queries,
		VariantFilter: *variant,
		Label:         *label,
		Rescore:       *rescore,
	}
	if cfg.Model == "" {
		cfg.Model = getenv("GEMINI_MODEL")
	}
	if cfg.Model == "" {
		cfg.Model = "gemini-3.5-flash-lite"
	}

	if !thinkingLevels[cfg.ThinkingLevel] {
		return evalConfig{}, fmt.Errorf("-thinking %q: want minimal, low, medium or high", cfg.ThinkingLevel)
	}
	if cfg.Temperature < 0 || cfg.Temperature > 2 {
		return evalConfig{}, fmt.Errorf("-temperature %v: want 0–2", cfg.Temperature)
	}
	if cfg.Runs < 1 {
		return evalConfig{}, fmt.Errorf("-runs %d: want at least 1", cfg.Runs)
	}
	if _, err := evalset.Queries(cfg.QuerySet); err != nil {
		return evalConfig{}, fmt.Errorf("-queries: %w", err)
	}
	if _, err := selectVariants(allVariants, cfg.VariantFilter); err != nil {
		return evalConfig{}, err
	}
	if !labelRe.MatchString(cfg.Label) || strings.Contains(cfg.Label, "..") {
		return evalConfig{}, fmt.Errorf("-label %q: use letters, digits, '.', '_' or '-'", cfg.Label)
	}
	return cfg, nil
}

// effectiveTemperature is the temperature a variant actually runs at.
func (c evalConfig) effectiveTemperature(v promptVariant) float64 {
	if c.Temperature > 0 {
		return c.Temperature
	}
	return v.temperature
}

// --- snapshot metadata ---

type snapshotVariant struct {
	Temperature       float64 `json:"temperature"`
	PromptFingerprint string  `json:"prompt_fingerprint"`
}

// snapshotConfig records everything needed to reproduce or interpret a run.
type snapshotConfig struct {
	Model         string                     `json:"model"`
	ThinkingLevel string                     `json:"thinking_level"`
	Runs          int                        `json:"runs"`
	QuerySet      string                     `json:"query_set"`
	Label         string                     `json:"label,omitempty"`
	Variants      map[string]snapshotVariant `json:"variants"`
	GitCommit     string                     `json:"git_commit"`
	GitDirty      bool                       `json:"git_dirty"` // uncommitted code changes (eval-results/ ignored)
}

func buildSnapshotConfig(cfg evalConfig, vs []promptVariant) snapshotConfig {
	sc := snapshotConfig{
		Model:         cfg.Model,
		ThinkingLevel: cfg.ThinkingLevel,
		Runs:          cfg.Runs,
		QuerySet:      cfg.QuerySet,
		Label:         cfg.Label,
		Variants:      make(map[string]snapshotVariant, len(vs)),
	}
	for _, v := range vs {
		sc.Variants[v.name] = snapshotVariant{
			Temperature:       cfg.effectiveTemperature(v),
			PromptFingerprint: llm.PromptFingerprint(v.system, v.variantOverrides),
		}
	}
	sc.GitCommit, sc.GitDirty = gitState()
	return sc
}

// gitState returns the short HEAD commit and whether tracked files outside
// eval-results/ have uncommitted changes. Returns "unknown" outside a repo.
func gitState() (commit string, dirty bool) {
	out, err := exec.Command("git", "rev-parse", "--short=12", "HEAD").Output()
	if err != nil {
		return "unknown", false
	}
	commit = strings.TrimSpace(string(out))
	status, err := exec.Command("git", "status", "--porcelain", "--untracked-files=no",
		"--", ".", ":(exclude)eval-results").Output()
	return commit, err == nil && len(strings.TrimSpace(string(status))) > 0
}

// snapshotName returns a unique snapshot path. Millisecond precision keeps
// back-to-back runs from overwriting each other.
func snapshotName(t time.Time, label string) string {
	name := "eval-results/run-" + t.UTC().Format("2006-01-02T150405.000")
	if label != "" {
		name += "-" + label
	}
	return name + ".json"
}
