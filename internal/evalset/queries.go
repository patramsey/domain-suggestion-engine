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

// holdoutQueries are for confirming a decision, never for making one. Every
// prompt, parser and ranking change so far was tuned and measured on core and
// hard, so results there are optimistic. These cover verticals and input
// shapes those sets do not, including other languages.
//
// Rules: do not look at holdout output while iterating on a change, do not
// edit this list to make a result look better, and do not add queries because
// a change happened to do well on them. Run it once, at the end, on the
// candidate you intend to ship. Keep "all" as core + hard so it stays a
// tuning set.
var holdoutQueries = []string{
	// services and trades
	"mobile dog grooming van in austin",
	"residential solar panel installation company",
	"emergency plumber available 24/7",
	"wedding photography duo who shoot on film",
	"bookkeeping for independent restaurants",

	// health
	"pediatric dental clinic",
	"physical therapy for runners",
	"telehealth app for managing type 2 diabetes",

	// consumer products
	"handmade ceramic dinnerware",
	"plant-based protein bars for endurance athletes",
	"refillable laundry detergent delivered by bike",
	"luxury dog beds",

	// software and B2B
	"open source observability for kubernetes",
	"contract management for construction subcontractors",
	"AI note taker for therapy sessions",
	"fleet maintenance scheduling for trucking companies",

	// education, culture, community
	"after school coding club for teenage girls",
	"independent bookstore and cafe",
	"local history walking tours",
	"esports team for college students",

	// short, vague or awkward inputs
	"bakery",
	"consulting",
	"cool name for my startup",
	"brightpath.io",

	// not English
	"cervecería artesanal en monterrey",
	"boulangerie bio à lyon",
	"nachhaltige mode aus berlin",
	"école de yoga à montréal",
}

// Queries returns the queries for a named set: core, hard, all (core + hard,
// the tuning set) or holdout (for a final check only — see holdoutQueries).
func Queries(set string) ([]string, error) {
	switch set {
	case "core":
		return coreQueries, nil
	case "hard":
		return hardQueries, nil
	case "all":
		return append(append([]string{}, coreQueries...), hardQueries...), nil
	case "holdout":
		return holdoutQueries, nil
	}
	return nil, fmt.Errorf("unknown query set %q: want core, hard, all or holdout", set)
}
