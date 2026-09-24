package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// item is one name to rate: the same shape cmd/ratings writes to items.json.
type item struct {
	ID     string `json:"id"`
	Query  string `json:"query"`
	Domain string `json:"domain"`
}

// rating is one verdict, the shape cmd/ratings analyze reads.
type rating struct {
	ID     string `json:"id"`
	Rating string `json:"rating"`
}

// systemPrompt mirrors the instructions on the human rating page, so the
// judge is answering the same question the human answered.
const systemPrompt = `You rate candidate domain names for a business. For each name, answer: would this be a good name for the business described?

Judge the name itself — its sound, memorability, and fit to the concept. Assume every name is available to register; never mark a name down for looking taken or for being a common word. Ignore which system produced it.

Ratings:
- "good": you would put this in front of the founder — it fits the concept and reads like a real brand.
- "okay": nothing wrong with it, but generic, forgettable, or a loose fit.
- "bad": a misspelling, a nonsense coinage, misleading, or embarrassing.

Output ONLY a JSON array of {"id","rating"} objects, one per name given, no prose.`

// batch splits items into groups of at most size.
func batch(items []item, size int) [][]item {
	var out [][]item
	for i := 0; i < len(items); i += size {
		out = append(out, items[i:min(i+size, len(items))])
	}
	return out
}

// userMessage lists the names to rate as JSON.
func userMessage(items []item) string {
	rows := make([]string, len(items))
	for i, it := range items {
		rows[i] = fmt.Sprintf(`{"id": %q, "business": %q, "domain": %q}`, it.ID, it.Query, it.Domain)
	}
	return fmt.Sprintf("Rate these %d domain names:\n[\n%s\n]", len(items), strings.Join(rows, ",\n"))
}

// examplesBlock shows the judge how this rater actually rates, so it can
// calibrate where the good/okay line sits instead of guessing. Examples must
// be held out of whatever set the judge is then measured on.
func examplesBlock(ex []item, ratings map[string]string) string {
	if len(ex) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\nThis rater has already judged these names. Match their standard, including how readily they say \"good\":\n")
	for _, it := range ex {
		fmt.Fprintf(&b, "- %s for \"%s\" → %s\n", it.Domain, it.Query, ratings[it.ID])
	}
	return b.String()
}

var validRatings = map[string]bool{"good": true, "okay": true, "bad": true}

// parseRatings reads the model's reply into id → rating, tolerating code
// fences or prose around the array. An unknown rating value is an error: a
// silently dropped verdict would bias the agreement numbers.
func parseRatings(text string) (map[string]string, error) {
	start, end := strings.Index(text, "["), strings.LastIndex(text, "]")
	if start < 0 || end < start {
		return nil, fmt.Errorf("no JSON array in reply: %s", truncate(text, 120))
	}
	var rows []rating
	if err := json.Unmarshal([]byte(text[start:end+1]), &rows); err != nil {
		return nil, fmt.Errorf("parse reply: %w", err)
	}
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		v := strings.ToLower(strings.TrimSpace(r.Rating))
		if !validRatings[v] {
			return nil, fmt.Errorf("unknown rating %q for %s", r.Rating, r.ID)
		}
		out[r.ID] = v
	}
	return out, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
