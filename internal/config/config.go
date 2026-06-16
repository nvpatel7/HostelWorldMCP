// Package config loads runtime configuration from environment variables.
// Secrets are kept in env vars and never logged; see DESIGN.md §10.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Demo         bool
	ListenAddr   string
	DailyBudget  int
	SoftCapPct   int
	HardCapPct   int
	RateBucket   int
	RateRefill   float64
	RealIPHeader string
	GlobalQPS    float64
	GlobalBurst  int
	BudgetFile   string

	// Scrape-mode settings. We scrape Hostelworld's public PWA backend rather
	// than a Partner API; see DESIGN.md §7.
	APIGeeBaseURL string
	PWAPageURL    string
	// APIGeeKey, if set, pins the api-key instead of scraping it from the page.
	APIGeeKey   string
	UserAgent   string
	MaxInFlight int

	// Circuit breaker around the (unofficial, breakable) upstream.
	BreakerMaxFailures  int
	BreakerCooldownSecs int
}

const defaultUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
	"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36"

const defaultPWAPage = "https://www.hostelworld.com/pwa/s?q=Amsterdam,%20Netherlands" +
	"&country=Netherlands&city=Amsterdam&type=city&id=15"

func Load() (*Config, error) {
	c := &Config{
		ListenAddr:    listenAddr(),
		RealIPHeader:  os.Getenv("REAL_IP_HEADER"),
		BudgetFile:    getOr("BUDGET_FILE", "budget.json"),
		APIGeeBaseURL: getOr("HOSTELWORLD_BASE_URL", "https://prod.apigee.hostelworld.com"),
		PWAPageURL:    getOr("HOSTELWORLD_PWA_PAGE_URL", defaultPWAPage),
		APIGeeKey:     os.Getenv("HOSTELWORLD_APIGEE_KEY"),
		UserAgent:     getOr("HOSTELWORLD_USER_AGENT", defaultUserAgent),
	}

	c.Demo = parseBool(os.Getenv("HOSTELWORLD_DEMO"), false)

	var err error
	if c.MaxInFlight, err = parseInt("MAX_IN_FLIGHT", 4); err != nil {
		return nil, err
	}
	if c.BreakerMaxFailures, err = parseInt("BREAKER_MAX_FAILURES", 5); err != nil {
		return nil, err
	}
	if c.BreakerCooldownSecs, err = parseInt("BREAKER_COOLDOWN_SECS", 30); err != nil {
		return nil, err
	}
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
	// Conservative default: we scrape an unofficial backend, so stay polite.
	c.GlobalQPS = parseFloat("GLOBAL_QPS", 2.0)
	if c.GlobalBurst, err = parseInt("GLOBAL_BURST", 5); err != nil {
		return nil, err
	}

	return c, nil
}

// Redacted returns a copy with secrets replaced. Safe to log with %+v. The
// apigee key is the PWA's public key, not a true secret, but we still mask it
// to avoid pinning a value in logs.
func (c *Config) Redacted() Config {
	out := *c
	if out.APIGeeKey != "" {
		out.APIGeeKey = "[REDACTED]"
	}
	return out
}

// listenAddr resolves the HTTP bind address. Precedence:
//  1. LISTEN_ADDR if set (explicit override, e.g. local dev).
//  2. 0.0.0.0:$PORT if PORT is set (Railway, Cloud Run, and other platforms
//     inject PORT and expect the app to bind it on all interfaces).
//  3. 127.0.0.1:8080 default (loopback-only for safe local runs).
func listenAddr() string {
	if v := os.Getenv("LISTEN_ADDR"); v != "" {
		return v
	}
	if p := os.Getenv("PORT"); p != "" {
		return "0.0.0.0:" + p
	}
	return "127.0.0.1:8080"
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
