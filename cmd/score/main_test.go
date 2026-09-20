package main

import (
	"testing"

	"github.com/patlivet/domain-suggestion-engine/internal/tlds"
)

func TestSplitDomain(t *testing.T) {
	icann := tlds.DefaultRegistry.ICANNSet()

	tests := []struct {
		domain  string
		wantSLD string
		wantTLD string
		wantOK  bool
	}{
		{"duskbrew.co.uk", "duskbrew", "co.uk", true},
		{"roast.coffee", "roast", "coffee", true},
		{"google.com", "google", "com", true},
		{"my.domain.co.uk", "my.domain", "co.uk", true},
		{"custom.privatetld", "custom", "privatetld", true},
		{"nodot", "", "", false},
		{".onlydot", "", "", false},
		{"dotatend.", "", "", false},
	}

	for _, tt := range tests {
		sld, tld, ok := splitDomain(tt.domain, icann)
		if ok != tt.wantOK {
			t.Errorf("splitDomain(%q) ok = %v, want %v", tt.domain, ok, tt.wantOK)
			continue
		}
		if ok {
			if sld != tt.wantSLD || tld != tt.wantTLD {
				t.Errorf("splitDomain(%q) = (%q, %q), want (%q, %q)", tt.domain, sld, tld, tt.wantSLD, tt.wantTLD)
			}
		}
	}
}
