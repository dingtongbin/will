// Package cache stores build products under a content-addressed key. This file
// holds the environment rules that decide where the store lives and how large
// it may grow; a usage error is reported to the caller so the command can exit
// with the usage code instead of guessing a default.
package cache

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// DefaultLimit is the size limit when WILL_CACHE_MAX is unset or zero: 4 GiB.
const DefaultLimit int64 = 4 << 30

// Root returns the cache directory. WILL_CACHE wins; otherwise the store lives
// under the user cache directory as will. The value must be an absolute path,
// because a relative one would silently depend on the working directory.
func Root() (string, error) {
	if dir := os.Getenv("WILL_CACHE"); dir != "" {
		if !filepath.IsAbs(dir) {
			return "", fmt.Errorf("WILL_CACHE must be an absolute path, got %q", dir)
		}
		return dir, nil
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("cannot find the user cache directory: %v", err)
	}
	return filepath.Join(dir, "will"), nil
}

// ModRoot returns the module cache directory. WILL_MODCACHE wins; otherwise it
// is mod under the cache root. The same absolute-path rule applies.
func ModRoot() (string, error) {
	if dir := os.Getenv("WILL_MODCACHE"); dir != "" {
		if !filepath.IsAbs(dir) {
			return "", fmt.Errorf("WILL_MODCACHE must be an absolute path, got %q", dir)
		}
		return dir, nil
	}
	root, err := Root()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "mod"), nil
}

// Limit returns the trim limit in bytes. WILL_CACHE_MAX takes a decimal byte
// count or an integer followed by K, M, or G for a power of 1024. Unset and
// zero mean DefaultLimit. A negative value, a fraction, a space, an unknown
// suffix, or an overflow is a usage error.
func Limit() (int64, error) {
	text := os.Getenv("WILL_CACHE_MAX")
	if text == "" {
		return DefaultLimit, nil
	}
	digits := text
	unit := int64(1)
	if last := text[len(text)-1]; last < '0' || last > '9' {
		switch last {
		case 'K':
			unit = 1 << 10
		case 'M':
			unit = 1 << 20
		case 'G':
			unit = 1 << 30
		default:
			return 0, fmt.Errorf("WILL_CACHE_MAX must be bytes or <n>K, <n>M, or <n>G, got %q", text)
		}
		digits = text[:len(text)-1]
	}
	if digits == "" || !allDigits(digits) {
		return 0, fmt.Errorf("WILL_CACHE_MAX must be bytes or <n>K, <n>M, or <n>G, got %q", text)
	}
	value, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("WILL_CACHE_MAX is out of range: %q", text)
	}
	if value < 0 {
		return 0, fmt.Errorf("WILL_CACHE_MAX must not be negative, got %q", text)
	}
	if value > (1<<63-1)/unit {
		return 0, fmt.Errorf("WILL_CACHE_MAX is out of range: %q", text)
	}
	value *= unit
	if value == 0 {
		return DefaultLimit, nil
	}
	return value, nil
}

// allDigits reports whether text is one or more ASCII decimal digits.
func allDigits(text string) bool {
	for i := 0; i < len(text); i++ {
		if text[i] < '0' || text[i] > '9' {
			return false
		}
	}
	return true
}
