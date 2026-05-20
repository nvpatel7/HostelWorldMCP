package mcpserver

import (
	"fmt"
	"strings"

	"github.com/nvpatel2002/hostelworld-mcp/internal/hostelworld"
)

// Human-readable fallback text for MCP clients that don't consume structured
// content. The model itself reads the structured field directly; this text is
// for log/debugging and dumb clients.

func formatSearchText(r *hostelworld.SearchResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Found %d properties (%d total available):\n\n", len(r.Results), r.TotalAvailable)
	for i, p := range r.Results {
		fmt.Fprintf(&b, "%d. %s (★ %.1f, from %.2f %s) — %s\n   id: %s\n",
			i+1, p.Name, p.Rating, p.PriceFrom.Amount, p.PriceFrom.Currency, p.Neighborhood, p.ID,
		)
	}
	return b.String()
}

func formatDetailsText(p *hostelworld.PropertyDetail, rooms []hostelworld.Room) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s — %s, %s\n", p.Name, p.City, p.Country)
	fmt.Fprintf(&b, "★ %.1f (%s) · from %.2f %s\n\n",
		p.Rating, p.RatingLabel, p.PriceFrom.Amount, p.PriceFrom.Currency)
	if p.Address != "" {
		fmt.Fprintf(&b, "Address: %s\n", p.Address)
	}
	if p.Description != "" {
		fmt.Fprintf(&b, "\n%s\n", p.Description)
	}
	if len(rooms) > 0 {
		b.WriteString("\nAvailable rooms:\n")
		for _, r := range rooms {
			fmt.Fprintf(&b, "  - %s (%d-person %s) — %.2f %s%s\n",
				r.Name, r.Capacity, r.Type, r.Price.Amount, r.Price.Currency,
				availText(r.Available))
		}
	}
	return b.String()
}

func availText(available bool) string {
	if available {
		return ""
	}
	return " — UNAVAILABLE"
}
