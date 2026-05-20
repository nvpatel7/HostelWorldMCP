// Package cache is a thin TTL+LRU wrapper. We don't cache availability/pricing
// (see DESIGN.md §12), but we aggressively cache city→location-id lookups and
// property static info.
package cache

import (
	"sync"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
)

type entry[V any] struct {
	value   V
	expires time.Time
}

// TTL is a sized LRU cache where each entry also has a wall-clock expiry.
// Concurrent-safe.
type TTL[K comparable, V any] struct {
	lru *lru.Cache[K, entry[V]]
	ttl time.Duration
	mu  sync.Mutex
}

func New[K comparable, V any](size int, ttl time.Duration) (*TTL[K, V], error) {
	l, err := lru.New[K, entry[V]](size)
	if err != nil {
		return nil, err
	}
	return &TTL[K, V]{lru: l, ttl: ttl}, nil
}

func (c *TTL[K, V]) Get(key K) (V, bool) {
	var zero V
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.lru.Get(key)
	if !ok {
		return zero, false
	}
	if time.Now().After(e.expires) {
		c.lru.Remove(key)
		return zero, false
	}
	return e.value, true
}

func (c *TTL[K, V]) Set(key K, value V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lru.Add(key, entry[V]{value: value, expires: time.Now().Add(c.ttl)})
}

func (c *TTL[K, V]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lru.Len()
}
