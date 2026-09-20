package cache

import (
	"testing"
	"time"
)

func TestKeyDistinguishesCountAndDebug(t *testing.T) {
	tlds := []string{"com", "io"}
	unavail := []string{"foo.com"}
	inspire := []string{"bar.com"}
	vars1 := []string{"evocative"}
	vars2 := []string{"evocative", "crafted"}

	k1 := Key("coffee", 10, false, false, tlds, unavail, inspire, vars1)
	k2 := Key("coffee", 20, false, false, tlds, unavail, inspire, vars1)
	k3 := Key("coffee", 10, true, false, tlds, unavail, inspire, vars1)
	k4 := Key("coffee", 10, false, true, tlds, unavail, inspire, vars1)
	k5 := Key("coffee", 10, false, false, tlds, unavail, inspire, vars2)

	if k1 == k2 {
		t.Error("cache keys with different count should not collide")
	}
	if k1 == k3 {
		t.Error("cache keys with different debug flag should not collide")
	}
	if k1 == k4 {
		t.Error("cache keys with different checkAvailability flag should not collide")
	}
	if k1 == k5 {
		t.Error("cache keys with different variants should not collide")
	}
}

func TestCacheTTLAndEviction(t *testing.T) {
	c, err := New(2, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	c.Set("k1", []byte("v1"))
	c.Set("k2", []byte("v2"))

	if val, ok := c.Get("k1"); !ok || string(val) != "v1" {
		t.Errorf("expected k1=v1, got %s (ok=%v)", string(val), ok)
	}

	time.Sleep(60 * time.Millisecond)
	if _, ok := c.Get("k1"); ok {
		t.Error("k1 should have expired")
	}
}
