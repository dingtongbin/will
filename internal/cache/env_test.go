package cache

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLimitAcceptsBytesAndPowersOf1024(t *testing.T) {
	cases := map[string]int64{
		"4096": 4096,
		"4K":   4 << 10,
		"2M":   2 << 20,
		"1G":   1 << 30,
		"0":    DefaultLimit,
		"0K":   DefaultLimit,
		"":     DefaultLimit,
	}
	for text, want := range cases {
		t.Setenv("WILL_CACHE_MAX", text)
		got, err := Limit()
		if err != nil {
			t.Errorf("Limit(%q) failed: %v", text, err)
			continue
		}
		if got != want {
			t.Errorf("Limit(%q) = %d, want %d", text, got, want)
		}
	}
}

func TestLimitRejectsUsageErrors(t *testing.T) {
	for _, text := range []string{"-1", "-4K", "1.5M", " 4K", "4K ", "unlimited", "4T", "K", "99999999999999999999"} {
		t.Run(text, func(t *testing.T) {
			t.Setenv("WILL_CACHE_MAX", text)
			if _, err := Limit(); err == nil {
				t.Fatalf("Limit(%q) succeeded, want a usage error", text)
			}
		})
	}
}

func TestRootDefaultsUnderTheUserCache(t *testing.T) {
	t.Setenv("WILL_CACHE", "")
	user, err := os.UserCacheDir()
	if err != nil {
		t.Skip("no user cache directory on this machine")
	}
	got, err := Root()
	if err != nil {
		t.Fatalf("Root: %v", err)
	}
	if want := filepath.Join(user, "will"); got != want {
		t.Fatalf("Root = %q, want %q", got, want)
	}
}

func TestRootAndModRootAcceptAbsolutePaths(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WILL_CACHE", root)
	t.Setenv("WILL_MODCACHE", filepath.Join(root, "modules"))
	got, err := Root()
	if err != nil || got != root {
		t.Fatalf("Root = %q, %v, want %q", got, err, root)
	}
	mod, err := ModRoot()
	if err != nil || mod != filepath.Join(root, "modules") {
		t.Fatalf("ModRoot = %q, %v", mod, err)
	}
}

func TestModRootDefaultsUnderTheCacheRoot(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WILL_CACHE", root)
	t.Setenv("WILL_MODCACHE", "")
	got, err := ModRoot()
	if err != nil {
		t.Fatalf("ModRoot: %v", err)
	}
	if want := filepath.Join(root, "mod"); got != want {
		t.Fatalf("ModRoot = %q, want %q", got, want)
	}
}

func TestRelativeCachePathsAreUsageErrors(t *testing.T) {
	t.Setenv("WILL_CACHE", "relative/cache")
	if _, err := Root(); err == nil {
		t.Fatal("a relative WILL_CACHE was accepted")
	}
	t.Setenv("WILL_CACHE", t.TempDir())
	t.Setenv("WILL_MODCACHE", "relative/modules")
	if _, err := ModRoot(); err == nil {
		t.Fatal("a relative WILL_MODCACHE was accepted")
	}
}
