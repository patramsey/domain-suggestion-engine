package dnscheck

import (
	"context"
	"sync"
	"testing"
)

func TestCheckAllLooksUpEachNameOnce(t *testing.T) {
	var mu sync.Mutex
	calls := map[string]int{}
	fake := func(_ context.Context, name string) Outcome {
		mu.Lock()
		calls[name]++
		mu.Unlock()
		switch name {
		case "taken.com":
			return Delegated
		case "flaky.io":
			return Unknown
		}
		return Free
	}
	got := CheckAll(context.Background(), []string{"taken.com", "open.coffee", "taken.com", "flaky.io"}, fake, 3)

	want := map[string]Outcome{"taken.com": Delegated, "open.coffee": Free, "flaky.io": Unknown}
	if len(got) != len(want) {
		t.Fatalf("got %d results, want %d: %v", len(got), len(want), got)
	}
	for n, o := range want {
		if got[n] != o {
			t.Errorf("%s: got %v, want %v", n, got[n], o)
		}
		if calls[n] != 1 {
			t.Errorf("%s looked up %d times, want 1", n, calls[n])
		}
	}
}

func TestCheckAllEmpty(t *testing.T) {
	got := CheckAll(context.Background(), nil, func(context.Context, string) Outcome { return Free }, 2)
	if len(got) != 0 {
		t.Fatalf("want empty map, got %v", got)
	}
}

func TestOutcomeString(t *testing.T) {
	for o, s := range map[Outcome]string{Unknown: "", Free: "free", Delegated: "delegated"} {
		if o.String() != s {
			t.Errorf("%d.String() = %q, want %q", o, o.String(), s)
		}
	}
}
