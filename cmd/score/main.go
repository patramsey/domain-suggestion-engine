package main

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/patlivet/domain-suggestion-engine/internal/algorithmic"
	"github.com/patlivet/domain-suggestion-engine/internal/parser"
	"github.com/patlivet/domain-suggestion-engine/internal/scorer"
	"github.com/patlivet/domain-suggestion-engine/internal/tlds"
)

func main() {
	query := flag.String("query", "", "concept to score against (e.g. \"coffee shop denver\")")
	domainsFlag := flag.String("domains", "", "comma-separated domain names (e.g. duskbrew.cafe,roast.coffee)")
	file := flag.String("file", "", "CSV file of domain names (one per line or one per row, first column used)")
	jsonOut := flag.Bool("json", false, "output JSON instead of a table")
	flag.Parse()

	if *query == "" {
		fmt.Fprintln(os.Stderr, "error: --query is required")
		flag.Usage()
		os.Exit(1)
	}
	if *domainsFlag == "" && *file == "" {
		fmt.Fprintln(os.Stderr, "error: one of --domains or --file is required")
		flag.Usage()
		os.Exit(1)
	}
	if *domainsFlag != "" && *file != "" {
		fmt.Fprintln(os.Stderr, "error: --domains and --file are mutually exclusive")
		flag.Usage()
		os.Exit(1)
	}

	var rawDomains []string
	if *domainsFlag != "" {
		for _, d := range strings.Split(*domainsFlag, ",") {
			if d = strings.TrimSpace(d); d != "" {
				rawDomains = append(rawDomains, d)
			}
		}
	} else {
		var err error
		rawDomains, err = readCSV(*file)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error reading file: %v\n", err)
			os.Exit(1)
		}
	}

	icann := tlds.DefaultRegistry.ICANNSet()
	tokens := parser.Parse(*query, icann)

	candidates := make([]algorithmic.Candidate, 0, len(rawDomains))
	for _, d := range rawDomains {
		d = strings.ToLower(strings.TrimSpace(d))
		idx := strings.LastIndex(d, ".")
		if idx <= 0 || idx == len(d)-1 {
			fmt.Fprintf(os.Stderr, "skipping %q: not a valid domain\n", d)
			continue
		}
		candidates = append(candidates, algorithmic.Candidate{
			SLD:    d[:idx],
			TLD:    d[idx+1:],
			Source: "external",
		})
	}

	ranked := scorer.Rank(candidates, tokens)

	if *jsonOut {
		printJSON(ranked)
	} else {
		printTable(ranked)
	}
}

func readCSV(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var domains []string
	r := csv.NewReader(bufio.NewReader(f))
	r.FieldsPerRecord = -1 // allow variable columns
	records, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	for _, row := range records {
		if len(row) > 0 {
			if d := strings.TrimSpace(row[0]); d != "" {
				domains = append(domains, d)
			}
		}
	}
	return domains, nil
}

func printTable(ranked []scorer.ScoredCandidate) {
	fmt.Printf("%-30s  %s\n", "domain", "score")
	fmt.Println(strings.Repeat("-", 40))
	for _, sc := range ranked {
		fmt.Printf("%-30s  %.3f\n", sc.SLD+"."+sc.TLD, sc.Score)
	}
}

func printJSON(ranked []scorer.ScoredCandidate) {
	type row struct {
		Name  string  `json:"name"`
		SLD   string  `json:"sld"`
		TLD   string  `json:"tld"`
		Score float64 `json:"score"`
	}
	out := make([]row, len(ranked))
	for i, sc := range ranked {
		out[i] = row{
			Name:  sc.SLD + "." + sc.TLD,
			SLD:   sc.SLD,
			TLD:   sc.TLD,
			Score: sc.Score,
		}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(out)
}
