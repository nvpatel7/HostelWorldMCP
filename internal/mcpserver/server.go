// Package mcpserver wires the Hostelworld client into a Model Context Protocol
// server with three tools: search_hostels, get_hostel_details, get_booking_url.
//
// See DESIGN.md §3 (transport), §4 (library choice), §5 (tool API), and §6
// (the "show me more" exclusion model).
package mcpserver

import (
	"log/slog"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/nvpatel2002/hostelworld-mcp/internal/budget"
	"github.com/nvpatel2002/hostelworld-mcp/internal/hostelworld"
)

// Server bundles the MCP server with the dependencies tool handlers need.
type Server struct {
	mcp    *server.MCPServer
	client hostelworld.Client
	budget *budget.Counter
	logger *slog.Logger
}

func New(client hostelworld.Client, b *budget.Counter, logger *slog.Logger) *Server {
	s := &Server{
		mcp: server.NewMCPServer(
			"hostelworld-mcp",
			"0.1.0",
			server.WithToolCapabilities(false),
			server.WithRecovery(),
		),
		client: client,
		budget: b,
		logger: logger,
	}
	s.registerTools()
	return s
}

// HTTPHandler returns the Streamable HTTP handler for the MCP server. Wrap it
// with middleware (rate limit, request ID, budget peek) before mounting.
func (s *Server) HTTPHandler() *server.StreamableHTTPServer {
	return server.NewStreamableHTTPServer(s.mcp)
}

func (s *Server) registerTools() {
	s.mcp.AddTool(searchTool(), s.handleSearch)
	s.mcp.AddTool(detailsTool(), s.handleDetails)
	s.mcp.AddTool(bookingTool(), s.handleBookingURL)
}

func searchTool() mcp.Tool {
	return mcp.NewTool("search_hostels",
		mcp.WithDescription(
			"Search Hostelworld for properties in a city for given dates and guest count. "+
				"Returns up to 10 properties per call. To get more results without duplicates, "+
				"call again with the previously returned IDs in exclude_ids."),
		mcp.WithString("city",
			mcp.Required(),
			mcp.Description("City name, e.g. 'Lisbon'.")),
		mcp.WithString("checkin",
			mcp.Required(),
			mcp.Description("Check-in date, YYYY-MM-DD. Today or later.")),
		mcp.WithString("checkout",
			mcp.Required(),
			mcp.Description("Check-out date, YYYY-MM-DD. After checkin, max 30 nights.")),
		mcp.WithNumber("guests",
			mcp.Required(),
			mcp.Description("Number of guests, 1-16.")),
		mcp.WithString("currency",
			mcp.Description("ISO-4217 currency code, e.g. 'USD', 'EUR'. Defaults to USD.")),
		mcp.WithString("sort",
			mcp.Description("'recommended' (default), 'price_low', or 'rating'."),
			mcp.Enum("recommended", "price_low", "rating")),
		mcp.WithArray("exclude_ids",
			mcp.Description("Property IDs already shown in this conversation. Pass to avoid duplicates."),
			mcp.WithStringItems()),
	)
}

func detailsTool() mcp.Tool {
	return mcp.NewTool("get_hostel_details",
		mcp.WithDescription(
			"Get available rooms, prices, and amenities for a specific Hostelworld property "+
				"on given dates."),
		mcp.WithString("property_id",
			mcp.Required(),
			mcp.Description("Hostelworld property ID from a previous search_hostels result.")),
		mcp.WithString("checkin", mcp.Required(),
			mcp.Description("Check-in date, YYYY-MM-DD.")),
		mcp.WithString("checkout", mcp.Required(),
			mcp.Description("Check-out date, YYYY-MM-DD.")),
		mcp.WithNumber("guests", mcp.Required(),
			mcp.Description("Number of guests, 1-16.")),
		mcp.WithString("currency",
			mcp.Description("ISO-4217 currency code. Defaults to USD.")),
	)
}

func bookingTool() mcp.Tool {
	return mcp.NewTool("get_booking_url",
		mcp.WithDescription(
			"Construct a hostelworld.com booking URL pre-filled with property, dates, and "+
				"guests. The user completes payment on hostelworld.com — this server never "+
				"handles payment."),
		mcp.WithString("property_id", mcp.Required(),
			mcp.Description("Hostelworld property ID.")),
		mcp.WithString("room_type_id",
			mcp.Description("Optional. Identifies the room the user picked; not part of the URL but useful for the model to communicate to the user.")),
		mcp.WithString("checkin", mcp.Required(),
			mcp.Description("Check-in date, YYYY-MM-DD.")),
		mcp.WithString("checkout", mcp.Required(),
			mcp.Description("Check-out date, YYYY-MM-DD.")),
		mcp.WithNumber("guests", mcp.Required(),
			mcp.Description("Number of guests, 1-16.")),
	)
}
