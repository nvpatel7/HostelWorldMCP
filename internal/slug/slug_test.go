package slug

import "testing"

func TestMake(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		// The real example from Hostelworld: ensures we match the canonical
		// slug for known property/city names.
		{"Revolution Khao San by The Bliss", "Revolution-Khao-San-by-The-Bliss"},
		{"Bangkok", "Bangkok"},
		{"Yes! Lisbon Hostel", "Yes-Lisbon-Hostel"},
		{"  leading and trailing  ", "leading-and-trailing"},
		{"multiple   spaces", "multiple-spaces"},
		{"Cais do Sodré", "Cais-do-Sodr"}, // non-ASCII dropped
		{"", ""},
		{"---", ""},
		{"with-existing-hyphens", "with-existing-hyphens"},
		{"numbers123 ok", "numbers123-ok"},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got := Make(c.in)
			if got != c.want {
				t.Errorf("Make(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
