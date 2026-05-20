package mcpserver

import (
	"fmt"
	"strings"
	"time"

	hwerr "github.com/nvpatel2002/hostelworld-mcp/internal/errors"
)

const dateFormat = "2006-01-02"

// validateDates parses checkin/checkout, asserts checkout > checkin, both are
// today-or-later, and the stay is at most 30 nights.
func validateDates(checkin, checkout string) error {
	ci, err := time.Parse(dateFormat, checkin)
	if err != nil {
		return hwerr.InvalidInput("checkin must be YYYY-MM-DD")
	}
	co, err := time.Parse(dateFormat, checkout)
	if err != nil {
		return hwerr.InvalidInput("checkout must be YYYY-MM-DD")
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	if ci.Before(today) {
		return hwerr.InvalidInput("checkin must be today or later")
	}
	if !co.After(ci) {
		return hwerr.InvalidInput("checkout must be after checkin")
	}
	nights := int(co.Sub(ci).Hours() / 24)
	if nights > 30 {
		return hwerr.InvalidInput(fmt.Sprintf("stay too long (%d nights); max 30", nights))
	}
	return nil
}

func validateGuests(guests int) error {
	if guests < 1 || guests > 16 {
		return hwerr.InvalidInput("guests must be between 1 and 16")
	}
	return nil
}

func validateCurrency(c string) (string, error) {
	if c == "" {
		return "USD", nil
	}
	c = strings.ToUpper(c)
	if len(c) != 3 {
		return "", hwerr.InvalidInput("currency must be a 3-letter ISO-4217 code")
	}
	for _, r := range c {
		if r < 'A' || r > 'Z' {
			return "", hwerr.InvalidInput("currency must be a 3-letter ISO-4217 code")
		}
	}
	return c, nil
}

func validateExcludeIDs(ids []string) error {
	if len(ids) > 200 {
		return hwerr.InvalidInput("exclude_ids capped at 200 entries")
	}
	return nil
}
