// Fits the scorer's signal weights to rated names and reports whether the
// fitted weights order names better than the hand-set ones.
//
// Usage: go run ./cmd/fitweights [-history FILE] [-folds K] [-model basic|full]
//
// The current weights (0.40 brandability, 0.30 concept relevance, 0.15 TLD
// premium, 0.15 length) were chosen by hand. Measured against the human
// ratings they order names no better than chance, so this fits them instead:
// within each query, every name rated good is paired against one rated okay
// or bad, and a pairwise logistic model learns which feature differences
// predict the human's preference. Scoring stays deterministic — only the
// constants would change.
//
// Accuracy is cross-validated by query, so a name never trains and tests on
// the same business. No API calls.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"

	"github.com/patlivet/domain-suggestion-engine/internal/algorithmic"
	"github.com/patlivet/domain-suggestion-engine/internal/parser"
	"github.com/patlivet/domain-suggestion-engine/internal/quality"
	"github.com/patlivet/domain-suggestion-engine/internal/scorer"
	"github.com/patlivet/domain-suggestion-engine/internal/tlds"
)

type historyEntry struct {
	Query  string `json:"query"`
	Domain string `json:"domain"`
	Rating string `json:"rating"`
}

// featureNames and vectorise define the two models under test.
// Three models: basic re-weights today's four signals; full adds everything
// measurable; taste drops tldFreeRate and commonWord, which are availability
// signals the engine already handles with its own penalty — fitting them as
// quality would quietly undo that.
func featureNames(kind string) []string {
	basic := []string{"brandability", "conceptRelevance", "tldPremium", "lengthScore"}
	switch kind {
	case "full":
		return append(basic, "ngram", "memorability", "tldFreeRate", "sldLen", "commonWord", "compound", "typo")
	case "taste":
		return append(basic, "ngram", "memorability", "sldLen", "compound", "typo")
	}
	return basic
}

func vectorise(f scorer.FeatureSet, sld string, kind string) []float64 {
	v := []float64{f.Brandability, f.ConceptRelevance, f.TLDPremium, f.LengthScore}
	switch kind {
	case "full":
		return append(v, f.NGram, f.Memorability, f.TLDFreeRate, float64(f.SLDLen)/14,
			boolVal(f.CommonWord), boolVal(quality.IsCompound(sld)), boolVal(quality.IsTypo(sld)))
	case "taste":
		return append(v, f.NGram, f.Memorability, float64(f.SLDLen)/14,
			boolVal(quality.IsCompound(sld)), boolVal(quality.IsTypo(sld)))
	}
	return v
}

func boolVal(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// handWeights are the weights in scorer.baseScore today, in basic order.
var handWeights = []float64{0.40, 0.30, 0.15, 0.15}

type rated struct {
	query  string
	vec    []float64
	isGood bool
}

func main() {
	history := flag.String("history", "eval-results/ratings/history.json", "rated names")
	folds := flag.Int("folds", 6, "cross-validation folds, split by query")
	modelKind := flag.String("model", "basic", "basic, taste or full — see featureNames")
	epochs := flag.Int("epochs", 4000, "training epochs")
	lr := flag.Float64("lr", 0.5, "learning rate")
	l2 := flag.Float64("l2", 1e-3, "L2 regularisation")
	rescore := flag.String("rescore", "", "eval snapshot to re-rank with the fitted weights")
	out := flag.String("out", "", "where to write the re-ranked snapshot (with -rescore)")
	flag.Parse()

	switch *modelKind {
	case "basic", "taste", "full":
	default:
		fmt.Fprintln(os.Stderr, "-model must be basic, taste or full")
		os.Exit(2)
	}

	raw, err := os.ReadFile(*history)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fitweights: %v\n", err)
		os.Exit(1)
	}
	var entries []historyEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		fmt.Fprintf(os.Stderr, "fitweights: %v\n", err)
		os.Exit(1)
	}

	icann := tlds.DefaultRegistry.ICANNSet()
	var all []rated
	seenQ := map[string]bool{}
	var queries []string
	for _, e := range entries {
		sld, tld, ok := splitDomain(e.Domain)
		if !ok {
			continue
		}
		tokens := parser.Parse(e.Query, icann)
		// Source is unknown for a rated name, so the LLM bonus is left out of
		// the vectors entirely: these models fit the quality signals only.
		f := scorer.Features(algorithmic.Candidate{SLD: sld, TLD: tld}, tokens)
		all = append(all, rated{query: e.Query, vec: vectorise(f, sld, *modelKind), isGood: e.Rating == "good"})
		if !seenQ[e.Query] {
			seenQ[e.Query] = true
			queries = append(queries, e.Query)
		}
	}
	sort.Strings(queries)

	pairsFor := func(keep map[string]bool) [][]float64 {
		byQuery := map[string][]rated{}
		for _, r := range all {
			if keep == nil || keep[r.query] {
				byQuery[r.query] = append(byQuery[r.query], r)
			}
		}
		var out [][]float64
		for _, rs := range byQuery {
			for _, g := range rs {
				if !g.isGood {
					continue
				}
				for _, w := range rs {
					if w.isGood {
						continue
					}
					d := make([]float64, len(g.vec))
					for i := range d {
						d[i] = g.vec[i] - w.vec[i]
					}
					out = append(out, d)
				}
			}
		}
		return out
	}

	names := featureNames(*modelKind)
	allPairs := pairsFor(nil)
	fmt.Printf("%d rated names, %d queries, %d good-vs-worse pairs, %d features\n\n",
		len(all), len(queries), len(allPairs), len(names))

	// Cross-validated accuracy: hand weights vs fitted weights.
	groups := foldsByQuery(queries, *folds)
	var handSum, fitSum float64
	for i, test := range groups {
		inTest := map[string]bool{}
		for _, q := range test {
			inTest[q] = true
		}
		train := map[string]bool{}
		for _, q := range queries {
			if !inTest[q] {
				train[q] = true
			}
		}
		trainPairs, testPairs := pairsFor(train), pairsFor(inTest)
		w := fit(trainPairs, *epochs, *lr, *l2)
		hand := padWeights(handWeights, len(names))
		h, f := pairAccuracy(testPairs, hand), pairAccuracy(testPairs, w)
		handSum += h
		fitSum += f
		fmt.Printf("  fold %d: %3d test pairs   hand %5.1f%%   fitted %5.1f%%\n", i+1, len(testPairs), h*100, f*100)
	}
	n := float64(len(groups))
	fmt.Printf("\n  mean over folds:        hand %5.1f%%   fitted %5.1f%%\n", handSum/n*100, fitSum/n*100)

	// Weights from all the data, for reading and for re-ranking.
	fitted := fit(allPairs, *epochs, *lr, *l2)
	if *rescore != "" {
		if *out == "" {
			fmt.Fprintln(os.Stderr, "-rescore needs -out")
			os.Exit(2)
		}
		if err := rescoreSnapshot(*rescore, *out, fitted, *modelKind); err != nil {
			fmt.Fprintf(os.Stderr, "fitweights: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("\n  re-ranked %s with the fitted weights → %s\n", *rescore, *out)
	}
	w := normalize(fitted)
	fmt.Printf("\n  fitted weights (normalised, all data):\n")
	for i, nm := range names {
		cur := ""
		if i < len(handWeights) {
			cur = fmt.Sprintf("   (currently %.2f)", handWeights[i])
		}
		fmt.Printf("    %-18s %+6.3f%s\n", nm, w[i], cur)
	}
}

// rescoreSnapshot rewrites a snapshot's scores with the fitted model, keeping
// the availability penalty, so the result can be compared head to head with
// the original ranking (judge compare). The LLM position bonus is dropped
// from both sides of the comparison because snapshots do not record LLMRank.
func rescoreSnapshot(path, outPath string, w []float64, kind string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var snap map[string]any
	if err := json.Unmarshal(raw, &snap); err != nil {
		return err
	}
	icann := tlds.DefaultRegistry.ICANNSet()
	results, _ := snap["results"].([]any)
	for _, r := range results {
		res, _ := r.(map[string]any)
		query, _ := res["query"].(string)
		tokens := parser.Parse(query, icann)
		sugs, _ := res["suggestions"].([]any)
		for _, s := range sugs {
			sug, _ := s.(map[string]any)
			sld, _ := sug["sld"].(string)
			tld, _ := sug["tld"].(string)
			f := scorer.Features(algorithmic.Candidate{SLD: sld, TLD: tld}, tokens)
			sug["score"] = dot(w, vectorise(f, sld, kind)) - f.AvailabilityPenalty
		}
	}
	b, err := json.MarshalIndent(snap, "", " ")
	if err != nil {
		return err
	}
	return os.WriteFile(outPath, b, 0o644)
}

// padWeights extends the hand weights with zeros so they can be scored
// against a longer feature vector.
func padWeights(w []float64, n int) []float64 {
	out := make([]float64, n)
	copy(out, w)
	return out
}

// splitDomain splits a domain at its public suffix.
func splitDomain(d string) (sld, tld string, ok bool) {
	icann := tlds.DefaultRegistry.ICANNSet()
	for i := 0; i < len(d); i++ {
		if d[i] != '.' {
			continue
		}
		if _, found := icann[d[i+1:]]; found {
			return d[:i], d[i+1:], true
		}
	}
	return "", "", false
}
