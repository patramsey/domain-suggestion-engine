package cache

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
)

const DefaultTTL = 5 * time.Minute

type entry struct {
	value     []byte
	expiresAt time.Time
}

// Cache is an in-process LRU cache with TTL support.
type Cache struct {
	lru *lru.Cache[string, entry]
	ttl time.Duration
	mu  sync.Mutex
}

// New creates a cache with the given maximum size and TTL.
func New(maxSize int, ttl time.Duration) (*Cache, error) {
	l, err := lru.New[string, entry](maxSize)
	if err != nil {
		return nil, err
	}
	return &Cache{lru: l, ttl: ttl}, nil
}

// Key builds a cache key from the normalized input, TLD filter, unavailable domains, and inspire_from list.
func Key(input string, tlds, unavailable, inspireFrom []string) string {
	normalized := strings.ToLower(strings.TrimSpace(input))
	sorted := make([]string, len(tlds))
	copy(sorted, tlds)
	sort.Strings(sorted)
	unavailSorted := make([]string, len(unavailable))
	copy(unavailSorted, unavailable)
	sort.Strings(unavailSorted)
	inspireSorted := make([]string, len(inspireFrom))
	copy(inspireSorted, inspireFrom)
	sort.Strings(inspireSorted)
	raw := normalized + "\x00" + strings.Join(sorted, ",") + "\x00" + strings.Join(unavailSorted, ",") + "\x00" + strings.Join(inspireSorted, ",")
	sum := sha256.Sum256([]byte(raw))
	return fmt.Sprintf("%x", sum)
}

// Get returns cached bytes and true if a non-expired entry exists.
func (c *Cache) Get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.lru.Get(key)
	if !ok {
		return nil, false
	}
	if time.Now().After(e.expiresAt) {
		c.lru.Remove(key)
		return nil, false
	}
	return e.value, true
}

// Set stores bytes under key with the cache TTL.
func (c *Cache) Set(key string, value []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lru.Add(key, entry{value: value, expiresAt: time.Now().Add(c.ttl)})
}
