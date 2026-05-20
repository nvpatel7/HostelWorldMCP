// Package budget tracks daily upstream-call consumption against a configured
// cap. See DESIGN.md §11.4 for the rationale. Two thresholds: soft (cache-only)
// and hard (refuse all). Counter resets at UTC midnight.
//
// The counter is persisted to a file on Spend so a process restart doesn't
// reset the count mid-day. Best-effort: a corrupted or missing file starts
// the day at zero with a warning logged by the caller.
package budget

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"sync"
	"time"
)

type State int

const (
	StateOK State = iota
	StateSoftCap
	StateHardCap
)

func (s State) String() string {
	switch s {
	case StateSoftCap:
		return "soft_cap"
	case StateHardCap:
		return "hard_cap"
	default:
		return "ok"
	}
}

type Counter struct {
	dailyBudget int
	softCapPct  int
	hardCapPct  int
	persistPath string

	mu    sync.Mutex
	day   string // YYYY-MM-DD in UTC
	count int
}

func New(dailyBudget, softCapPct, hardCapPct int, persistPath string) *Counter {
	c := &Counter{
		dailyBudget: dailyBudget,
		softCapPct:  softCapPct,
		hardCapPct:  hardCapPct,
		persistPath: persistPath,
		day:         today(),
	}
	c.load()
	return c
}

// CheckAndSpend records one upstream call and returns the resulting state.
// If the state is StateHardCap, the caller should refuse the request.
// If StateSoftCap, the caller should only serve from cache.
func (c *Counter) CheckAndSpend() State {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rolloverLocked()
	c.count++
	c.persistLocked()
	return c.stateLocked()
}

// Peek returns the current state without consuming a call. Useful when the
// handler wants to short-circuit before doing any work.
func (c *Counter) Peek() State {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rolloverLocked()
	return c.stateLocked()
}

func (c *Counter) Snapshot() (day string, count, budget int, state State) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rolloverLocked()
	return c.day, c.count, c.dailyBudget, c.stateLocked()
}

func (c *Counter) stateLocked() State {
	if c.dailyBudget <= 0 {
		return StateOK
	}
	pct := c.count * 100 / c.dailyBudget
	switch {
	case pct >= c.hardCapPct:
		return StateHardCap
	case pct >= c.softCapPct:
		return StateSoftCap
	default:
		return StateOK
	}
}

func (c *Counter) rolloverLocked() {
	t := today()
	if t != c.day {
		c.day = t
		c.count = 0
		c.persistLocked()
	}
}

type persisted struct {
	Day   string `json:"day"`
	Count int    `json:"count"`
}

func (c *Counter) load() {
	if c.persistPath == "" {
		return
	}
	data, err := os.ReadFile(c.persistPath)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			// Caller (main) logs the warning; we silently start at zero.
		}
		return
	}
	var p persisted
	if json.Unmarshal(data, &p) != nil {
		return
	}
	if p.Day == today() {
		c.count = p.Count
	}
}

func (c *Counter) persistLocked() {
	if c.persistPath == "" {
		return
	}
	data, err := json.Marshal(persisted{Day: c.day, Count: c.count})
	if err != nil {
		return
	}
	_ = os.WriteFile(c.persistPath, data, 0o600)
}

func today() string {
	return time.Now().UTC().Format("2006-01-02")
}
