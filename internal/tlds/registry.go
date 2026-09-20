package tlds

import (
	"bufio"
	"bytes"
	"fmt"
	"strings"
)

// Registry holds the parsed PSL ICANN TLD set and named category lists.
type Registry struct {
	icann      map[string]struct{} // all ICANN-section entries
	categories map[string][]string // name → sorted TLD list
	pslDate    string
	idSet      map[string]struct{} // Identity Digital TLD set for fast lookup
}

// Filter selects which TLDs to use for a request.
type Filter struct {
	Category string
	List     []string
}

const defaultCategory = "default"

// NewRegistry parses the embedded PSL and category files.
func NewRegistry(pslData []byte, categoryFiles map[string][]byte) (*Registry, error) {
	r := &Registry{
		icann:      make(map[string]struct{}),
		categories: make(map[string][]string),
		idSet:      make(map[string]struct{}),
	}

	if err := r.parsePSL(pslData); err != nil {
		return nil, fmt.Errorf("parse PSL: %w", err)
	}

	for name, data := range categoryFiles {
		tlds, err := parseList(data)
		if err != nil {
			return nil, fmt.Errorf("parse category %s: %w", name, err)
		}
		// validate every entry is in the ICANN set
		for _, tld := range tlds {
			if _, ok := r.icann[tld]; !ok {
				return nil, fmt.Errorf("category %s: TLD %q not found in PSL ICANN section", name, tld)
			}
		}
		r.categories[name] = tlds
		if name == "identity_digital" {
			for _, tld := range tlds {
				r.idSet[tld] = struct{}{}
			}
		}
	}

	// derive "all" and "country" from the parsed ICANN set
	r.categories["all"] = setToSortedSlice(r.icann)
	r.categories["country"] = r.countryTLDs()

	return r, nil
}

// Resolve returns the concrete TLD list for a filter.
// A zero Filter resolves to the default category.
func (r *Registry) Resolve(f Filter) ([]string, error) {
	if f.Category == "" && len(f.List) == 0 {
		return r.categories[defaultCategory], nil
	}
	if f.Category != "" && len(f.List) > 0 {
		return nil, fmt.Errorf("category and list are mutually exclusive")
	}
	if f.Category != "" {
		tlds, ok := r.categories[f.Category]
		if !ok {
			return nil, fmt.Errorf("unknown category %q", f.Category)
		}
		return tlds, nil
	}
	// explicit list — validate each entry
	var unknown []string
	normalized := make([]string, 0, len(f.List))
	for _, tld := range f.List {
		norm := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(tld), "."))
		if _, ok := r.icann[norm]; !ok {
			unknown = append(unknown, norm)
		} else {
			normalized = append(normalized, norm)
		}
	}
	if len(unknown) > 0 {
		return nil, &UnknownTLDError{TLDs: unknown}
	}
	return dedupeStrings(normalized), nil
}

// CategoryNames returns all available category names.
func (r *Registry) CategoryNames() []string {
	names := make([]string, 0, len(r.categories))
	for name := range r.categories {
		names = append(names, name)
	}
	return names
}

// Category returns the TLD list for a named category.
func (r *Registry) Category(name string) ([]string, bool) {
	tlds, ok := r.categories[name]
	return tlds, ok
}

// PSLDate returns the date extracted from the PSL file header.
func (r *Registry) PSLDate() string { return r.pslDate }

// ICANNSet returns a copy of the parsed ICANN TLD set for use by the parser.
func (r *Registry) ICANNSet() map[string]struct{} {
	cp := make(map[string]struct{}, len(r.icann))
	for k := range r.icann {
		cp[k] = struct{}{}
	}
	return cp
}

// ICANNCount returns the total number of ICANN TLDs parsed.
func (r *Registry) ICANNCount() int { return len(r.icann) }

// IsIdentityDigital reports whether a TLD is in the Identity Digital portfolio.
func (r *Registry) IsIdentityDigital(tld string) bool {
	_, ok := r.idSet[tld]
	return ok
}

// IdentityDigitalTLDs returns the full Identity Digital TLD list.
func (r *Registry) IdentityDigitalTLDs() []string {
	return r.categories["identity_digital"]
}

func (r *Registry) parsePSL(data []byte) error {
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
		if !inICANN {
			// extract date from header comments before ICANN section
			if r.pslDate == "" && strings.HasPrefix(line, "// ") {
				if idx := strings.Index(line, "20"); idx != -1 {
					candidate := line[idx:]
					if len(candidate) >= 10 && isDateLike(candidate[:10]) {
						r.pslDate = candidate[:10]
					}
				}
			}
			continue
		}

		if line == "" || strings.HasPrefix(line, "//") || strings.HasPrefix(line, "!") {
			continue
		}

		// wildcard entries like *.ck — store base
		tld := strings.ToLower(strings.TrimPrefix(line, "*."))
		r.icann[tld] = struct{}{}
	}
	return scanner.Err()
}

func (r *Registry) countryTLDs() []string {
	var cc []string
	for tld := range r.icann {
		if len(tld) == 2 && !strings.Contains(tld, ".") {
			cc = append(cc, tld)
		}
	}
	sortStrings(cc)
	return cc
}

func parseList(data []byte) ([]string, error) {
	var tlds []string
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		tlds = append(tlds, strings.ToLower(line))
	}
	return tlds, scanner.Err()
}

func isDateLike(s string) bool {
	if len(s) < 10 {
		return false
	}
	return s[4] == '-' && s[7] == '-'
}

// UnknownTLDError is returned when a list filter contains unrecognized TLDs.
type UnknownTLDError struct {
	TLDs []string
}

func (e *UnknownTLDError) Error() string {
	return fmt.Sprintf("unknown TLDs: %s", strings.Join(e.TLDs, ", "))
}
