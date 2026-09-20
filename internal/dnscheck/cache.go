package dnscheck

import (
	"context"
	"strings"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
)

const (
	DefaultCacheSize = 5000
	DefaultCacheTTL  = 1 * time.Hour
)

type cacheEntry struct {
	outcome   Outcome
	expiresAt time.Time
}

// Cache provides an in-memory, thread-safe LRU cache with TTL expiration for DNS lookup outcomes.
type Cache struct {
	lru *lru.Cache[string, cacheEntry]
	ttl time.Duration
}

// NewCache creates an LRU cache with the specified capacity and TTL.
// If size <= 0, returns nil (disabled).
func NewCache(size int, ttl time.Duration) (*Cache, error) {
	if size <= 0 {
		return nil, nil
	}
	if ttl <= 0 {
		ttl = DefaultCacheTTL
	}
	l, err := lru.New[string, cacheEntry](size)
	if err != nil {
		return nil, err
	}
	return &Cache{lru: l, ttl: ttl}, nil
}

func normalizeDomain(name string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(name), "."))
}

// Get checks if the normalized domain name is present in cache and not expired.
func (c *Cache) Get(name string) (Outcome, bool) {
	if c == nil || c.lru == nil {
		return Unknown, false
	}
	norm := normalizeDomain(name)
	entry, ok := c.lru.Get(norm)
	if !ok {
		return Unknown, false
	}
	if time.Now().After(entry.expiresAt) {
		c.lru.Remove(norm)
		return Unknown, false
	}
	return entry.outcome, true
}

// Set adds a Free or Delegated outcome into the cache.
// Unknown outcomes are deliberately not cached so subsequent requests can retry.
func (c *Cache) Set(name string, o Outcome) {
	if c == nil || c.lru == nil || o == Unknown {
		return
	}
	norm := normalizeDomain(name)
	c.lru.Add(norm, cacheEntry{
		outcome:   o,
		expiresAt: time.Now().Add(c.ttl),
	})
}

// Len returns the current count of entries in cache.
func (c *Cache) Len() int {
	if c == nil || c.lru == nil {
		return 0
	}
	return c.lru.Len()
}

// CachedLookup wraps an existing Lookup with the provided Cache.
// If cache is nil, look is returned unmodified.
func CachedLookup(look Lookup, c *Cache) Lookup {
	if c == nil {
		return look
	}
	return func(ctx context.Context, name string) Outcome {
		if o, hit := c.Get(name); hit {
			return o
		}
		o := look(ctx, name)
		if o != Unknown {
			c.Set(name, o)
		}
		return o
	}
}

// CachedResolver returns a Lookup that resolves DNS via addr and caches positive
// outcomes (Free and Delegated) using the provided cache.
func CachedResolver(addr string, c *Cache) Lookup {
	base := Resolver(addr)
	return CachedLookup(base, c)
}
