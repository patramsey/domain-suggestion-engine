package main

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestSpearmanPerfectAndTies(t *testing.T) {
	if r := spearman([]float64{1, 2, 3, 4}, []float64{10, 20, 30, 40}); math.Abs(r-1) > 1e-9 {
		t.Errorf("monotone rho = %v, want 1", r)
	}
	if r := spearman([]float64{1, 2, 3, 4}, []float64{4, 3, 2, 1}); math.Abs(r+1) > 1e-9 {
		t.Errorf("reversed rho = %v, want -1", r)
	}
	if got := ranks([]float64{5, 1, 5}); got[0] != 2.5 || got[1] != 1 || got[2] != 2.5 {
		t.Errorf("ranks with ties = %v, want [2.5 1 2.5]", got)
	}
}

func TestFlagComparisonGoodShare(t *testing.T) {
	var flagged, good []bool
	for i := 0; i < 40; i++ {
		f := i < 10
		flagged = append(flagged, f)
		good = append(good, !f || i == 0) // 1 of 10 flagged good; all 30 others good
	}
	c := compareFlag(flagged, good, 2000, 1)
	if c.nFlagged != 10 || c.nUnflagged != 30 {
		t.Fatalf("counts = %+v", c)
	}
	if c.goodFlagged != 0.1 || c.goodUnflagged != 1.0 {
		t.Errorf("shares = %+v", c)
	}
	if c.p >= 0.01 {
		t.Errorf("p = %v, want < 0.01", c.p)
	}
}

func TestCompareModels(t *testing.T) {
	var rows []row
	for i := 0; i < 20; i++ {
		v := 1.0 // bad
		if i < 18 {
			v = 3 // good
		}
		rows = append(rows, row{e: keyEntry{Models: []string{"m-new"}}, value: v})
	}
	for i := 0; i < 20; i++ {
		v := 1.0
		if i < 10 {
			v = 3
		}
		rows = append(rows, row{e: keyEntry{Models: []string{"m-old"}}, value: v})
	}
	rows = append(rows, row{e: keyEntry{Models: []string{"m-new", "m-old"}}, value: 3}) // shared: excluded
	got := compareModels(rows)
	if len(got) != 2 {
		t.Fatalf("want 2 models, got %+v", got)
	}
	byName := map[string]modelShare{got[0].model: got[0], got[1].model: got[1]}
	if byName["m-new"].n != 20 || byName["m-new"].good != 0.9 {
		t.Errorf("m-new = %+v", byName["m-new"])
	}
	if byName["m-old"].n != 20 || byName["m-old"].good != 0.5 {
		t.Errorf("m-old = %+v", byName["m-old"])
	}
}

func TestVerdict(t *testing.T) {
	cases := []struct {
		name   string
		agrees bool
		p      float64
		enough bool
		want   string
	}{
		{"agrees: right direction, significant", true, 0.01, true, "AGREES with your ratings"},
		{"too few: not enough data regardless of stats", true, 0.01, false, "TOO FEW rated names to judge"},
		{"disagrees: right direction but p >= 0.05", true, 0.2, true, "DOES NOT AGREE with your ratings"},
		{"disagrees: p < 0.05 but wrong direction", false, 0.01, true, "DOES NOT AGREE with your ratings"},
	}
	for _, c := range cases {
		if got := verdict(c.agrees, c.p, c.enough); got != c.want {
			t.Errorf("%s: verdict(%v, %v, %v) = %q, want %q", c.name, c.agrees, c.p, c.enough, got, c.want)
		}
	}
}

func TestSldOf(t *testing.T) {
	for domain, want := range map[string]string{
		"mat.guru":    "mat",
		"Foo.Bar.com": "foo",
		"noDot":       "nodot",
	} {
		if got := sldOf(domain); got != want {
			t.Errorf("sldOf(%q) = %q, want %q", domain, got, want)
		}
	}
}

// The word list changes over time (see internal/wordlist), so analyze must
// recompute Typo/CommonWord from the current word list rather than trust the
// value stored in key.json at sample time.
func TestAnalyzeRecomputesFlags(t *testing.T) {
	dir := t.TempDir()
	key := []keyEntry{
		{ID: "r001", Query: "pizza place", Domain: "pizzaria.pizza", Typo: false},
	}
	ratings := []rating{{ID: "r001", Rating: "bad"}}
	if err := writeJSON(filepath.Join(dir, "key.json"), key); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(dir, "ratings.json"), ratings); err != nil {
		t.Fatal(err)
	}

	var gotKey []keyEntry
	var gotRatings []rating
	if err := readJSON(filepath.Join(dir, "key.json"), &gotKey); err != nil {
		t.Fatal(err)
	}
	if err := readJSON(filepath.Join(dir, "ratings.json"), &gotRatings); err != nil {
		t.Fatal(err)
	}
	rows, err := join(gotKey, gotRatings)
	if err != nil {
		t.Fatal(err)
	}
	rows = recomputeFlags(rows)
	if len(rows) != 1 {
		t.Fatalf("rows = %+v", rows)
	}
	if !rows[0].e.Typo {
		t.Errorf("recomputeFlags did not flag pizzaria.pizza as a typo: %+v", rows[0].e)
	}

	// os and json are used only to sanity-check the files this test wrote.
	data, err := os.ReadFile(filepath.Join(dir, "key.json"))
	if err != nil {
		t.Fatal(err)
	}
	var raw []map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if raw[0]["typo"] != false {
		t.Errorf("key.json on disk should keep its original stored value: %v", raw[0]["typo"])
	}
}

func TestJoinRatingsIgnoresUnknownAndUnrated(t *testing.T) {
	key := []keyEntry{{ID: "r001"}, {ID: "r002"}, {ID: "r003"}}
	ratings := []rating{{ID: "r001", Rating: "good"}, {ID: "r003", Rating: "bad"}, {ID: "zzz", Rating: "okay"}}
	rows, err := join(key, ratings)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].value != 3 || rows[1].value != 1 {
		t.Errorf("rows = %+v", rows)
	}
	if _, err := join(key, []rating{{ID: "r001", Rating: "great"}}); err == nil {
		t.Error("expected error for unknown rating value")
	}
}
