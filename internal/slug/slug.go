package slug

import (
	"strings"
	"unicode"
)

// Make slugifies a string for use in a Hostelworld booking URL path segment.
// Hostelworld URLs look like:
//
//	/pwa/hosteldetails.php/Revolution-Khao-San-by-The-Bliss/Bangkok/326223?...
//
// Words are joined by hyphens, capitalisation is preserved when present, and
// non-ASCII letters are dropped (Hostelworld's own slugs appear to be ASCII).
//
// If the upstream property API returns a canonical slug field, prefer it over
// calling this — see DESIGN.md §8.4.
func Make(s string) string {
	var b strings.Builder
	b.Grow(len(s))

	prevHyphen := true // suppresses leading hyphens
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) && r < 128:
			b.WriteRune(r)
			prevHyphen = false
		case unicode.IsDigit(r):
			b.WriteRune(r)
			prevHyphen = false
		case prevHyphen:
			// collapse runs of separators
		default:
			b.WriteByte('-')
			prevHyphen = true
		}
	}

	out := b.String()
	return strings.TrimRight(out, "-")
}
