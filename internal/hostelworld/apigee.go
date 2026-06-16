package hostelworld

import "strconv"

// This file holds the apigee backend's response shapes and the mapping from
// those shapes to our stable, tool-facing types in types.go. Only the subset of
// fields we surface is modelled. Shapes were captured from live responses; see
// the fixtures in fixtures/apigee_*.json.

// --- shared scalars ---

// apigeeMoney is a {"value":"26.04","currency":"USD"} price.
type apigeeMoney struct {
	Value    string `json:"value"`
	Currency string `json:"currency"`
}

func (m apigeeMoney) toMoney() Money {
	amt, _ := strconv.ParseFloat(m.Value, 64)
	return Money{Amount: amt, Currency: m.Currency}
}

type apigeeImage struct {
	Prefix string `json:"prefix"`
	Suffix string `json:"suffix"`
}

func (i apigeeImage) url() string { return "https://" + i.Prefix + i.Suffix }

func imageURLs(imgs []apigeeImage, max int) []string {
	if len(imgs) > max {
		imgs = imgs[:max]
	}
	out := make([]string, 0, len(imgs))
	for _, im := range imgs {
		out = append(out, im.url())
	}
	return out
}

type apigeeRating struct {
	Overall         int    `json:"overall"` // 0–100
	NumberOfRatings string `json:"numberOfRatings"`
}

// ratingTen converts the 0–100 upstream rating to our 0–10 scale.
func (r apigeeRating) ratingTen() float64 { return float64(r.Overall) / 10.0 }

// ratingLabel mirrors Hostelworld's qualitative bands.
func ratingLabel(r float64) string {
	switch {
	case r >= 9:
		return "Superb"
	case r >= 8:
		return "Fabulous"
	case r >= 7:
		return "Very Good"
	case r >= 6:
		return "Good"
	case r > 0:
		return "Okay"
	default:
		return ""
	}
}

// --- autocomplete ---

type apigeeSuggestion struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

// --- city property search ---

type apigeeCityProperties struct {
	Properties []apigeeProperty `json:"properties"`
	Pagination struct {
		Total int `json:"totalNumberOfItems"`
	} `json:"pagination"`
}

type apigeeNamed struct {
	Name string `json:"name"`
}

type apigeeProperty struct {
	ID                  int64         `json:"id"`
	Name                string        `json:"name"`
	City                apigeeNamed   `json:"city"`
	Country             apigeeNamed   `json:"country"`
	District            apigeeNamed   `json:"district"`
	OverallRating       apigeeRating  `json:"overallRating"`
	LowestPricePerNight apigeeMoney   `json:"lowestPricePerNight"`
	Images              []apigeeImage `json:"images"`
}

func (a apigeeProperty) toProperty() Property {
	rating := a.OverallRating.ratingTen()
	thumb := ""
	if len(a.Images) > 0 {
		thumb = a.Images[0].url()
	}
	return Property{
		ID:           strconv.FormatInt(a.ID, 10),
		Name:         a.Name,
		City:         a.City.Name,
		Country:      a.Country.Name,
		Neighborhood: a.District.Name,
		Rating:       rating,
		RatingLabel:  ratingLabel(rating),
		PriceFrom:    a.LowestPricePerNight.toMoney(),
		Thumbnail:    thumb,
	}
}

// --- property detail ---

type apigeeDetail struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Address1    string `json:"address1"`
	Address2    string `json:"address2"`
	Description string `json:"description"`
	City        struct {
		Name    string `json:"name"`
		Country string `json:"country"`
	} `json:"city"`
	Rating  apigeeRating `json:"rating"`
	CheckIn struct {
		StartsAt string `json:"startsAt"`
		EndsAt   string `json:"endsAt"`
	} `json:"checkIn"`
	LatestCheckOut string                   `json:"latestCheckOut"`
	Facilities     []apigeeFacilityCategory `json:"facilities"`
	Images         []apigeeImage            `json:"images"`
}

type apigeeFacilityCategory struct {
	Name       string        `json:"name"`
	Facilities []apigeeNamed `json:"facilities"`
}

func (d apigeeDetail) toDetail() *PropertyDetail {
	rating := d.Rating.ratingTen()

	addr := d.Address1
	if d.Address2 != "" {
		if addr != "" {
			addr += ", "
		}
		addr += d.Address2
	}

	checkIn := ""
	if d.CheckIn.StartsAt != "" {
		checkIn = d.CheckIn.StartsAt + ":00"
		if d.CheckIn.EndsAt != "" {
			checkIn += "–" + d.CheckIn.EndsAt + ":00"
		}
	}

	// Flatten the nested facility tree into a flat amenity list, capped.
	var facilities []string
	for _, cat := range d.Facilities {
		for _, f := range cat.Facilities {
			facilities = append(facilities, f.Name)
			if len(facilities) >= 40 {
				break
			}
		}
		if len(facilities) >= 40 {
			break
		}
	}

	return &PropertyDetail{
		Property: Property{
			ID:          d.ID,
			Name:        d.Name,
			City:        d.City.Name,
			Country:     d.City.Country,
			Rating:      rating,
			RatingLabel: ratingLabel(rating),
		},
		Address:      addr,
		Description:  d.Description,
		Facilities:   facilities,
		CheckInTime:  checkIn,
		CheckOutTime: d.LatestCheckOut,
		Photos:       imageURLs(d.Images, 6),
	}
}

// --- availability / rooms ---

type apigeeAvailability struct {
	Rooms struct {
		Dorms    []apigeeRoom `json:"dorms"`
		Privates []apigeeRoom `json:"privates"`
	} `json:"rooms"`
}

type apigeeRoom struct {
	ID                  int64       `json:"id"`
	Name                string      `json:"name"`
	Description         string      `json:"description"`
	Capacity            string      `json:"capacity"`
	TotalBedsAvailable  int         `json:"totalBedsAvailable"`
	LowestPricePerNight apigeeMoney `json:"lowestPricePerNight"`
}

func (av apigeeAvailability) toRooms() []Room {
	var out []Room
	add := func(list []apigeeRoom, typ string) {
		for _, r := range list {
			cap, _ := strconv.Atoi(r.Capacity)
			out = append(out, Room{
				ID:          strconv.FormatInt(r.ID, 10),
				Name:        r.Name,
				Type:        typ,
				Capacity:    cap,
				Price:       r.LowestPricePerNight.toMoney(),
				Available:   r.TotalBedsAvailable > 0,
				Description: r.Description,
			})
		}
	}
	add(av.Rooms.Dorms, "dorm")
	add(av.Rooms.Privates, "private")
	return out
}
