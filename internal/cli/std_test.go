package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stdModule writes a module that requires std and replaces the requirement with
// the given local tree.
func stdModule(t *testing.T, std string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "app")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, root, "will.mod",
		"module example.com/app\nwill 1\nrequire std@v0.1.0\nreplace std@v0.1.0 => "+filepath.ToSlash(std)+"\n")
	writeFile(t, root, "main.will",
		"import \"std/net/http\"\n\nfunc F() -> i32 {\n    return http.Get()\n}\n")
	return root
}

// writeStdFile writes one file of a local standard library tree, creating its
// parent directories.
func writeStdFile(t *testing.T, dir, name, src string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	writeFile(t, dir, name, src)
}

// TestCheckResolvesStdThroughReplace checks that an explicit require plus a
// local replace resolves a std package.
func TestCheckResolvesStdThroughReplace(t *testing.T) {
	std := filepath.Join(t.TempDir(), "std")
	writeStdFile(t, std, "will.mod", "module std\nwill 1\n")
	writeStdFile(t, filepath.Join(std, "net", "http"), "http.will",
		"func Get() -> i32 {\n    return 1\n}\n")
	root := stdModule(t, std)

	code, out, errOut := runCLI("check", root)
	if code != 0 || out != "" || errOut != "" {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, out, errOut)
	}
}

// TestCheckStdWithoutRequireIsB0008 checks that the toolchain has no implicit
// standard library dependency.
func TestCheckStdWithoutRequireIsB0008(t *testing.T) {
	root := filepath.Join(t.TempDir(), "app")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, root, "will.mod", "module example.com/app\nwill 1\n")
	writeFile(t, root, "main.will",
		"import \"std/net/http\"\n\nfunc F() -> i32 {\n    return http.Get()\n}\n")

	code, out, _ := runCLI("check", root)
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(out, "B0008 unknown import path std/net/http") {
		t.Fatalf("stdout = %q", out)
	}
}

// TestCheckStdMissingSubpackageIsB0008 checks that an existing replaced root
// without the imported subpackage stays an import path error.
func TestCheckStdMissingSubpackageIsB0008(t *testing.T) {
	std := filepath.Join(t.TempDir(), "std")
	writeStdFile(t, std, "will.mod", "module std\nwill 1\n")
	writeStdFile(t, filepath.Join(std, "net", "other"), "other.will",
		"func Get() -> i32 {\n    return 1\n}\n")
	root := stdModule(t, std)

	code, out, _ := runCLI("check", root)
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(out, "B0008 unknown import path std/net/http") {
		t.Fatalf("stdout = %q", out)
	}
}
