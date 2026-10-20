package gen

import (
	"strconv"
	"strings"
)

// durationNanos turns a duration literal such as 5ms into its decimal
// nanosecond count. The units are the ones the grammar accepts; the returned
// text is what LLVM stores in an i64.
func durationNanos(text string) (string, bool) {
	text = strings.TrimSpace(text)
	i := 0
	for i < len(text) && text[i] >= '0' && text[i] <= '9' {
		i++
	}
	if i == 0 || i == len(text) {
		return "", false
	}
	number, err := strconv.ParseUint(text[:i], 10, 64)
	if err != nil {
		return "", false
	}
	scale := uint64(0)
	switch text[i:] {
	case "ns":
		scale = 1
	case "us":
		scale = 1000
	case "ms":
		scale = 1000 * 1000
	case "s":
		scale = 1000 * 1000 * 1000
	case "m":
		scale = 60 * 1000 * 1000 * 1000
	case "h":
		scale = 60 * 60 * 1000 * 1000 * 1000
	default:
		return "", false
	}
	if number > ^uint64(0)/scale {
		return "", false
	}
	return strconv.FormatUint(number*scale, 10), true
}
