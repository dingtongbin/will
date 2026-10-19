package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeModule writes a module whose only dependency is a local directory, so
// the checksum can be computed without fetching anything.
func writeModule(t *testing.T) (root string) {
	t.Helper()
	parent := t.TempDir()
	root = filepath.Join(parent, "app")
	lib := filepath.Join(parent, "lib")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, root, "will.mod", "module example.com/app\nwill 1\nrequire example.com/lib@v1.0.0\nreplace example.com/lib@v1.0.0 => ../lib\n")
	writeFile(t, root, "app.will", "func F() {\n}\n")
	writeFile(t, lib, "will.mod", "module example.com/lib\nwill 1\n")
	writeFile(t, lib, "lib.will", "func Util() {\n}\n")
	return root
}

func TestModTidyThenVerify(t *testing.T) {
	root := writeModule(t)
	if code, out, errOut := runCLI("mod", "tidy", root); code != 0 {
		t.Fatalf("tidy: code = %d, stdout = %q, stderr = %q", code, out, errOut)
	}
	sum, err := os.ReadFile(filepath.Join(root, "will.sum"))
	if err != nil {
		t.Fatalf("read will.sum: %v", err)
	}
	if !strings.HasPrefix(string(sum), "example.com/lib sha256:") {
		t.Fatalf("will.sum = %q", sum)
	}
	if code, out, _ := runCLI("mod", "verify", root); code != 0 {
		t.Fatalf("verify: code = %d, stdout = %q", code, out)
	}
}

func TestModVerifyReportsMismatch(t *testing.T) {
	root := writeModule(t)
	if code, _, _ := runCLI("mod", "tidy", root); code != 0 {
		t.Fatalf("tidy failed")
	}
	// A file outside the source face does not change the sum.
	writeFile(t, filepath.Join(filepath.Dir(root), "lib"), "README", "notes\n")
	if code, out, _ := runCLI("mod", "verify", root); code != 0 {
		t.Fatalf("a README changed the checksum: %q", out)
	}
	// Changing the dependency's source must change its checksum.
	writeFile(t, filepath.Join(filepath.Dir(root), "lib"), "lib.will", "func Util() -> i32 {\n    return 1\n}\n")
	code, out, _ := runCLI("mod", "verify", root)
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(out, "B0003 checksum mismatch for example.com/lib") ||
		!strings.Contains(out, "    help: run will mod tidy") {
		t.Fatalf("stdout = %q", out)
	}
}

func TestModUsage(t *testing.T) {
	if code, _, _ := runCLI("mod"); code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if code, _, _ := runCLI("mod", "nosuch"); code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
}
