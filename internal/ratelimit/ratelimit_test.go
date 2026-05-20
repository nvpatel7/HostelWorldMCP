package ratelimit

import (
	"testing"
	"time"
)

func TestBurstThenDeny(t *testing.T) {
	// 3 tokens, refill 0.001/sec so it never refills inside the test.
	l := NewPerKey(3, 0.001, time.Minute)
	for i := 0; i < 3; i++ {
		if !l.Allow("ip1") {
			t.Fatalf("allow #%d should succeed", i)
		}
	}
	if l.Allow("ip1") {
		t.Error("4th call should be denied")
	}
}

func TestIndependentKeys(t *testing.T) {
	l := NewPerKey(1, 0.001, time.Minute)
	if !l.Allow("ip1") {
		t.Fatal("ip1 first call should succeed")
	}
	if l.Allow("ip1") {
		t.Error("ip1 second call should be denied")
	}
	if !l.Allow("ip2") {
		t.Error("ip2 should not share ip1's bucket")
	}
}

func TestSize(t *testing.T) {
	l := NewPerKey(1, 0.001, time.Minute)
	l.Allow("a")
	l.Allow("b")
	l.Allow("c")
	if l.Size() != 3 {
		t.Errorf("Size = %d, want 3", l.Size())
	}
}
