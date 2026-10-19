// Package mod parses will.mod and selects the module versions of one build.
package mod

import (
	"strconv"
	"strings"
)

// Version is a v<major>.<minor>.<patch> version.
type Version struct {
	Major int
	Minor int
	Patch int
}

// ParseVersion reads a v<major>.<minor>.<patch> version.
func ParseVersion(s string) (Version, bool) {
	if len(s) < 2 || s[0] != 'v' {
		return Version{}, false
	}
	parts := strings.Split(s[1:], ".")
	if len(parts) != 3 {
		return Version{}, false
	}
	nums := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || !allDigits(p) {
			return Version{}, false
		}
		nums[i] = n
	}
	return Version{Major: nums[0], Minor: nums[1], Patch: nums[2]}, true
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// Compare returns -1, 0, or 1.
func (v Version) Compare(o Version) int {
	switch {
	case v.Major != o.Major:
		return cmpInt(v.Major, o.Major)
	case v.Minor != o.Minor:
		return cmpInt(v.Minor, o.Minor)
	}
	return cmpInt(v.Patch, o.Patch)
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// String writes the canonical version text.
func (v Version) String() string {
	return "v" + strconv.Itoa(v.Major) + "." + strconv.Itoa(v.Minor) + "." + strconv.Itoa(v.Patch)
}
