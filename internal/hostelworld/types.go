// Package hostelworld is the Partner API client. Field shapes are best-effort
// pending real-key access to the API; expect tweaks once we can hit the live
// endpoints. The Client interface is stable.
package hostelworld

// Location is a resolved city/region with an ID the Partner API accepts.
type Location struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"` // "city", "region", etc.
}

// Money is a price in a named currency.
type Money struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

// Property is a single hostel in search results. Fields are the subset we
// surface to the model.
type Property struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	City         string  `json:"city"`
	Country      string  `json:"country"`
	Neighborhood string  `json:"neighborhood,omitempty"`
	Rating       float64 `json:"rating"`        // 0–10
	RatingLabel  string  `json:"rating_label"`  // "Superb", "Fabulous", etc.
	PriceFrom    Money   `json:"price_from"`
	Thumbnail    string  `json:"thumbnail,omitempty"`
	Tags         []string `json:"tags,omitempty"`
	// CanonicalSlug, if set by the upstream, is used in booking URLs instead
	// of our local slugifier. Greatly reduces 404 risk.
	CanonicalSlug string `json:"canonical_slug,omitempty"`
}

// Room is one bookable room type at a property.
type Room struct {
	ID          string `json:"id"`
	Name        string `json:"name"`         // "6-Bed Mixed Dorm"
	Type        string `json:"type"`         // "dorm", "private", etc.
	Capacity    int    `json:"capacity"`
	Price       Money  `json:"price"`
	Available   bool   `json:"available"`
	Description string `json:"description,omitempty"`
}

// PropertyDetail is the full record returned by the detail endpoint.
type PropertyDetail struct {
	Property
	Address      string   `json:"address"`
	Description  string   `json:"description"`
	Facilities   []string `json:"facilities"`
	CheckInTime  string   `json:"checkin_time,omitempty"`
	CheckOutTime string   `json:"checkout_time,omitempty"`
	Photos       []string `json:"photos,omitempty"`
}

// SearchParams is the input to PropertySearch.
type SearchParams struct {
	City     string
	Checkin  string // YYYY-MM-DD
	Checkout string // YYYY-MM-DD
	Guests   int
	Currency string
	// Sort: "recommended", "price_low", "rating".
	Sort string
	// Limit caps results; default 10 in handlers.
	Limit int
	// ExcludeIDs filters out properties the model has already shown.
	ExcludeIDs []string
}

// DetailsParams is the input to PropertyDetails.
type DetailsParams struct {
	PropertyID string
	Checkin    string
	Checkout   string
	Guests     int
	Currency   string
}

// SearchResult bundles results with the total upstream said matched, so the
// model can offer "show me more" intelligently.
type SearchResult struct {
	Results        []Property `json:"results"`
	TotalAvailable int        `json:"total_available"`
}
