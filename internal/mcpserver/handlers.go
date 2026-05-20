package mcpserver

import (
	"context"
	"errors"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/nvpatel2002/hostelworld-mcp/internal/budget"
	hwerr "github.com/nvpatel2002/hostelworld-mcp/internal/errors"
	"github.com/nvpatel2002/hostelworld-mcp/internal/hostelworld"
)

const defaultSearchLimit = 10

type searchOutput struct {
	Results        []hostelworld.Property `json:"results"`
	TotalAvailable int                    `json:"total_available"`
	ShownNow       int                    `json:"shown_now"`
}

type detailsOutput struct {
	Property *hostelworld.PropertyDetail `json:"property"`
	Rooms    []hostelworld.Room          `json:"rooms"`
}

type bookingOutput struct {
	URL         string `json:"url"`
	PropertyID  string `json:"property_id"`
	RoomTypeID  string `json:"room_type_id,omitempty"`
	Checkin     string `json:"checkin"`
	Checkout    string `json:"checkout"`
	Guests      int    `json:"guests"`
	Disclaimer  string `json:"disclaimer"`
}

func (s *Server) handleSearch(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	city, err := req.RequireString("city")
	if err != nil {
		return toolError(hwerr.InvalidInput("city is required"))
	}
	checkin, err := req.RequireString("checkin")
	if err != nil {
		return toolError(hwerr.InvalidInput("checkin is required"))
	}
	checkout, err := req.RequireString("checkout")
	if err != nil {
		return toolError(hwerr.InvalidInput("checkout is required"))
	}
	guests, err := req.RequireInt("guests")
	if err != nil {
		return toolError(hwerr.InvalidInput("guests is required"))
	}

	if err := validateDates(checkin, checkout); err != nil {
		return toolError(err)
	}
	if err := validateGuests(guests); err != nil {
		return toolError(err)
	}
	currency, err := validateCurrency(req.GetString("currency", "USD"))
	if err != nil {
		return toolError(err)
	}
	excludeIDs := req.GetStringSlice("exclude_ids", nil)
	if err := validateExcludeIDs(excludeIDs); err != nil {
		return toolError(err)
	}

	// Budget short-circuit: refuse before doing any work if we're past the
	// hard cap. Soft cap is informational here — the upstream cache will
	// serve stale-but-correct results inside the client.
	if s.budget != nil && s.budget.Peek() == budget.StateHardCap {
		return toolError(&hwerr.Error{
			Code:    hwerr.CodeQuotaExhausted,
			Message: "daily quota exhausted; try again after UTC midnight",
		})
	}

	result, err := s.client.Search(ctx, hostelworld.SearchParams{
		City:       city,
		Checkin:    checkin,
		Checkout:   checkout,
		Guests:     guests,
		Currency:   currency,
		Sort:       req.GetString("sort", "recommended"),
		Limit:      defaultSearchLimit,
		ExcludeIDs: excludeIDs,
	})
	if err != nil {
		return mapError(err)
	}

	if s.budget != nil {
		s.budget.CheckAndSpend()
	}

	if len(result.Results) == 0 {
		return toolError(hwerr.NoAvailability("no properties match those dates; try a broader range or a different city"))
	}

	return mcp.NewToolResultStructured(searchOutput{
		Results:        result.Results,
		TotalAvailable: result.TotalAvailable,
		ShownNow:       len(result.Results),
	}, formatSearchText(result)), nil
}

func (s *Server) handleDetails(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	propID, err := req.RequireString("property_id")
	if err != nil {
		return toolError(hwerr.InvalidInput("property_id is required"))
	}
	checkin, err := req.RequireString("checkin")
	if err != nil {
		return toolError(hwerr.InvalidInput("checkin is required"))
	}
	checkout, err := req.RequireString("checkout")
	if err != nil {
		return toolError(hwerr.InvalidInput("checkout is required"))
	}
	guests, err := req.RequireInt("guests")
	if err != nil {
		return toolError(hwerr.InvalidInput("guests is required"))
	}

	if err := validateDates(checkin, checkout); err != nil {
		return toolError(err)
	}
	if err := validateGuests(guests); err != nil {
		return toolError(err)
	}
	currency, err := validateCurrency(req.GetString("currency", "USD"))
	if err != nil {
		return toolError(err)
	}

	if s.budget != nil && s.budget.Peek() == budget.StateHardCap {
		return toolError(&hwerr.Error{
			Code:    hwerr.CodeQuotaExhausted,
			Message: "daily quota exhausted; try again after UTC midnight",
		})
	}

	detail, rooms, err := s.client.Details(ctx, hostelworld.DetailsParams{
		PropertyID: propID,
		Checkin:    checkin,
		Checkout:   checkout,
		Guests:     guests,
		Currency:   currency,
	})
	if err != nil {
		return mapError(err)
	}

	if s.budget != nil {
		s.budget.CheckAndSpend()
	}

	return mcp.NewToolResultStructured(detailsOutput{
		Property: detail,
		Rooms:    rooms,
	}, formatDetailsText(detail, rooms)), nil
}

func (s *Server) handleBookingURL(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	propID, err := req.RequireString("property_id")
	if err != nil {
		return toolError(hwerr.InvalidInput("property_id is required"))
	}
	checkin, err := req.RequireString("checkin")
	if err != nil {
		return toolError(hwerr.InvalidInput("checkin is required"))
	}
	checkout, err := req.RequireString("checkout")
	if err != nil {
		return toolError(hwerr.InvalidInput("checkout is required"))
	}
	guests, err := req.RequireInt("guests")
	if err != nil {
		return toolError(hwerr.InvalidInput("guests is required"))
	}

	if err := validateDates(checkin, checkout); err != nil {
		return toolError(err)
	}
	if err := validateGuests(guests); err != nil {
		return toolError(err)
	}

	// We need the property's name + city slugs to build the URL. The
	// details call gives us both. (One upstream call; cached if we
	// recently fetched details for this property.)
	detail, _, err := s.client.Details(ctx, hostelworld.DetailsParams{
		PropertyID: propID,
		Checkin:    checkin,
		Checkout:   checkout,
		Guests:     guests,
		Currency:   "USD",
	})
	if err != nil {
		return mapError(err)
	}

	if s.budget != nil {
		s.budget.CheckAndSpend()
	}

	url := BuildBookingURL(detail, checkin, checkout, guests)

	out := bookingOutput{
		URL:        url,
		PropertyID: propID,
		RoomTypeID: req.GetString("room_type_id", ""),
		Checkin:    checkin,
		Checkout:   checkout,
		Guests:     guests,
		Disclaimer: "Payment is completed on hostelworld.com. This server never handles payment.",
	}
	return mcp.NewToolResultStructured(out,
		"Booking URL: "+url+"\n\nClick the link to complete your booking on hostelworld.com."), nil
}

// toolError converts any error to a structured tool error. If err is not
// already an *hwerr.Error, it's wrapped as a generic service_error. Internal
// detail (the wrapped Cause) is never returned to the client — see
// DESIGN.md §13.
func toolError(err error) (*mcp.CallToolResult, error) {
	var hw *hwerr.Error
	if !errors.As(err, &hw) {
		hw = hwerr.ServiceError(err)
	}
	return mcp.NewToolResultStructured(hw.External(), hw.Message), nil
}

// mapError is an alias kept for upstream-API failures, to make the call-site
// intent clear (vs. local input-validation errors).
func mapError(err error) (*mcp.CallToolResult, error) { return toolError(err) }
