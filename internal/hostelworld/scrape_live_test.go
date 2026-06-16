package hostelworld

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/nvpatel2002/hostelworld-mcp/internal/breaker"
)

// TestLiveScrape hits the real Hostelworld backend. It is skipped unless
// HW_LIVE=1, so it never runs in CI. It also exercises the api-key bootstrap
// (no pinned key), proving the page-scrape path end to end.
func TestLiveScrape(t *testing.T) {
	if os.Getenv("HW_LIVE") != "1" {
		t.Skip("set HW_LIVE=1 to run the live scrape test")
	}

	cb := breaker.New(breaker.Config{MaxFailures: 5, CooldownSecs: 30})
	c, err := NewScrapeClient(ScrapeConfig{
		APIGeeBaseURL: "https://prod.apigee.hostelworld.com",
		PWAPageURL: "https://www.hostelworld.com/pwa/s?q=Amsterdam,%20Netherlands" +
			"&country=Netherlands&city=Amsterdam&type=city&id=15",
		UserAgent:   "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36",
		Logger:      slog.New(slog.NewTextHandler(os.Stderr, nil)),
		GlobalQPS:   2,
		GlobalBurst: 5,
		MaxInFlight: 4,
		Breaker:     cb,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	checkin := time.Now().AddDate(0, 0, 14).Format("2006-01-02")
	checkout := time.Now().AddDate(0, 0, 17).Format("2006-01-02")

	res, err := c.Search(ctx, SearchParams{
		City: "Amsterdam", Checkin: checkin, Checkout: checkout, Guests: 2, Currency: "USD", Limit: 5,
	})
	if err != nil {
		t.Fatalf("live Search: %v", err)
	}
	if len(res.Results) == 0 {
		t.Fatal("live Search returned no properties")
	}
	t.Logf("live: %d shown of %d total", len(res.Results), res.TotalAvailable)
	for _, p := range res.Results {
		t.Logf("  %s  %-32s  rating=%.1f %-9s  from %.2f %s  [%s]",
			p.ID, p.Name, p.Rating, p.RatingLabel, p.PriceFrom.Amount, p.PriceFrom.Currency, p.Neighborhood)
		if p.ID == "" || p.Name == "" {
			t.Fatalf("property missing id/name: %+v", p)
		}
	}

	first := res.Results[0].ID
	detail, rooms, err := c.Details(ctx, DetailsParams{
		PropertyID: first, Checkin: checkin, Checkout: checkout, Guests: 2, Currency: "USD",
	})
	if err != nil {
		t.Fatalf("live Details(%s): %v", first, err)
	}
	t.Logf("detail: %s — %s — %d rooms", detail.Name, detail.Address, len(rooms))
	for _, rm := range rooms {
		t.Logf("    room %s  %-30s  %-7s cap=%d  %.2f %s  avail=%v",
			rm.ID, rm.Name, rm.Type, rm.Capacity, rm.Price.Amount, rm.Price.Currency, rm.Available)
	}
}
