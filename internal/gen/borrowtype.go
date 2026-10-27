package gen

import "strings"

// borrowText splits an explicit borrow prefix from a canonical type text.
// kind is "ref" or "mut" for ref T and mut T; mut []T is a mutable view and
// keeps the view layout, so it reports kind "mutview".
func borrowText(text string) (base, kind string) {
	if strings.HasPrefix(text, "ref ") {
		return strings.TrimPrefix(text, "ref "), "ref"
	}
	if strings.HasPrefix(text, "mut []") {
		return strings.TrimPrefix(text, "mut "), "mutview"
	}
	if strings.HasPrefix(text, "mut ") {
		return strings.TrimPrefix(text, "mut "), "mut"
	}
	return text, ""
}

func isSingleBorrow(text string) bool {
	_, kind := borrowText(text)
	return kind == "ref" || kind == "mut"
}

func baseBorrowText(text string) string {
	base, _ := borrowText(text)
	return base
}
