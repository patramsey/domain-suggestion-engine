package llm

import "testing"

func TestShardsDefaultsToOne(t *testing.T) {
	c := NewClient("k", "m")
	if got := c.shards(); got != 1 {
		t.Errorf("shards() = %d, want 1 by default", got)
	}
	c.Shards = 3
	if got := c.shards(); got != 3 {
		t.Errorf("shards() = %d, want 3", got)
	}
	c.Shards = -2
	if got := c.shards(); got != 1 {
		t.Errorf("shards() = %d, want 1 for a nonsense value", got)
	}
}

// Sharding splits a variant's request across calls: the same total names are
// asked for, in smaller pieces, so each call generates less and finishes sooner.
func TestShardCountSplitsTheRequest(t *testing.T) {
	// 20 names, 3 variants: 7 per variant unsharded.
	if got := shardCount(20, 3, 1); got != 7 {
		t.Errorf("unsharded = %d, want 7", got)
	}
	if got := shardCount(20, 3, 2); got != 4 { // ceil(7/2)
		t.Errorf("2 shards = %d, want 4", got)
	}
	if got := shardCount(20, 1, 4); got != 5 {
		t.Errorf("single variant, 4 shards = %d, want 5", got)
	}
	if got := shardCount(20, 3, 0); got != 7 {
		t.Errorf("zero shards should behave as one, got %d", got)
	}
}

// Each shard returns its own best-first list. Joining them end to end gave
// shard 2's best name a middling LLMRank below shard 1's worst; interleaving
// gives each shard's best the same footing.
func TestInterleaveShards(t *testing.T) {
	a := []rawPair{{SLD: "a1"}, {SLD: "a2"}, {SLD: "a3"}}
	b := []rawPair{{SLD: "b1"}, {SLD: "b2"}}
	got := interleave([][]rawPair{a, b})
	want := []string{"a1", "b1", "a2", "b2", "a3"}
	if len(got) != len(want) {
		t.Fatalf("got %d pairs, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].SLD != w {
			t.Fatalf("position %d = %s, want %s (got %v)", i, got[i].SLD, w, got)
		}
	}
	if interleave(nil) != nil {
		t.Error("interleave(nil) should be nil")
	}
}
