package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain isolates the cache of every command test under one temporary root.
// Without it a test that builds would write to the user cache directory, and a
// later test could read an entry it did not create.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "will-cli-cache")
	if err == nil {
		os.Setenv("WILL_CACHE", dir)
		os.Setenv("WILL_MODCACHE", filepath.Join(dir, "mod"))
	}
	code := m.Run()
	if dir != "" {
		os.RemoveAll(dir)
	}
	os.Exit(code)
}
