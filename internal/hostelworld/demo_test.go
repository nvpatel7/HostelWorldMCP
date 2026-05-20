package hostelworld

import (
	"context"
	"testing"
)

func TestDemo_Search(t *testing.T) {
	d := NewDemoClient()
	r, err := d.Search(context.Background(), SearchParams{
		City:     "Lisbon",
		Checkin:  "2026-05-21",
		Checkout: "2026-05-23",
		Guests:   2,
		Limit:    3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Results) != 3 {
		t.Errorf("want 3 results, got %d", len(r.Results))
	}
	if r.TotalAvailable < 3 {
		t.Errorf("want TotalAvailable >= 3, got %d", r.TotalAvailable)
	}
}

func TestDemo_SearchExcludes(t *testing.T) {
	d := NewDemoClient()
	first, err := d.Search(context.Background(), SearchParams{
		City: "Lisbon", Checkin: "2026-05-21", Checkout: "2026-05-23",
		Guests: 2, Limit: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(first.Results))
	}
	excluded := []string{first.Results[0].ID, first.Results[1].ID}

	second, err := d.Search(context.Background(), SearchParams{
		City: "Lisbon", Checkin: "2026-05-21", Checkout: "2026-05-23",
		Guests: 2, Limit: 10, ExcludeIDs: excluded,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range second.Results {
		for _, eid := range excluded {
			if p.ID == eid {
				t.Errorf("excluded id %s leaked into results", eid)
			}
		}
	}
}

func TestDemo_Details(t *testing.T) {
	d := NewDemoClient()
	p, rooms, err := d.Details(context.Background(), DetailsParams{
		PropertyID: "326223",
		Checkin:    "2026-05-21",
		Checkout:   "2026-05-23",
		Guests:     1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != "326223" {
		t.Errorf("wrong property id: %s", p.ID)
	}
	if len(rooms) == 0 {
		t.Error("expected at least one room")
	}
}

func TestDemo_CurrencyOverride(t *testing.T) {
	d := NewDemoClient()
	r, err := d.Search(context.Background(), SearchParams{
		City: "Lisbon", Checkin: "2026-05-21", Checkout: "2026-05-23",
		Guests: 2, Currency: "EUR", Limit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Results) == 0 {
		t.Fatal("no results")
	}
	if r.Results[0].PriceFrom.Currency != "EUR" {
		t.Errorf("currency = %q, want EUR", r.Results[0].PriceFrom.Currency)
	}
}

func TestDemo_UnknownCityFallsBack(t *testing.T) {
	d := NewDemoClient()
	r, err := d.Search(context.Background(), SearchParams{
		City: "Atlantis", Checkin: "2026-05-21", Checkout: "2026-05-23",
		Guests: 2, Limit: 3,
	})
	if err != nil {
		t.Fatalf("demo mode should fall back rather than error: %v", err)
	}
	if len(r.Results) == 0 {
		t.Error("expected fallback results")
	}
}
