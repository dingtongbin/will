package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stdModule writes a module that requires and imports the bundled standard
// library, returning its root.
func stdModule(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "app")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, root, "will.mod", "module example.com/app\nwill 1\nrequire std@bundled\n")
	writeFile(t, root, "main.will",
		"import \"std/net/http\"\n\nfunc F() -> i32 {\n    return http.Get()\n}\n")
	return root
}

// writeStdFile writes one file of a bundled standard library tree, creating
// its parent directories.
func writeStdFile(t *testing.T, dir, name, src string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	writeFile(t, dir, name, src)
}

// TestCheckMissingStdRootExitsTwo checks that a missing bundled standard
// library is a startup error: one stderr line and exit code 2, not B0008.
func TestCheckMissingStdRootExitsTwo(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "std")
	t.Setenv("WILL_ROOT", missing)
	root := stdModule(t)

	code, out, errOut := runCLI("check", root)
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if out != "" {
		t.Fatalf("stdout = %q, want empty", out)
	}
	want := "will: the bundled standard library is missing: " + filepath.Clean(missing) + " (set WILL_ROOT)\n"
	if errOut != want {
		t.Fatalf("stderr = %q, want %q", errOut, want)
	}
}

// TestBuildMissingStdRootExitsTwo checks that every command that loads a
// module turns the same fault into exit code 2.
func TestBuildMissingStdRootExitsTwo(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "std")
	t.Setenv("WILL_ROOT", missing)
	root := stdModule(t)

	code, out, _ := runCLI("build", root)
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if out != "" {
		t.Fatalf("stdout = %q, want empty", out)
	}
}

// TestCheckResolvesBundledStd checks that WILL_ROOT supplies std packages to
// the whole command line, so an import under std resolves and checks.
func TestCheckResolvesBundledStd(t *testing.T) {
	std := filepath.Join(t.TempDir(), "std")
	writeStdFile(t, std, "will.mod", "module std\nwill 1\n")
	writeStdFile(t, filepath.Join(std, "net", "http"), "http.will",
		"func Get() -> i32 {\n    return 1\n}\n")
	t.Setenv("WILL_ROOT", std)
	root := stdModule(t)

	code, out, errOut := runCLI("check", root)
	if code != 0 || out != "" || errOut != "" {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, out, errOut)
	}
}

// TestCheckStdSubpackageMissingIsB0008 checks that an existing root with a
// missing subpackage stays an import path error.
func TestCheckStdSubpackageMissingIsB0008(t *testing.T) {
	std := filepath.Join(t.TempDir(), "std")
	writeStdFile(t, std, "will.mod", "module std\nwill 1\n")
	writeStdFile(t, filepath.Join(std, "net", "other"), "other.will",
		"func Get() -> i32 {\n    return 1\n}\n")
	t.Setenv("WILL_ROOT", std)
	root := stdModule(t)

	code, out, _ := runCLI("check", root)
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(out, "B0008 unknown import path std/net/http") {
		t.Fatalf("stdout = %q", out)
	}
}
