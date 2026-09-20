package dnscheck

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCacheBasic(t *testing.T) {
	cache, err := NewCache(100, 10*time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Not found initially
	if _, ok := cache.Get("example.com"); ok {
		t.Errorf("expected miss on empty cache")
	}

	// Cache a Delegated domain
	cache.Set("example.com", Delegated)

	// Hits with various normalizations
	for _, n := range []string{"example.com", "EXAMPLE.COM", "example.com.", "  EXAMPLE.COM. "} {
		out, ok := cache.Get(n)
		if !ok || out != Delegated {
			t.Errorf("Get(%q) = (%v, %v), want (%v, true)", n, out, ok, Delegated)
		}
	}

	// Cache a Free domain
	cache.Set("unregistered-foobar.xyz", Free)
	out, ok := cache.Get("unregistered-foobar.xyz")
	if !ok || out != Free {
		t.Errorf("Get(unregistered-foobar.xyz) = (%v, %v), want (%v, true)", out, ok, Free)
	}
}

func TestCacheUnknownNotCached(t *testing.T) {
	cache, err := NewCache(100, 10*time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Unknown should NOT be stored
	cache.Set("transient-failure.com", Unknown)
	if _, ok := cache.Get("transient-failure.com"); ok {
		t.Errorf("expected Unknown outcome to never be cached")
	}
	if cache.Len() != 0 {
		t.Errorf("cache.Len() = %d, want 0", cache.Len())
	}
}

func TestCacheTTL(t *testing.T) {
	cache, err := NewCache(100, 40*time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cache.Set("shortlived.com", Free)

	out, ok := cache.Get("shortlived.com")
	if !ok || out != Free {
		t.Fatalf("immediate Get = (%v, %v), want (%v, true)", out, ok, Free)
	}

	// Wait for TTL expiration
	time.Sleep(50 * time.Millisecond)

	out, ok = cache.Get("shortlived.com")
	if ok {
		t.Errorf("Get after TTL = (%v, %v), want miss", out, ok)
	}
}

func TestCachedLookup(t *testing.T) {
	cache, err := NewCache(100, 10*time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var callCount atomic.Int32
	mockLookup := func(_ context.Context, name string) Outcome {
		callCount.Add(1)
		if name == "registered.com" {
			return Delegated
		}
		if name == "error.com" {
			return Unknown
		}
		return Free
	}

	cached := CachedLookup(mockLookup, cache)
	ctx := context.Background()

	// 1. Registered domain - first call calls mock, second hits cache
	o1 := cached(ctx, "registered.com")
	if o1 != Delegated || callCount.Load() != 1 {
		t.Errorf("first call: got %v (calls=%d), want Delegated (calls=1)", o1, callCount.Load())
	}

	o2 := cached(ctx, "registered.com")
	if o2 != Delegated || callCount.Load() != 1 {
		t.Errorf("second call: got %v (calls=%d), want Delegated (calls=1)", o2, callCount.Load())
	}

	// 2. Error domain - not cached, so each lookup calls mock
	e1 := cached(ctx, "error.com")
	if e1 != Unknown || callCount.Load() != 2 {
		t.Errorf("first error: got %v (calls=%d), want Unknown (calls=2)", e1, callCount.Load())
	}
	e2 := cached(ctx, "error.com")
	if e2 != Unknown || callCount.Load() != 3 {
		t.Errorf("second error: got %v (calls=%d), want Unknown (calls=3)", e2, callCount.Load())
	}
}

func TestCacheConcurrency(t *testing.T) {
	cache, err := NewCache(500, 1*time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var wg sync.WaitGroup
	ctx := context.Background()

	mockLookup := func(_ context.Context, name string) Outcome {
		return Delegated
	}
	lookup := CachedLookup(mockLookup, cache)

	for i := range 30 {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := range 50 {
				domain := fmt.Sprintf("domain-%d.com", j%10)
				_ = lookup(ctx, domain)
			}
		}(i)
	}

	wg.Wait()
}
