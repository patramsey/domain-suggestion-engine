// Generates internal/scorer/tld_scores_gen.go from two public data sources:
//   - Mozilla Public Suffix List (data/tlds/public_suffix_list.dat) — already present
//   - Majestic Million (downloads.majestic.com) — fetched at generation time
//
// Score per TLD = 35% length prior + 40% adoption rank + 25% word-likeness.
//
// Usage: go run ./cmd/gen/tld-scores
package main

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
	"strings"
	"text/template"
	"time"
)

const (
	pslPath     = "data/tlds/public_suffix_list.dat"
	outPath     = "internal/scorer/tld_scores_gen.go"
	majesticURL = "https://downloads.majestic.com/majestic_million.csv"
	httpTimeout = 60 * time.Second
)

func main() {
	pslData, err := os.ReadFile(pslPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read PSL: %v\n", err)
		os.Exit(1)
	}
	icannTLDs := parsePSLTLDs(pslData)
	fmt.Printf("Parsed %d ICANN TLDs from PSL\n", len(icannTLDs))

	adoption, adpSource, err := fetchAdoptionCounts()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: adoption data unavailable (%v) — scoring on structure only\n", err)
		adoption = map[string]int{}
		adpSource = "none"
	} else {
		fmt.Printf("Loaded adoption counts for %d TLDs (%s)\n", len(adoption), adpSource)
	}

	scores := computeScores(icannTLDs, adoption)
	fmt.Printf("Computed scores for %d TLDs\n", len(scores))

	if err := writeGenFile(outPath, scores, adpSource); err != nil {
		fmt.Fprintf(os.Stderr, "write output: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Wrote %s\n", outPath)
}

// parsePSLTLDs returns unique single-level ICANN TLDs from the PSL.
// Multi-level entries (co.uk, com.au) and IDN punycode (xn--*) are excluded.
func parsePSLTLDs(data []byte) []string {
	seen := make(map[string]struct{})
	scanner := bufio.NewScanner(bytes.NewReader(data))
	inICANN := false

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.Contains(line, "===BEGIN ICANN DOMAINS===") {
			inICANN = true
			continue
		}
		if strings.Contains(line, "===BEGIN PRIVATE DOMAINS===") {
			break
		}
		if !inICANN || line == "" || strings.HasPrefix(line, "//") || strings.HasPrefix(line, "!") {
			continue
		}
		tld := strings.ToLower(strings.TrimPrefix(line, "*."))
		if strings.Contains(tld, ".") || strings.HasPrefix(tld, "xn--") {
			continue
		}
		// Skip non-ASCII TLDs (unicode IDN entries) — they won't appear in suggestions
		asciiOnly := true
		for _, r := range tld {
			if r > 127 {
				asciiOnly = false
				break
			}
		}
		if !asciiOnly {
			continue
		}
		seen[tld] = struct{}{}
	}

	out := make([]string, 0, len(seen))
	for t := range seen {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// fetchAdoptionCounts downloads Majestic Million and returns per-TLD domain counts.
func fetchAdoptionCounts() (map[string]int, string, error) {
	client := &http.Client{Timeout: httpTimeout}
	resp, err := client.Get(majesticURL)
	if err != nil {
		return nil, "", fmt.Errorf("fetch Majestic: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("Majestic returned HTTP %d", resp.StatusCode)
	}

	counts := make(map[string]int)
	r := csv.NewReader(resp.Body)
	r.LazyQuotes = true
	r.Read() // skip header row
	for {
		row, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil || len(row) < 4 {
			continue
		}
		// column 3 is the TLD
		tld := strings.ToLower(strings.TrimSpace(row[3]))
		if tld != "" && !strings.Contains(tld, ".") {
			counts[tld]++
		}
	}
	return counts, "Majestic Million", nil
}

type tldScore struct {
	TLD   string
	Score float64
}

func computeScores(tlds []string, adoption map[string]int) []tldScore {
	maxCount := 1
	for _, c := range adoption {
		if c > maxCount {
			maxCount = c
		}
	}

	scores := make([]tldScore, 0, len(tlds))
	for _, tld := range tlds {
		lp := lengthPrior(tld)
		wl := wordLikeness(tld)

		var adp float64
		if len(adoption) > 0 {
			adp = math.Log(float64(adoption[tld])+1) / math.Log(float64(maxCount)+1)
		}

		score := 0.35*lp + 0.40*adp + 0.25*wl
		if score < 0.10 {
			score = 0.10
		}
		if score > 0.98 {
			score = 0.98
		}
		scores = append(scores, tldScore{TLD: tld, Score: score})
	}
	return scores
}

// lengthPrior reflects the historical/structural character of a TLD by length.
// 2-char = ccTLD territory; 3-char = legacy gTLD territory; 4+ = new gTLD.
func lengthPrior(tld string) float64 {
	switch len(tld) {
	case 2:
		return 0.45
	case 3:
		return 0.72
	case 4:
		return 0.65
	default:
		return 0.55
	}
}

// wordLikeness scores how "word-like" the TLD string is via vowel ratio and length.
// Word-like TLDs (coffee, studio, app) should score higher than consonant clusters (mg, cd).
func wordLikeness(tld string) float64 {
	n := len(tld)
	if n == 0 {
		return 0
	}
	vowels := 0
	for _, r := range tld {
		switch r {
		case 'a', 'e', 'i', 'o', 'u':
			vowels++
		}
	}
	ratio := float64(vowels) / float64(n)

	var wl float64
	switch {
	case ratio == 0:
		wl = 0.20 // pure consonant cluster (mg, cd, bg)
	case ratio >= 0.25 && ratio <= 0.60:
		wl = 0.90 // ideal English word range (coffee, studio, app)
	case ratio > 0.60:
		wl = 0.65 // mostly/all vowels (io, ai, eu)
	default:
		wl = 0.55 // low but nonzero vowel ratio (com, net, xyz)
	}

	// Short TLDs are abbreviations, not words
	if n <= 3 {
		wl *= 0.75
	} else if n > 15 {
		wl *= 0.80
	}
	return wl
}

var genTemplate = template.Must(template.New("").Parse(`// Code generated by cmd/gen/tld-scores. DO NOT EDIT.
// Sources: Mozilla Public Suffix List + {{.AdpSource}} ({{.Date}})
// Refresh: make gen-tld-scores
package scorer

// tldScores maps every ICANN TLD to a base quality score in [0.10, 0.98].
// Three signals: length prior (35%) + adoption rank (40%) + word-likeness (25%).
var tldScores = map[string]float64{
{{- range .Scores}}
	"{{.TLD}}": {{printf "%.4f" .Score}},
{{- end}}
}
`))

func writeGenFile(path string, scores []tldScore, adpSource string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return genTemplate.Execute(f, struct {
		Date      string
		AdpSource string
		Scores    []tldScore
	}{
		Date:      time.Now().UTC().Format("2006-01-02"),
		AdpSource: adpSource,
		Scores:    scores,
	})
}
