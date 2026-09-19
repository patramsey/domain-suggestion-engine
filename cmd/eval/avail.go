package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/patlivet/domain-suggestion-engine/internal/dnscheck"
)

// availTopN is how deep each result is checked: the API's default count.
const availTopN = 20

// availHook returns the DNS annotation step when -avail is set.
func (c evalConfig) availHook() []func(*savedRun) {
	if !c.Avail {
		return nil
	}
	return []func(*savedRun){func(run *savedRun) {
		fmt.Printf("\nChecking DNS for each query's top %d names via %s ...\n", availTopN, c.Resolver)
		annotateDNS(context.Background(), run, dnscheck.Resolver(c.Resolver), 8)
	}}
}

// annotateDNS looks up each result's top availTopN names by score and records
// the outcome on the suggestion ("free", "delegated", or "" when the lookup
// failed). Names below the top availTopN are left unchecked.
func annotateDNS(ctx context.Context, run *savedRun, look dnscheck.Lookup, conc int) {
	var names []string
	for _, r := range run.Results {
		if r.Error != "" {
			continue
		}
		for _, s := range topByScore(r.Suggestions, availTopN) {
			names = append(names, s.Name)
		}
	}
	got := dnscheck.CheckAll(ctx, names, look, conc)
	for i := range run.Results {
		r := &run.Results[i]
		if r.Error != "" {
			continue
		}
		top := map[string]bool{}
		for _, s := range topByScore(r.Suggestions, availTopN) {
			top[s.Name] = true
		}
		for j := range r.Suggestions {
			s := &r.Suggestions[j]
			if top[s.Name] {
				s.DNS = got[s.Name].String()
			}
		}
	}
}

// availStats counts DNS outcomes over each result's top n names.
type availStats struct {
	Free, Known, Unknown int // Known = free + delegated; Unknown = failed or unchecked
	Queries, QueriesNone int // results counted; results with no free name in their top n
}

// Rate is the likely-registrable share of names with a known outcome.
func (a availStats) Rate() float64 {
	if a.Known == 0 {
		return 0
	}
	return float64(a.Free) / float64(a.Known)
}

// PerQuery is the mean number of likely-registrable names per result.
func (a availStats) PerQuery() float64 {
	if a.Queries == 0 {
		return 0
	}
	return float64(a.Free) / float64(a.Queries)
}

func availSummary(results []savedQueryResult, n int) availStats {
	var st availStats
	for _, r := range results {
		if r.Error != "" {
			continue
		}
		st.Queries++
		free := 0
		for _, s := range topByScore(r.Suggestions, n) {
			switch s.DNS {
			case "free":
				free++
				st.Known++
			case "delegated":
				st.Known++
			default:
				st.Unknown++
			}
		}
		st.Free += free
		if free == 0 {
			st.QueriesNone++
		}
	}
	return st
}

// hasDNS reports whether any suggestion in run carries a DNS outcome.
func hasDNS(run savedRun) bool {
	for _, r := range run.Results {
		for _, s := range r.Suggestions {
			if s.DNS != "" {
				return true
			}
		}
	}
	return false
}

// printAvailability prints, per variant, the likely-registrable share of each
// query's top 10 and top 20, and per-run figures with noise bands when the
// snapshot has more than one run.
func printAvailability(run savedRun) {
	if !hasDNS(run) {
		return
	}
	fmt.Printf("\n\n=== LIKELY REGISTRABLE (no DNS delegation; top %d per query checked) ===\n\n", availTopN)
	fmt.Printf("%-20s  %-6s  %7s  %9s  %11s  %8s\n", "Variant", "Scope", "Free%", "Free/qry", "Qry w/ none", "Unknown")
	fmt.Println(strings.Repeat("-", 72))
	for _, v := range run.Variants {
		var rs []savedQueryResult
		for _, r := range run.Results {
			if r.Variant == v {
				rs = append(rs, r)
			}
		}
		for _, n := range []int{10, availTopN} {
			st := availSummary(rs, n)
			fmt.Printf("%-20s  top%-3d  %6.1f%%  %9.1f  %5d of %-3d  %8d\n",
				v, n, st.Rate()*100, st.PerQuery(), st.QueriesNone, st.Queries, st.Unknown)
		}
		printAvailabilityByRun(rs)
	}
	fmt.Println("  Free% is over names with a known outcome. No delegation usually means available,")
	fmt.Println("  but the name may be reserved or premium-priced; confirm winners with a registrar check.")
}

func printAvailabilityByRun(rs []savedQueryResult) {
	byRun := map[int][]savedQueryResult{}
	var order []int
	for _, r := range rs {
		if _, ok := byRun[r.Run]; !ok {
			order = append(order, r.Run)
		}
		byRun[r.Run] = append(byRun[r.Run], r)
	}
	if len(order) < 2 {
		return
	}
	var t10, t20 []float64
	for _, k := range order {
		a, b := availSummary(byRun[k], 10), availSummary(byRun[k], availTopN)
		fmt.Printf("%-20s  run %-2d  top10 %5.1f%%  top%d %5.1f%%\n", "", k, a.Rate()*100, availTopN, b.Rate()*100)
		t10, t20 = append(t10, a.Rate()), append(t20, b.Rate())
	}
	fmt.Printf("%-20s  band    top10 %5.1f%%  top%d %5.1f%%   (max − min across runs)\n",
		"", noiseBand(t10)*100, availTopN, noiseBand(t20)*100)
}
