package evalset

import "testing"

func TestCoreQuerySetUnchanged(t *testing.T) {
	// "core" must stay the historical 16 so old snapshots remain comparable.
	core, err := Queries("core")
	if err != nil {
		t.Fatal(err)
	}
	if len(core) != 16 || core[0] != "yoga studio" {
		t.Errorf("core set changed: %d queries, first %q", len(core), core[0])
	}
}

func TestAllQuerySetIsCorePlusHardWithoutDuplicates(t *testing.T) {
	core, _ := Queries("core")
	hard, _ := Queries("hard")
	all, _ := Queries("all")
	if len(hard) == 0 {
		t.Fatal("hard set is empty")
	}
	if len(all) != len(core)+len(hard) {
		t.Errorf("all = %d, want %d", len(all), len(core)+len(hard))
	}
	seen := map[string]bool{}
	for _, q := range all {
		if seen[q] {
			t.Errorf("duplicate query %q", q)
		}
		seen[q] = true
	}
}
