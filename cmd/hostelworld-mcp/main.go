// hostelworld-mcp is a Model Context Protocol server that exposes the
// Hostelworld Partner API to AI assistants. See README.md and DESIGN.md
// at the project root.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/nvpatel2002/hostelworld-mcp/internal/breaker"
	"github.com/nvpatel2002/hostelworld-mcp/internal/budget"
	"github.com/nvpatel2002/hostelworld-mcp/internal/config"
	"github.com/nvpatel2002/hostelworld-mcp/internal/hostelworld"
	"github.com/nvpatel2002/hostelworld-mcp/internal/mcpserver"
	"github.com/nvpatel2002/hostelworld-mcp/internal/ratelimit"
)

func main() {
	_ = godotenv.Load() // .env is optional

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		logger.Error("config load failed", "err", err)
		os.Exit(2)
	}
	logger.Info("config loaded", "cfg", cfg.Redacted())

	var client hostelworld.Client
	if cfg.Demo {
		logger.Warn("running in DEMO mode — responses come from embedded fixtures, not the live site")
		client = hostelworld.NewDemoClient()
	} else {
		cb := breaker.New(breaker.Config{
			MaxFailures:  cfg.BreakerMaxFailures,
			CooldownSecs: cfg.BreakerCooldownSecs,
			Logger:       logger,
		})
		sc, err := hostelworld.NewScrapeClient(hostelworld.ScrapeConfig{
			APIGeeBaseURL: cfg.APIGeeBaseURL,
			PWAPageURL:    cfg.PWAPageURL,
			APIGeeKey:     cfg.APIGeeKey,
			UserAgent:     cfg.UserAgent,
			Logger:        logger,
			GlobalQPS:     cfg.GlobalQPS,
			GlobalBurst:   cfg.GlobalBurst,
			MaxInFlight:   cfg.MaxInFlight,
			Breaker:       cb,
		})
		if err != nil {
			logger.Error("hostelworld client init failed", "err", err)
			os.Exit(2)
		}
		client = sc
	}

	budgetCounter := budget.New(cfg.DailyBudget, cfg.SoftCapPct, cfg.HardCapPct, cfg.BudgetFile)
	day, count, b, state := budgetCounter.Snapshot()
	logger.Info("budget loaded", "day", day, "count", count, "budget", b, "state", state.String())

	limiter := ratelimit.NewPerKey(cfg.RateBucket, cfg.RateRefill, 10*time.Minute)
	stopJanitor := make(chan struct{})
	go limiter.RunJanitor(stopJanitor, 1*time.Minute)
	defer close(stopJanitor)

	mcpSrv := mcpserver.New(client, budgetCounter, logger)
	mw := mcpserver.NewMiddleware(logger, limiter, cfg.RealIPHeader)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.Handle("/mcp", mw.Wrap(mcpSrv.HTTPHandler()))

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		logger.Info("listening", "addr", cfg.ListenAddr, "mcp_endpoint", "/mcp", "demo", cfg.Demo)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	logger.Info("shutting down")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("shutdown error", "err", err)
		os.Exit(1)
	}
	logger.Info("shut down cleanly")
}
