package mcpserver

import (
	"testing"
	"time"
)

func TestValidateDates(t *testing.T) {
	today := time.Now().UTC().Format("2006-01-02")
	tomorrow := time.Now().UTC().AddDate(0, 0, 1).Format("2006-01-02")
	dayAfter := time.Now().UTC().AddDate(0, 0, 2).Format("2006-01-02")
	farFuture := time.Now().UTC().AddDate(0, 0, 40).Format("2006-01-02")

	cases := []struct {
		name              string
		ci, co            string
		wantErr           bool
	}{
		{"valid one night", today, tomorrow, false},
		{"valid two nights", today, dayAfter, false},
		{"bad checkin format", "not-a-date", tomorrow, true},
		{"bad checkout format", today, "nope", true},
		{"checkout before checkin", tomorrow, today, true},
		{"same day", today, today, true},
		{"too long", today, farFuture, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateDates(c.ci, c.co)
			if (err != nil) != c.wantErr {
				t.Errorf("validateDates(%q,%q) err = %v, wantErr = %v", c.ci, c.co, err, c.wantErr)
			}
		})
	}
}

func TestValidateGuests(t *testing.T) {
	for _, g := range []int{1, 2, 8, 16} {
		if err := validateGuests(g); err != nil {
			t.Errorf("guests=%d should be valid, got %v", g, err)
		}
	}
	for _, g := range []int{0, -1, 17, 100} {
		if err := validateGuests(g); err == nil {
			t.Errorf("guests=%d should be invalid", g)
		}
	}
}

func TestValidateCurrency(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"", "USD", false},
		{"usd", "USD", false},
		{"EUR", "EUR", false},
		{"GBP", "GBP", false},
		{"US", "", true},
		{"USDX", "", true},
		{"US1", "", true},
	}
	for _, c := range cases {
		got, err := validateCurrency(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("validateCurrency(%q) err = %v, wantErr = %v", c.in, err, c.wantErr)
			continue
		}
		if !c.wantErr && got != c.want {
			t.Errorf("validateCurrency(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
