package budget

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStatesByPercent(t *testing.T) {
	c := New(100, 70, 95, "")
	cases := []struct {
		afterSpends int
		want        State
	}{
		{1, StateOK},
		{69, StateOK},
		{70, StateSoftCap},
		{94, StateSoftCap},
		{95, StateHardCap},
		{100, StateHardCap},
	}
	c2 := New(100, 70, 95, "")
	last := 0
	for _, tc := range cases {
		for i := last; i < tc.afterSpends; i++ {
			c2.CheckAndSpend()
		}
		last = tc.afterSpends
		got := c2.Peek()
		if got != tc.want {
			t.Errorf("at %d spends: got %s, want %s", tc.afterSpends, got, tc.want)
		}
	}
	_ = c
}

func TestDisabledWhenBudgetZero(t *testing.T) {
	c := New(0, 70, 95, "")
	for i := 0; i < 1000; i++ {
		c.CheckAndSpend()
	}
	if c.Peek() != StateOK {
		t.Errorf("budget=0 should always be OK, got %s", c.Peek())
	}
}

func TestPersistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "budget.json")

	c1 := New(100, 70, 95, path)
	for i := 0; i < 50; i++ {
		c1.CheckAndSpend()
	}

	c2 := New(100, 70, 95, path)
	_, count, _, _ := c2.Snapshot()
	if count != 50 {
		t.Errorf("reloaded count = %d, want 50", count)
	}
}

func TestPersistenceMissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent.json")
	c := New(100, 70, 95, path)
	_, count, _, _ := c.Snapshot()
	if count != 0 {
		t.Errorf("missing file should start at 0, got %d", count)
	}
}

func TestPersistenceCorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "corrupt.json")
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := New(100, 70, 95, path)
	_, count, _, _ := c.Snapshot()
	if count != 0 {
		t.Errorf("corrupt file should start at 0, got %d", count)
	}
}
