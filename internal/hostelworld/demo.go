package hostelworld

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"strings"

	hwerr "github.com/nvpatel2002/hostelworld-mcp/internal/errors"
)

//go:embed fixtures/*.json
var fixtures embed.FS

// DemoClient serves canned responses from embedded fixtures. Used when
// HOSTELWORLD_DEMO=true (no real API key required). The data shape matches
// the production Client interface so tool handlers don't branch on mode.
type DemoClient struct{}

func NewDemoClient() *DemoClient { return &DemoClient{} }

func (d *DemoClient) Search(_ context.Context, p SearchParams) (*SearchResult, error) {
	city := strings.ToLower(strings.TrimSpace(p.City))
	name := "fixtures/properties_" + slugifyCity(city) + ".json"

	data, err := fixtures.ReadFile(name)
	if err != nil {
		// Fall back to Lisbon so any city query returns something sensible
		// in demo mode.
		data, err = fixtures.ReadFile("fixtures/properties_lisbon.json")
		if err != nil {
			return nil, hwerr.NotFound(fmt.Sprintf("no demo data for %q", p.City))
		}
	}

	var all []Property
	if err := json.Unmarshal(data, &all); err != nil {
		return nil, hwerr.ServiceError(err)
	}

	// Demo prices are quoted in USD; rewrite the currency label so the
	// model's response respects the user's chosen currency. Real prices
	// would come from the upstream API in the requested currency.
	if p.Currency != "" && p.Currency != "USD" {
		for i := range all {
			all[i].PriceFrom.Currency = p.Currency
		}
	}

	filtered := filterExcluded(all, p.ExcludeIDs)
	limit := p.Limit
	if limit <= 0 || limit > len(filtered) {
		limit = len(filtered)
	}

	return &SearchResult{
		Results:        filtered[:limit],
		TotalAvailable: len(all),
	}, nil
}

func (d *DemoClient) Details(_ context.Context, p DetailsParams) (*PropertyDetail, []Room, error) {
	data, err := fixtures.ReadFile("fixtures/property_" + p.PropertyID + ".json")
	if err != nil {
		return nil, nil, hwerr.NotFound(fmt.Sprintf("no demo data for property %q", p.PropertyID))
	}

	var payload struct {
		Property PropertyDetail `json:"property"`
		Rooms    []Room         `json:"rooms"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, nil, hwerr.ServiceError(err)
	}

	if p.Currency != "" && p.Currency != "USD" {
		for i := range payload.Rooms {
			payload.Rooms[i].Price.Currency = p.Currency
		}
		payload.Property.PriceFrom.Currency = p.Currency
	}

	return &payload.Property, payload.Rooms, nil
}

func slugifyCity(city string) string {
	out := make([]byte, 0, len(city))
	for i := 0; i < len(city); i++ {
		ch := city[i]
		switch {
		case ch >= 'a' && ch <= 'z', ch >= '0' && ch <= '9':
			out = append(out, ch)
		case ch >= 'A' && ch <= 'Z':
			out = append(out, ch+32)
		case ch == ' ' || ch == '-' || ch == '_':
			out = append(out, '_')
		}
	}
	return string(out)
}
