package tlds

import (
	"fmt"
	"sort"

	tlddata "github.com/patlivet/domain-suggestion-engine/data/tlds"
)

// DefaultRegistry is the package-level registry loaded at init time.
var DefaultRegistry *Registry

func init() {
	var err error
	DefaultRegistry, err = NewRegistry(tlddata.PSLData, map[string][]byte{
		"default":          tlddata.DefaultData,
		"identity_digital": tlddata.IdentityDigitalData,
		"classic":          tlddata.ClassicData,
		"tech":             tlddata.TechData,
	})
	if err != nil {
		panic(fmt.Sprintf("tlds: failed to initialize registry: %v", err))
	}
}

// Resolve is a convenience wrapper around DefaultRegistry.Resolve.
func Resolve(f Filter) ([]string, error) {
	return DefaultRegistry.Resolve(f)
}

func setToSortedSlice(m map[string]struct{}) []string {
	s := make([]string, 0, len(m))
	for k := range m {
		s = append(s, k)
	}
	sort.Strings(s)
	return s
}

func sortStrings(s []string) {
	sort.Strings(s)
}

func dedupeStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	return out
}
