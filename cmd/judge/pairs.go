package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"sort"
	"strings"
)

// pair is one head-to-head question: which of two names suits the business
// better. Better holds the human's preferred name when there is one.
type pair struct {
	ID     string `json:"id"`
	Query  string `json:"query"`
	A      string `json:"a"`
	B      string `json:"b"`
	Better string `json:"better,omitempty"`
}

// pairSystemPrompt asks for a comparison rather than a grade: models rank two
// options far more reliably than they score one in isolation.
const pairSystemPrompt = `You compare candidate domain names for a business. For each pair, say which name would serve the business better.

Judge the names themselves — sound, memorability, and fit to the concept. Assume both are available to register; never prefer one because the other looks taken or is a common word. You must choose one; there are no ties.

Output ONLY a JSON array of {"id","winner"} objects, one per pair, where winner is "a" or "b". No prose.`

// buildPairs makes head-to-head questions from rated names: within a query,
// pair a name the human rated good against one they rated okay or bad, so the
// human's preference is known. perQuery caps how many come from one query.
func buildPairs(entries []historyEntry, perQuery int, seed int64) []pair {
	byQuery := map[string][]historyEntry{}
	var queries []string
	for _, e := range entries {
		if _, ok := byQuery[e.Query]; !ok {
			queries = append(queries, e.Query)
		}
		byQuery[e.Query] = append(byQuery[e.Query], e)
	}
	sort.Strings(queries)
	rng := rand.New(rand.NewSource(seed))

	var out []pair
	for _, q := range queries {
		var good, worse []historyEntry
		for _, e := range byQuery[q] {
			if e.Rating == "good" {
				good = append(good, e)
			} else {
				worse = append(worse, e)
			}
		}
		sort.Slice(good, func(i, j int) bool { return good[i].Domain < good[j].Domain })
		sort.Slice(worse, func(i, j int) bool { return worse[i].Domain < worse[j].Domain })
		rng.Shuffle(len(good), func(i, j int) { good[i], good[j] = good[j], good[i] })
		rng.Shuffle(len(worse), func(i, j int) { worse[i], worse[j] = worse[j], worse[i] })

		if len(good) == 0 {
			continue
		}
		for i := 0; i < min(perQuery, len(worse)); i++ {
			g, w := good[i%len(good)], worse[i]
			p := pair{ID: fmt.Sprintf("p%04d", len(out)), Query: q, A: g.Domain, B: w.Domain, Better: g.Domain}
			if rng.Intn(2) == 1 { // vary which side the better name sits on
				p.A, p.B = p.B, p.A
			}
			out = append(out, p)
		}
	}
	return out
}

// pairExamples shows the judge comparisons this rater has already settled,
// so it can calibrate to their taste. Examples are held out of the measured set.
func pairExamples(ex []pair) string {
	if len(ex) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\nThis rater has already settled these comparisons. Match their taste:\n")
	for _, p := range ex {
		worse := p.A
		if p.Better == p.A {
			worse = p.B
		}
		fmt.Fprintf(&b, "- for \"%s\": %s beats %s\n", p.Query, p.Better, worse)
	}
	return b.String()
}

// swapped returns the same pair with the names the other way round, under the
// id suffixed "r", so each comparison can be asked in both orders.
func swapped(p pair) pair {
	p.ID, p.A, p.B = p.ID+"r", p.B, p.A
	return p
}

func pairMessage(pairs []pair) string {
	rows := make([]string, len(pairs))
	for i, p := range pairs {
		rows[i] = fmt.Sprintf(`{"id": %q, "business": %q, "a": %q, "b": %q}`, p.ID, p.Query, p.A, p.B)
	}
	return fmt.Sprintf("Compare these %d pairs:\n[\n%s\n]", len(pairs), strings.Join(rows, ",\n"))
}

type winnerRow struct {
	ID     string `json:"id"`
	Winner string `json:"winner"`
}

// parseWinners reads the reply into id → "a"/"b".
func parseWinners(text string) (map[string]string, error) {
	start, end := strings.Index(text, "["), strings.LastIndex(text, "]")
	if start < 0 || end < start {
		return nil, fmt.Errorf("no JSON array in reply: %s", truncate(text, 120))
	}
	var rows []winnerRow
	if err := json.Unmarshal([]byte(text[start:end+1]), &rows); err != nil {
		return nil, fmt.Errorf("parse reply: %w", err)
	}
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		w := strings.ToLower(strings.TrimSpace(r.Winner))
		if w != "a" && w != "b" {
			return nil, fmt.Errorf("unknown winner %q for %s", r.Winner, r.ID)
		}
		out[r.ID] = w
	}
	return out, nil
}

// pairResult summarises a pairwise run against known human preferences.
type pairResult struct {
	N          int // pairs answered in both orders
	Correct    int // the human's preferred name won, counting each order
	Consistent int // same name won in both orders
	Answers    int // individual answers counted (up to 2N)
}

func (r pairResult) Accuracy() float64       { return share(r.Correct, r.Answers) }
func (r pairResult) ConsistentRate() float64 { return share(r.Consistent, r.N) }

// scorePairs compares answers with the human's preference. A pair counts only
// when both orders came back.
func scorePairs(pairs []pair, winners map[string]string) pairResult {
	var res pairResult
	for _, p := range pairs {
		fwd, okF := winners[p.ID]
		rev, okR := winners[p.ID+"r"]
		if !okF || !okR {
			continue
		}
		res.N++
		// name chosen in each order
		pickFwd, pickRev := p.A, p.B
		if fwd == "b" {
			pickFwd = p.B
		}
		if rev == "b" {
			pickRev = p.A
		}
		if pickFwd == pickRev {
			res.Consistent++
		}
		for _, pick := range []string{pickFwd, pickRev} {
			res.Answers++
			if pick == p.Better {
				res.Correct++
			}
		}
	}
	return res
}
