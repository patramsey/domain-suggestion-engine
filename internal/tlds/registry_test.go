package tlds

import (
	"strings"
	"testing"

	tlddata "github.com/patlivet/domain-suggestion-engine/data/tlds"
)

func TestDefaultCategoryReturnsNonEmpty(t *testing.T) {
	tlds, err := Resolve(Filter{})
	if err != nil {
		t.Fatalf("Resolve(zero): %v", err)
	}
	if len(tlds) < 100 {
		t.Errorf("default category has %d TLDs, want >= 100", len(tlds))
	}
}

func TestDefaultContainsExpected(t *testing.T) {
	tlds, _ := Resolve(Filter{})
	set := make(map[string]struct{}, len(tlds))
	for _, tld := range tlds {
		set[tld] = struct{}{}
	}
	for _, want := range []string{"com", "io", "ai", "pizza", "coffee", "studio", "co", "ee", "am"} {
		if _, ok := set[want]; !ok {
			t.Errorf("default category missing expected TLD %q", want)
		}
	}
}

func TestClassicCategory(t *testing.T) {
	tlds, err := Resolve(Filter{Category: "classic"})
	if err != nil {
		t.Fatalf("Resolve(classic): %v", err)
	}
	want := []string{"biz", "com", "info", "net", "org"}
	if len(tlds) != len(want) {
		t.Fatalf("classic has %d TLDs, want %d", len(tlds), len(want))
	}
}

func TestTechCategory(t *testing.T) {
	tlds, err := Resolve(Filter{Category: "tech"})
	if err != nil {
		t.Fatalf("Resolve(tech): %v", err)
	}
	set := make(map[string]struct{})
	for _, tld := range tlds {
		set[tld] = struct{}{}
	}
	for _, want := range []string{"io", "ai", "dev", "app"} {
		if _, ok := set[want]; !ok {
			t.Errorf("tech category missing %q", want)
		}
	}
}

func TestIdentityDigitalCategory(t *testing.T) {
	tlds, err := Resolve(Filter{Category: "identity_digital"})
	if err != nil {
		t.Fatalf("Resolve(identity_digital): %v", err)
	}
	if len(tlds) < 100 {
		t.Errorf("identity_digital has %d TLDs, want >= 100", len(tlds))
	}
	set := make(map[string]struct{})
	for _, tld := range tlds {
		set[tld] = struct{}{}
	}
	for _, want := range []string{"pizza", "cafe", "studio", "band", "beer"} {
		if _, ok := set[want]; !ok {
			t.Errorf("identity_digital missing %q", want)
		}
	}
}

func TestAllCategory(t *testing.T) {
	tlds, err := Resolve(Filter{Category: "all"})
	if err != nil {
		t.Fatalf("Resolve(all): %v", err)
	}
	if len(tlds) < 1000 {
		t.Errorf("all category has %d TLDs, want >= 1000 (full ICANN PSL)", len(tlds))
	}
}

func TestCountryCategory(t *testing.T) {
	tlds, err := Resolve(Filter{Category: "country"})
	if err != nil {
		t.Fatalf("Resolve(country): %v", err)
	}
	for _, tld := range tlds {
		if len(tld) != 2 || strings.Contains(tld, ".") {
			t.Errorf("country category contains non-ccTLD: %q", tld)
		}
	}
	if len(tlds) < 100 {
		t.Errorf("country category has %d TLDs, want >= 100 ccTLDs", len(tlds))
	}
}

func TestUnknownCategoryError(t *testing.T) {
	_, err := Resolve(Filter{Category: "notreal"})
	if err == nil {
		t.Fatal("expected error for unknown category, got nil")
	}
}

func TestExplicitListValid(t *testing.T) {
	tlds, err := Resolve(Filter{List: []string{"com", "io", "pizza"}})
	if err != nil {
		t.Fatalf("Resolve(list=[com,io,pizza]): %v", err)
	}
	if len(tlds) != 3 {
		t.Errorf("got %d TLDs, want 3", len(tlds))
	}
}

func TestExplicitListUnknownTLD(t *testing.T) {
	_, err := Resolve(Filter{List: []string{"com", "fakemadeuptld999"}})
	if err == nil {
		t.Fatal("expected UnknownTLDError, got nil")
	}
	ute, ok := err.(*UnknownTLDError)
	if !ok {
		t.Fatalf("expected *UnknownTLDError, got %T: %v", err, err)
	}
	if len(ute.TLDs) != 1 || ute.TLDs[0] != "fakemadeuptld999" {
		t.Errorf("unexpected TLDs in error: %v", ute.TLDs)
	}
}

func TestAmbiguousFilterError(t *testing.T) {
	_, err := Resolve(Filter{Category: "classic", List: []string{"com"}})
	if err == nil {
		t.Fatal("expected error for ambiguous filter, got nil")
	}
}

func TestMultiLevelSuffix(t *testing.T) {
	// co.uk is a well-known multi-level suffix in the PSL
	if _, ok := DefaultRegistry.icann["co.uk"]; !ok {
		t.Error("PSL ICANN set missing multi-level suffix co.uk")
	}
}

func TestPSLDateExtracted(t *testing.T) {
	if DefaultRegistry.PSLDate() == "" {
		t.Error("PSL date not extracted from file header")
	}
}

func TestICANNCountReasonable(t *testing.T) {
	n := DefaultRegistry.ICANNCount()
	if n < 1000 {
		t.Errorf("ICANN TLD count %d, want >= 1000", n)
	}
}

func TestIsIdentityDigital(t *testing.T) {
	if !DefaultRegistry.IsIdentityDigital("pizza") {
		t.Error("pizza should be Identity Digital")
	}
	if DefaultRegistry.IsIdentityDigital("com") {
		t.Error("com should not be Identity Digital")
	}
}

func TestRegistryValidatesHandMaintainedFiles(t *testing.T) {
	// All entries in our hand-maintained files must be in the PSL ICANN set.
	// NewRegistry already panics on violation, but test explicitly too.
	_, err := NewRegistry(tlddata.PSLData, map[string][]byte{
		"bad": []byte("com\nfakemadeuptld999\n"),
	})
	if err == nil {
		t.Fatal("expected error for invalid TLD in category file, got nil")
	}
}
