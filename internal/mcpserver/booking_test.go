package mcpserver

import (
	"testing"

	"github.com/nvpatel2002/hostelworld-mcp/internal/hostelworld"
)

func TestBuildBookingURL_RealExample(t *testing.T) {
	// Oracle: the actual URL pattern the partner team shared.
	// https://www.hostelworld.com/pwa/hosteldetails.php/Revolution-Khao-San-by-The-Bliss/Bangkok/326223?from=2026-05-21&to=2026-05-28&guests=1
	p := &hostelworld.PropertyDetail{
		Property: hostelworld.Property{
			ID:   "326223",
			Name: "Revolution Khao San by The Bliss",
			City: "Bangkok",
		},
	}
	got := BuildBookingURL(p, "2026-05-21", "2026-05-28", 1)
	want := "https://www.hostelworld.com/pwa/hosteldetails.php/Revolution-Khao-San-by-The-Bliss/Bangkok/326223?from=2026-05-21&guests=1&to=2026-05-28"
	if got != want {
		t.Errorf("BuildBookingURL mismatch:\n got: %s\nwant: %s", got, want)
	}
}

func TestBuildBookingURL_CanonicalSlugPreferred(t *testing.T) {
	p := &hostelworld.PropertyDetail{
		Property: hostelworld.Property{
			ID:            "999",
			Name:          "Some Wildly Different Name",
			City:          "Lisbon",
			CanonicalSlug: "Canonical-Slug-From-API",
		},
	}
	got := BuildBookingURL(p, "2026-06-01", "2026-06-03", 2)
	want := "https://www.hostelworld.com/pwa/hosteldetails.php/Canonical-Slug-From-API/Lisbon/999?from=2026-06-01&guests=2&to=2026-06-03"
	if got != want {
		t.Errorf("expected canonical slug to be used:\n got: %s\nwant: %s", got, want)
	}
}
