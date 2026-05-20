package cache

import (
	"testing"
	"time"
)

func TestSetGet(t *testing.T) {
	c, err := New[string, int](10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	c.Set("a", 1)
	v, ok := c.Get("a")
	if !ok || v != 1 {
		t.Errorf("got %v %v, want 1 true", v, ok)
	}
}

func TestMiss(t *testing.T) {
	c, _ := New[string, int](10, time.Minute)
	if _, ok := c.Get("missing"); ok {
		t.Error("expected miss for absent key")
	}
}

func TestExpiry(t *testing.T) {
	c, _ := New[string, int](10, 10*time.Millisecond)
	c.Set("a", 1)
	time.Sleep(25 * time.Millisecond)
	if _, ok := c.Get("a"); ok {
		t.Error("expected expired entry to miss")
	}
}

func TestEviction(t *testing.T) {
	c, _ := New[int, int](2, time.Minute)
	c.Set(1, 1)
	c.Set(2, 2)
	c.Set(3, 3) // evicts 1
	if _, ok := c.Get(1); ok {
		t.Error("LRU should have evicted key 1")
	}
	if _, ok := c.Get(3); !ok {
		t.Error("key 3 should still be present")
	}
}
