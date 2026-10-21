package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/will-lang/will/internal/cache"
	"github.com/will-lang/will/internal/toolchain"
)

// TestLinkCacheKeyIgnoresTemporaryPath pins that package C inputs are hashed by
// name and bytes, not by the fresh temporary directory they live in.
func TestLinkCacheKeyIgnoresTemporaryPath(t *testing.T) {
	t.Setenv("WILL_CACHE", t.TempDir())
	first, second := t.TempDir(), t.TempDir()
	write := func(dir string) string {
		path := filepath.Join(dir, "shim.bc")
		if err := os.WriteFile(path, []byte("same bitcode"), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	a := write(first)
	b := write(second)
	clang := &toolchain.Clang{Path: "clang"}
	_, keyA := linkCacheKey([]byte("ir"), clang, []string{"-O2", a})
	_, keyB := linkCacheKey([]byte("ir"), clang, []string{"-O2", b})
	if keyA == "" || keyA != keyB {
		t.Fatalf("temporary paths changed the key: %q != %q", keyA, keyB)
	}
}

func TestLinkCacheKeyChangesWithContents(t *testing.T) {
	t.Setenv("WILL_CACHE", t.TempDir())
	path := filepath.Join(t.TempDir(), "shim.bc")
	if err := os.WriteFile(path, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	clang := &toolchain.Clang{Path: "clang"}
	_, first := linkCacheKey([]byte("ir"), clang, []string{path})
	if err := os.WriteFile(path, []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, second := linkCacheKey([]byte("ir"), clang, []string{path})
	if first == second {
		t.Fatal("changed input contents reused the link key")
	}
}

func TestLinkCacheCopiesArtifact(t *testing.T) {
	root := t.TempDir()
	store := cache.New(root, 0)
	key := "abc"
	artifact := filepath.Join(t.TempDir(), "program.exe")
	if err := os.WriteFile(artifact, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	rememberLink(store, key, artifact)
	out := filepath.Join(t.TempDir(), "copy.exe")
	if !cachedLink(store, key, out) {
		t.Fatal("cachedLink missed a stored artifact")
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "binary" {
		t.Fatalf("artifact = %q, want binary", got)
	}
}
