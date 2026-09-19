package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadHistoryMissingFileIsEmpty(t *testing.T) {
	hist, err := loadHistory(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 0 {
		t.Errorf("hist = %+v, want empty", hist)
	}
}

func TestLoadHistoryRejectsBadRating(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.json")
	if err := os.WriteFile(path, []byte(`[{"query":"q","domain":"d.com","rating":"great","round":1}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadHistory(path); err == nil {
		t.Error("expected error for invalid rating value")
	}
}

func TestSamplePrefillsAndHidesHistory(t *testing.T) {
	pool := testPool()
	chosen := sample(pool, 12, 1)
	if len(chosen) < 2 {
		t.Fatalf("need at least 2 chosen entries, got %d", len(chosen))
	}
	hist := map[[2]string]historyEntry{
		{chosen[0].Query, chosen[0].Domain}: {Query: chosen[0].Query, Domain: chosen[0].Domain, Rating: "good", Round: 1},
		{chosen[1].Query, chosen[1].Domain}: {Query: chosen[1].Query, Domain: chosen[1].Domain, Rating: "bad", Round: 2},
	}
	keyEntries, visible, n := prefill(chosen, hist)
	if n != 2 {
		t.Fatalf("n = %d, want 2", n)
	}
	if len(keyEntries) != len(chosen) {
		t.Fatalf("keyEntries len = %d, want %d", len(keyEntries), len(chosen))
	}
	if keyEntries[0].Prefilled != "good" {
		t.Errorf("keyEntries[0].Prefilled = %q, want good", keyEntries[0].Prefilled)
	}
	if keyEntries[1].Prefilled != "bad" {
		t.Errorf("keyEntries[1].Prefilled = %q, want bad", keyEntries[1].Prefilled)
	}
	for i := 2; i < len(keyEntries); i++ {
		if keyEntries[i].Prefilled != "" {
			t.Errorf("entry %d unexpectedly prefilled: %+v", i, keyEntries[i])
		}
	}
	if len(visible) != len(chosen)-2 {
		t.Errorf("visible len = %d, want %d", len(visible), len(chosen)-2)
	}
	for _, it := range visible {
		if it.ID == chosen[0].ID || it.ID == chosen[1].ID {
			t.Errorf("prefilled entry %s leaked into visible items", it.ID)
		}
	}
}

// grainPool has three TLD variants of "grain" for q1, one shared variant for
// q2, and unrelated unique names, for testing the one-variant-per-name rule.
func grainPool() []keyEntry {
	pool := []keyEntry{
		{Query: "q1", Domain: "grain.pub"},
		{Query: "q1", Domain: "grain.market"},
		{Query: "q1", Domain: "grain.co"},
		{Query: "q2", Domain: "grain.pub"},
		{Query: "q2", Domain: "grain.market"},
	}
	for i := 0; i < 20; i++ {
		pool = append(pool, keyEntry{Query: fmt.Sprintf("u%d", i), Domain: fmt.Sprintf("uniq%d.com", i)})
	}
	return pool
}

func TestSampleOneVariantPerName(t *testing.T) {
	pool := grainPool()
	chosen := sample(pool, len(pool), 1)
	countByQuery := map[string]int{}
	for _, e := range chosen {
		if sldOf(e.Domain) == "grain" {
			countByQuery[e.Query]++
		}
	}
	for q, c := range countByQuery {
		if c > 1 {
			t.Errorf("query %q has %d grain variants chosen, want at most 1", q, c)
		}
	}
	if len(countByQuery) == 0 {
		t.Error("no grain entries were chosen at all; dedupe should not eliminate every variant")
	}
}

func TestSampleByModelOneVariantPerName(t *testing.T) {
	pool := []keyEntry{
		{Query: "q1", Domain: "grain.pub", Models: []string{"m1"}},
		{Query: "q1", Domain: "grain.co", Models: []string{"m1"}},
		{Query: "q1", Domain: "grain.market", Models: []string{"m2"}},
		{Query: "q2", Domain: "grain.pub", Models: []string{"m1"}},
		{Query: "q2", Domain: "grain.market", Models: []string{"m2"}},
	}
	for i := 0; i < 20; i++ {
		pool = append(pool, keyEntry{Query: fmt.Sprintf("u%d", i), Domain: fmt.Sprintf("a%d.com", i), Models: []string{"m1"}})
		pool = append(pool, keyEntry{Query: fmt.Sprintf("v%d", i), Domain: fmt.Sprintf("b%d.com", i), Models: []string{"m2"}})
	}
	chosen := sampleByModel(pool, 44, 1)
	countByQuery := map[string]int{}
	for _, e := range chosen {
		if len(e.Models) != 1 {
			t.Fatalf("shared name sampled: %+v", e)
		}
		if sldOf(e.Domain) == "grain" {
			countByQuery[e.Query]++
		}
	}
	for q, c := range countByQuery {
		if c > 1 {
			t.Errorf("query %q has %d grain variants chosen, want at most 1", q, c)
		}
	}
	if len(countByQuery) == 0 {
		t.Error("no grain entries were chosen at all; dedupe should not eliminate every variant")
	}
}

func TestAnalyzeUsesPrefilled(t *testing.T) {
	key := []keyEntry{
		{ID: "r001", Query: "q", Domain: "a.com", Prefilled: "good"},
		{ID: "r002", Query: "q", Domain: "b.com", Prefilled: "bad"},
		{ID: "r003", Query: "q", Domain: "c.com"},
	}
	ratings := []rating{{ID: "r002", Rating: "okay"}, {ID: "r003", Rating: "good"}}
	merged, n := withPrefilled(key, ratings)
	if n != 1 {
		t.Fatalf("withPrefilled count = %d, want 1", n)
	}
	rows, err := join(key, merged)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]float64{}
	for _, r := range rows {
		got[r.e.Domain] = r.value
	}
	if got["a.com"] != ratingValue["good"] {
		t.Errorf("a.com = %v, want prefilled good (%v)", got["a.com"], ratingValue["good"])
	}
	if got["b.com"] != ratingValue["okay"] {
		t.Errorf("b.com = %v, want ratings.json okay (%v), which wins over prefilled bad", got["b.com"], ratingValue["okay"])
	}
	if got["c.com"] != ratingValue["good"] {
		t.Errorf("c.com = %v, want rated good (%v)", got["c.com"], ratingValue["good"])
	}
}

func TestHistoryAdd(t *testing.T) {
	dir := t.TempDir()
	key := []keyEntry{
		{ID: "r001", Query: "q1", Domain: "a.com"},
		{ID: "r002", Query: "q1", Domain: "b.com"},
		{ID: "r003", Query: "q2", Domain: "c.com"},
	}
	ratings := []rating{
		{ID: "r001", Rating: "good"},
		{ID: "r002", Rating: "bad"},
		{ID: "r003", Rating: "okay"},
	}
	if err := writeJSON(filepath.Join(dir, "key.json"), key); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(dir, "ratings.json"), ratings); err != nil {
		t.Fatal(err)
	}
	histPath := filepath.Join(dir, "history.json")
	existing := []historyEntry{{Query: "q1", Domain: "b.com", Rating: "good", Round: 1}}
	if err := writeJSON(histPath, existing); err != nil {
		t.Fatal(err)
	}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	err := runHistoryAdd([]string{"-dir", dir, "-history", histPath, "-round", "2"})
	w.Close()
	os.Stdout = old
	data, _ := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	out := string(data)
	if !strings.Contains(out, "changed: q1 | b.com: good -> bad") {
		t.Errorf("missing changed line, got: %s", out)
	}
	if !strings.Contains(out, "2 added") && !strings.Contains(out, "Added 2") {
		t.Errorf("missing added count in output: %s", out)
	}

	var got []historyEntry
	if err := readJSON(histPath, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("history = %+v, want 3 entries", got)
	}
	for i := 1; i < len(got); i++ {
		prevKey := got[i-1].Query + "\x00" + got[i-1].Domain
		curKey := got[i].Query + "\x00" + got[i].Domain
		if prevKey >= curKey {
			t.Errorf("history not sorted by (query, domain) at %d: %+v", i, got)
		}
	}
	byKey := map[[2]string]historyEntry{}
	for _, e := range got {
		byKey[[2]string{e.Query, e.Domain}] = e
	}
	if e := byKey[[2]string{"q1", "a.com"}]; e.Rating != "good" || e.Round != 2 {
		t.Errorf("a.com not added correctly: %+v", e)
	}
	if e := byKey[[2]string{"q1", "b.com"}]; e.Rating != "bad" || e.Round != 2 {
		t.Errorf("b.com not changed correctly: %+v", e)
	}
	if e := byKey[[2]string{"q2", "c.com"}]; e.Rating != "okay" || e.Round != 2 {
		t.Errorf("c.com not added correctly: %+v", e)
	}
}
