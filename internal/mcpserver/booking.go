package mcpserver

import (
	"net/url"

	"github.com/nvpatel2002/hostelworld-mcp/internal/hostelworld"
	"github.com/nvpatel2002/hostelworld-mcp/internal/slug"
)

// BuildBookingURL constructs the hostelworld.com deep link the user clicks to
// complete their booking. Pattern confirmed from a real example:
//
//	https://www.hostelworld.com/pwa/hosteldetails.php
//	  /{name-slug}/{city-slug}/{property-id}
//	  ?from=YYYY-MM-DD&to=YYYY-MM-DD&guests=N
//
// We prefer the property's CanonicalSlug if the upstream provides one; that
// avoids any drift between our slugifier and Hostelworld's canonical slug.
func BuildBookingURL(p *hostelworld.PropertyDetail, checkin, checkout string, guests int) string {
	nameSlug := p.CanonicalSlug
	if nameSlug == "" {
		nameSlug = slug.Make(p.Name)
	}
	citySlug := slug.Make(p.City)

	u := &url.URL{
		Scheme: "https",
		Host:   "www.hostelworld.com",
		Path:   "/pwa/hosteldetails.php/" + nameSlug + "/" + citySlug + "/" + p.ID,
	}
	q := u.Query()
	q.Set("from", checkin)
	q.Set("to", checkout)
	q.Set("guests", itoa(guests))
	u.RawQuery = q.Encode()

	return u.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
