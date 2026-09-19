// Package evalset holds the fixed query sets used by the eval harness and the /suggest checker.
package evalset

import "fmt"

// coreQueries is the historical 16-query set across 8 verticals. Keep it
// unchanged so new snapshots stay comparable with old ones.
var coreQueries = []string{
	// wellness / fitness
	"yoga studio",
	"meditation and mindfulness app for anxiety",

	// tech / SaaS
	"AI-powered legal document review tool for small law firms",
	"developer tool for automating code reviews with AI",
	"team project management and async communication tool",

	// community / media
	"indie game developer community and showcase",
	"podcast hosting and analytics platform",

	// food / drink
	"a denver based coffee shop that serves beer at night",
	"craft beer subscription box monthly delivery",

	// existing domain input
	"patspizza.com",

	// marketplaces
	"vintage clothing resale marketplace",
	"freelance marketplace for creative professionals",

	// physical / local
	"neighborhood barbershop in brooklyn",

	// other verticals
	"personal finance and budgeting app for millennials",
	"online learning platform for professional photography",
	"sustainable outdoor gear and apparel brand",
}

// hardQueries stress inputs the core set under-covers: very short, vague,
// non-English, long and rambling, niche B2B, and a second domain input.
var hardQueries = []string{
	"tea",
	"law firm",
	"something for my side hustle",
	"panadería artesanal en madrid",
	"we're a small family-run business in vermont that makes maple syrup and maple candy, we sell at farmers markets and online and want to start doing corporate holiday gift boxes",
	"SOC 2 compliance automation for mid-market fintech companies",
	"getfitnow.net",
	"nonprofit that plants trees in cities",
}

// Queries returns the queries for a named set: core, hard or all.
func Queries(set string) ([]string, error) {
	switch set {
	case "core":
		return coreQueries, nil
	case "hard":
		return hardQueries, nil
	case "all":
		return append(append([]string{}, coreQueries...), hardQueries...), nil
	}
	return nil, fmt.Errorf("unknown query set %q: want core, hard or all", set)
}
