package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/will-lang/will/internal/pkg"
)

// TestDirectoryCacheKeyCoversSubpackageFiles checks that a change in a package
// below the module root reaches the key. A file's path is relative to its
// module root, so joining it with the package directory silently reads nothing.
func TestDirectoryCacheKeyCoversDependencySources(t *testing.T) {
	base := t.TempDir()
	app := filepath.Join(base, "app")
	dep := filepath.Join(base, "dep")
	writeTree(t, app, map[string]string{
		"will.mod":  "module example.com/app\nwill 1\nrequire example.com/dep@v1.0.0\nreplace example.com/dep@v1.0.0 => ../dep\n",
		"main.will": "import \"example.com/dep\"\n\nfunc main() -> i32 {\n    return dep.Double(2)\n}\n",
	})
	writeTree(t, dep, map[string]string{
		"will.mod": "module example.com/dep\nwill 1\n",
		"dep.will": "func Double(n: i32) -> i32 {\n    return n + n\n}\n",
	})

	t.Setenv("WILL_CACHE", t.TempDir())
	_, first, ok := dirCacheKey(pkg.Load(app), checkFlags{})
	if !ok {
		t.Fatal("the module is not cacheable")
	}
	writeTree(t, dep, map[string]string{
		"dep.will": "func Double(n: i32) -> i32 {\n    return n + n + 1\n}\n",
	})
	_, changed, ok := dirCacheKey(pkg.Load(app), checkFlags{})
	if !ok {
		t.Fatal("the module is not cacheable")
	}
	if changed == first {
		t.Fatal("a change in a replaced dependency did not change the key")
	}
}

// writeTree writes the named files under root, creating directories.
func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
}

func TestDirectoryCacheKeyCoversSubpackageFiles(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WILL_CACHE", t.TempDir())
	files := map[string]string{
		"will.mod":     "module example.com/sub\nwill 1\n",
		"main.will":    "func main() -> i32 {\n    return 0\n}\n",
		"lib/lib.will": "func Double(n: i32) -> i32 {\n    return n + n\n}\n",
	}
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	_, first, ok := dirCacheKey(pkg.Load(root), checkFlags{})
	if !ok {
		t.Fatal("the module is not cacheable")
	}
	_, again, ok := dirCacheKey(pkg.Load(root), checkFlags{})
	if !ok || again != first {
		t.Fatalf("the same tree gave %q then %q", first, again)
	}

	lib := filepath.Join(root, "lib", "lib.will")
	if err := os.WriteFile(lib, []byte("func Double(n: i32) -> i32 {\n    return n + n + 1\n}\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, changed, ok := dirCacheKey(pkg.Load(root), checkFlags{})
	if !ok {
		t.Fatal("the module is not cacheable")
	}
	if changed == first {
		t.Fatal("a change in a subpackage did not change the key")
	}
}
