// Package breaker is a thin wrapper over sony/gobreaker that guards the
// Hostelworld upstream. Because we scrape an unofficial backend rather than
// call a contracted Partner API, the upstream can break at any time (the
// scraped api-key rotates, an endpoint shape changes, the host blocks us). A
// circuit breaker stops us hammering a broken upstream: after a run of
// failures it trips open and tool calls fail fast with a clean service_busy
// instead of every request paying the full timeout. See DESIGN.md §11.7.
package breaker

import (
	"log/slog"
	"time"

	hwerr "github.com/nvpatel2002/hostelworld-mcp/internal/errors"
	"github.com/sony/gobreaker/v2"
)

func durationSecs(s int) time.Duration { return time.Duration(s) * time.Second }

// Breaker guards a single logical upstream (all apigee operations share one).
type Breaker struct {
	cb       *gobreaker.CircuitBreaker[any]
	cooldown int // seconds, surfaced as retry_after on the open error
}

// Config tunes the breaker. Zero values fall back to sane defaults.
type Config struct {
	// MaxFailures is the number of consecutive failures that trips the breaker.
	MaxFailures int
	// CooldownSecs is how long the breaker stays open before a half-open probe.
	CooldownSecs int
	Logger       *slog.Logger
}

// New builds a Breaker. While open, Execute returns a service_busy error
// without invoking the guarded function; after CooldownSecs it allows a single
// half-open probe, closing on success or re-opening on failure.
func New(cfg Config) *Breaker {
	if cfg.MaxFailures <= 0 {
		cfg.MaxFailures = 5
	}
	if cfg.CooldownSecs <= 0 {
		cfg.CooldownSecs = 30
	}
	logger := cfg.Logger
	maxFail := uint32(cfg.MaxFailures)

	settings := gobreaker.Settings{
		Name:        "hostelworld-upstream",
		Timeout:     durationSecs(cfg.CooldownSecs), // how long the breaker stays open
		MaxRequests: 1,                              // half-open: allow a single probe
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= maxFail
		},
		OnStateChange: func(name string, from, to gobreaker.State) {
			if logger != nil {
				logger.Warn("circuit breaker state change",
					"breaker", name, "from", from.String(), "to", to.String())
			}
		},
	}

	return &Breaker{
		cb:       gobreaker.NewCircuitBreaker[any](settings),
		cooldown: cfg.CooldownSecs,
	}
}

// Execute runs fn under the breaker. When the breaker is open it returns a
// service_busy *hwerr.Error carrying a retry hint, without calling fn.
func (b *Breaker) Execute(fn func() error) error {
	_, err := b.cb.Execute(func() (any, error) {
		return nil, fn()
	})
	if err == gobreaker.ErrOpenState || err == gobreaker.ErrTooManyRequests {
		return &hwerr.Error{
			Code:    hwerr.CodeServiceBusy,
			Message: "search is temporarily unavailable; please try again shortly",
			Retry:   b.cooldown,
		}
	}
	return err
}

// State returns the current breaker state name, for logging/metrics.
func (b *Breaker) State() string { return b.cb.State().String() }
