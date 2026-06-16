package hostelworld

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/nvpatel2002/hostelworld-mcp/internal/breaker"
	hwerr "github.com/nvpatel2002/hostelworld-mcp/internal/errors"
)

// apigeeStub routes the apigee paths the ScrapeClient calls to recorded
// fixtures, so the integration test exercises real response shapes.
func apigeeStub(t *testing.T) *httptest.Server {
	t.Helper()
	read := func(name string) []byte {
		b, err := os.ReadFile("fixtures/" + name)
		if err != nil {
			t.Fatalf("read fixture %s: %v", name, err)
		}
		return b
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("api-key") == "" {
			http.Error(w, "missing api-key", http.StatusUnauthorized)
			return
		}
		p := r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(p, "/autocomplete/web"):
			w.Write(read("apigee_autocomplete_amsterdam.json"))
		case strings.HasSuffix(p, "/availability/"):
			w.Write(read("apigee_availability_93919.json"))
		case strings.Contains(p, "/cities/") && strings.HasSuffix(p, "/properties/"):
			w.Write(read("apigee_city_properties_amsterdam.json"))
		case strings.Contains(p, "/properties/"):
			w.Write(read("apigee_property_93919.json"))
		default:
			http.Error(w, "no stub for "+p, http.StatusNotFound)
		}
	}))
}

func newTestScrapeClient(t *testing.T, base string, cb *breaker.Breaker) *ScrapeClient {
	t.Helper()
	if cb == nil {
		cb = breaker.New(breaker.Config{MaxFailures: 3, CooldownSecs: 30})
	}
	sc, err := NewScrapeClient(ScrapeConfig{
		APIGeeBaseURL: base,
		APIGeeKey:     "test-key", // pin so no page scrape
		UserAgent:     "test-agent",
		Logger:        slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		GlobalQPS:     1000,
		GlobalBurst:   1000,
		MaxInFlight:   4,
		Breaker:       cb,
	})
	if err != nil {
		t.Fatalf("NewScrapeClient: %v", err)
	}
	return sc
}

func TestScrapeSearchMapsFixtures(t *testing.T) {
	srv := apigeeStub(t)
	defer srv.Close()
	c := newTestScrapeClient(t, srv.URL, nil)

	res, err := c.Search(context.Background(), SearchParams{
		City: "Amsterdam", Checkin: "2026-06-20", Checkout: "2026-06-23",
		Guests: 2, Currency: "USD", Limit: 10,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Results) != 3 {
		t.Fatalf("want 3 results (trimmed fixture), got %d", len(res.Results))
	}
	if res.TotalAvailable != 68 {
		t.Fatalf("want total 68 from pagination, got %d", res.TotalAvailable)
	}
	p := res.Results[0]
	if p.ID != "93919" || p.Name != "ClinkNOORD" {
		t.Fatalf("unexpected first property: %+v", p)
	}
	if p.City != "Amsterdam" || p.Country != "Netherlands" {
		t.Fatalf("city/country mapping wrong: %+v", p)
	}
	if p.Rating != 8.4 { // overallRating.overall 84 → 8.4
		t.Fatalf("rating mapping wrong: %v", p.Rating)
	}
	if p.RatingLabel != "Fabulous" {
		t.Fatalf("rating label wrong: %q", p.RatingLabel)
	}
	if p.PriceFrom.Amount != 26.04 || p.PriceFrom.Currency != "USD" {
		t.Fatalf("price mapping wrong: %+v", p.PriceFrom)
	}
	if !strings.HasPrefix(p.Thumbnail, "https://a.hwstatic.com/") || !strings.HasSuffix(p.Thumbnail, ".jpg") {
		t.Fatalf("thumbnail mapping wrong: %q", p.Thumbnail)
	}
}

func TestScrapeExcludeIDs(t *testing.T) {
	srv := apigeeStub(t)
	defer srv.Close()
	c := newTestScrapeClient(t, srv.URL, nil)

	res, err := c.Search(context.Background(), SearchParams{
		City: "Amsterdam", Checkin: "2026-06-20", Checkout: "2026-06-23",
		Guests: 2, Limit: 10, ExcludeIDs: []string{"93919"},
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	for _, p := range res.Results {
		if p.ID == "93919" {
			t.Fatal("excluded ID 93919 leaked into results")
		}
	}
	if len(res.Results) != 2 {
		t.Fatalf("want 2 after excluding 1, got %d", len(res.Results))
	}
}

func TestScrapeDetailsAndRooms(t *testing.T) {
	srv := apigeeStub(t)
	defer srv.Close()
	c := newTestScrapeClient(t, srv.URL, nil)

	detail, rooms, err := c.Details(context.Background(), DetailsParams{
		PropertyID: "93919", Checkin: "2026-06-20", Checkout: "2026-06-23", Guests: 2, Currency: "USD",
	})
	if err != nil {
		t.Fatalf("Details: %v", err)
	}
	if detail.Name != "ClinkNOORD" || detail.City != "Amsterdam" {
		t.Fatalf("detail mapping wrong: %+v", detail.Property)
	}
	if detail.Address == "" || len(detail.Facilities) == 0 {
		t.Fatalf("detail missing address/facilities: %+v", detail)
	}
	if len(rooms) != 2 { // 1 dorm + 1 private in trimmed fixture
		t.Fatalf("want 2 rooms, got %d", len(rooms))
	}
	var sawDorm, sawPrivate bool
	for _, rm := range rooms {
		if rm.Type == "dorm" {
			sawDorm = true
			if rm.Capacity != 14 || rm.Price.Amount <= 0 {
				t.Fatalf("dorm mapping wrong: %+v", rm)
			}
		}
		if rm.Type == "private" {
			sawPrivate = true
		}
	}
	if !sawDorm || !sawPrivate {
		t.Fatalf("missing room types: dorm=%v private=%v", sawDorm, sawPrivate)
	}
}

func TestScrapeUnknownCityIsNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// autocomplete "no match" returns a plain-text apology with 200.
		w.Write([]byte("Sorry, we cannot find anything that matches your search term."))
	}))
	defer srv.Close()
	c := newTestScrapeClient(t, srv.URL, nil)

	_, err := c.Search(context.Background(), SearchParams{
		City: "Nowhereville", Checkin: "2026-06-20", Checkout: "2026-06-23", Guests: 2,
	})
	var hw *hwerr.Error
	if !errors.As(err, &hw) || hw.Code != hwerr.CodeNotFound {
		t.Fatalf("want not_found, got %v", err)
	}
}

func TestScrapeBreakerTripsOnUpstream5xx(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	cb := breaker.New(breaker.Config{MaxFailures: 2, CooldownSecs: 30})
	c := newTestScrapeClient(t, srv.URL, cb)

	// Each Search retries internally then fails; after 2 failed ops the breaker opens.
	for i := 0; i < 2; i++ {
		if _, err := c.Search(context.Background(), SearchParams{
			City: "Amsterdam", Checkin: "2026-06-20", Checkout: "2026-06-23", Guests: 2,
		}); err == nil {
			t.Fatal("expected error from failing upstream")
		}
	}
	if cb.State() != "open" {
		t.Fatalf("breaker should be open, got %q", cb.State())
	}

	callsBefore := atomic.LoadInt32(&calls)
	_, err := c.Search(context.Background(), SearchParams{
		City: "Amsterdam", Checkin: "2026-06-20", Checkout: "2026-06-23", Guests: 2,
	})
	var hw *hwerr.Error
	if !errors.As(err, &hw) || hw.Code != hwerr.CodeServiceBusy {
		t.Fatalf("want service_busy while open, got %v", err)
	}
	if atomic.LoadInt32(&calls) != callsBefore {
		t.Fatal("breaker open but upstream was still called")
	}
}
