// Package config loads runtime configuration from environment variables.
// Secrets are kept in env vars and never logged; see DESIGN.md §10.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	APIKey         string
	Demo           bool
	ListenAddr     string
	DailyBudget    int
	SoftCapPct     int
	HardCapPct     int
	RateBucket     int
	RateRefill     float64
	RealIPHeader   string
	GlobalQPS      float64
	GlobalBurst    int
	BudgetFile     string
	HostelworldURL string
}

func Load() (*Config, error) {
	c := &Config{
		ListenAddr:     getOr("LISTEN_ADDR", "127.0.0.1:8080"),
		RealIPHeader:   os.Getenv("REAL_IP_HEADER"),
		BudgetFile:     getOr("BUDGET_FILE", "budget.json"),
		HostelworldURL: getOr("HOSTELWORLD_BASE_URL", "https://partner-api.hostelworld.com"),
	}

	c.Demo = parseBool(os.Getenv("HOSTELWORLD_DEMO"), false)
	c.APIKey = os.Getenv("HOSTELWORLD_API_KEY")

	if !c.Demo && c.APIKey == "" {
		return nil, errors.New("HOSTELWORLD_API_KEY required (or set HOSTELWORLD_DEMO=true)")
	}

	var err error
	if c.DailyBudget, err = parseInt("DAILY_BUDGET", 10000); err != nil {
		return nil, err
	}
	if c.SoftCapPct, err = parseInt("SOFT_CAP_PCT", 70); err != nil {
		return nil, err
	}
	if c.HardCapPct, err = parseInt("HARD_CAP_PCT", 95); err != nil {
		return nil, err
	}
	if c.RateBucket, err = parseInt("RATE_BUCKET", 20); err != nil {
		return nil, err
	}
	c.RateRefill = parseFloat("RATE_REFILL_PER_SEC", 0.2)
	c.GlobalQPS = parseFloat("GLOBAL_QPS", 5.0)
	if c.GlobalBurst, err = parseInt("GLOBAL_BURST", 10); err != nil {
		return nil, err
	}

	return c, nil
}

// Redacted returns a copy with secrets replaced. Safe to log with %+v.
func (c *Config) Redacted() Config {
	out := *c
	if out.APIKey != "" {
		out.APIKey = "[REDACTED]"
	}
	return out
}

func getOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func parseBool(s string, def bool) bool {
	if s == "" {
		return def
	}
	switch strings.ToLower(s) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func parseInt(key string, def int) (int, error) {
	s := os.Getenv(key)
	if s == "" {
		return def, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return n, nil
}

func parseFloat(key string, def float64) float64 {
	s := os.Getenv(key)
	if s == "" {
		return def
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return def
	}
	return f
}
