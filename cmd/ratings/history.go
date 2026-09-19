package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// historyEntry is one previously rated (query, domain) pair, kept across
// rounds so a name already rated is never shown again for the same query.
// TLD variants of a rated name are unaffected — they key on the full domain.
type historyEntry struct {
	Query  string `json:"query"`
	Domain string `json:"domain"`
	Rating string `json:"rating"`
	Round  int    `json:"round"`
}

// loadHistory reads a rating history file into a map keyed by (query,
// domain). A missing file is not an error: it simply means no history yet.
func loadHistory(path string) (map[[2]string]historyEntry, error) {
	out := map[[2]string]historyEntry{}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	var entries []historyEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	for _, e := range entries {
		if _, ok := ratingValue[e.Rating]; !ok {
			return nil, fmt.Errorf("invalid rating %q for %q | %q in %s", e.Rating, e.Query, e.Domain, path)
		}
		out[[2]string{e.Query, e.Domain}] = e
	}
	return out, nil
}

// withPrefilled returns ratings extended with a synthetic rating for every
// key entry that has a Prefilled value and no real entry in ratings, plus
// how many were added. Ratings already present in ratings win, unchanged.
func withPrefilled(key []keyEntry, ratings []rating) ([]rating, int) {
	rated := make(map[string]bool, len(ratings))
	for _, r := range ratings {
		rated[r.ID] = true
	}
	out := append([]rating(nil), ratings...)
	n := 0
	for _, e := range key {
		if e.Prefilled == "" || rated[e.ID] {
			continue
		}
		out = append(out, rating{ID: e.ID, Rating: e.Prefilled})
		n++
	}
	return out, n
}

// runHistoryAdd reads a rated round's key.json and ratings.json and records
// every rated (query, domain) pair into the history file, creating it if
// missing. An existing entry is kept unless this round rates it differently,
// in which case the new rating replaces it.
func runHistoryAdd(args []string) error {
	fs := flag.NewFlagSet("history-add", flag.ContinueOnError)
	dir := fs.String("dir", "", "directory with key.json and ratings.json")
	histPath := fs.String("history", "", "rating history file to update (created if missing)")
	round := fs.Int("round", -1, "round number to record for added/changed entries")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dir == "" || *histPath == "" || *round < 0 {
		return fmt.Errorf("need -dir DIR, -history FILE, and -round N")
	}

	var key []keyEntry
	var ratings []rating
	if err := readJSON(filepath.Join(*dir, "key.json"), &key); err != nil {
		return err
	}
	if err := readJSON(filepath.Join(*dir, "ratings.json"), &ratings); err != nil {
		return err
	}
	byID := make(map[string]keyEntry, len(key))
	for _, e := range key {
		byID[e.ID] = e
	}

	hist, err := loadHistory(*histPath)
	if err != nil {
		return err
	}

	added, changed := 0, 0
	for _, r := range ratings {
		if _, ok := ratingValue[r.Rating]; !ok {
			return fmt.Errorf("unknown rating %q for %s", r.Rating, r.ID)
		}
		e, ok := byID[r.ID]
		if !ok {
			continue
		}
		k := [2]string{e.Query, e.Domain}
		if old, ok := hist[k]; ok {
			if old.Rating == r.Rating {
				continue
			}
			fmt.Printf("changed: %s | %s: %s -> %s\n", e.Query, e.Domain, old.Rating, r.Rating)
			hist[k] = historyEntry{Query: e.Query, Domain: e.Domain, Rating: r.Rating, Round: *round}
			changed++
			continue
		}
		hist[k] = historyEntry{Query: e.Query, Domain: e.Domain, Rating: r.Rating, Round: *round}
		added++
	}

	out := make([]historyEntry, 0, len(hist))
	for _, e := range hist {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Query != out[j].Query {
			return out[i].Query < out[j].Query
		}
		return out[i].Domain < out[j].Domain
	})
	if err := writeJSON(*histPath, out); err != nil {
		return err
	}
	fmt.Printf("Added %d, changed %d entries in %s\n", added, changed, *histPath)
	return nil
}
