package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeIn creates dir and writes one file under it.
func writeIn(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	writeFile(t, dir, name, body)
}

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
	writeFile(t, root, "app.will", "def F() {\n}\n")
	writeIn(t, lib, "will.mod", "module example.com/lib\nwill 1\n")
	writeIn(t, lib, "lib.will", "def Util() {\n}\n")
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
	writeFile(t, filepath.Join(filepath.Dir(root), "lib"), "lib.will", "def Util() -> i32 {\n    return 1\n}\n")
	code, out, _ := runCLI("mod", "verify", root)
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(out, "B0003 checksum mismatch for example.com/lib") ||
		!strings.Contains(out, "    help: run will mod tidy") {
		t.Fatalf("stdout = %q", out)
	}
}

func TestModTidyCoversTransitiveDependencies(t *testing.T) {
	parent := t.TempDir()
	app := filepath.Join(parent, "app")
	a := filepath.Join(parent, "a")
	b := filepath.Join(parent, "b")
	writeIn(t, app, "will.mod", "module example.com/app\nwill 1\nrequire example.com/a@v1.0.0\nreplace example.com/a@v1.0.0 => ../a\n")
	writeIn(t, app, "app.will", "def F() {\n}\n")
	writeIn(t, a, "will.mod", "module example.com/a\nwill 1\nrequire example.com/b@v1.0.0\nreplace example.com/b@v1.0.0 => ../b\n")
	writeIn(t, a, "a.will", "def A() {\n}\n")
	writeIn(t, b, "will.mod", "module example.com/b\nwill 1\n")
	writeIn(t, b, "b.will", "def B() {\n}\n")

	if code, out, errOut := runCLI("mod", "tidy", app); code != 0 {
		t.Fatalf("tidy: code = %d, stdout = %q, stderr = %q", code, out, errOut)
	}
	sum, err := os.ReadFile(filepath.Join(app, "will.sum"))
	if err != nil {
		t.Fatalf("read will.sum: %v", err)
	}
	for _, want := range []string{"example.com/a sha256:", "example.com/b sha256:"} {
		if !strings.Contains(string(sum), want) {
			t.Fatalf("will.sum lacks %q:\n%s", want, sum)
		}
	}
	if code, out, _ := runCLI("mod", "verify", app); code != 0 {
		t.Fatalf("verify: code = %d, stdout = %q", code, out)
	}
	writeIn(t, b, "b.will", "def B() -> i32 {\n    return 1\n}\n")
	code, out, _ := runCLI("mod", "verify", app)
	if code != 1 || !strings.Contains(out, "B0003 checksum mismatch for example.com/b") {
		t.Fatalf("after a transitive change: code = %d, stdout = %q", code, out)
	}
}

func TestModVerifyRequiresALineForEveryLocalModule(t *testing.T) {
	parent := t.TempDir()
	app := filepath.Join(parent, "app")
	lib := filepath.Join(parent, "lib")
	writeIn(t, app, "will.mod", "module example.com/app\nwill 1\nrequire example.com/lib@v1.0.0\nreplace example.com/lib@v1.0.0 => ../lib\n")
	writeIn(t, app, "app.will", "def F() {\n}\n")
	writeIn(t, lib, "will.mod", "module example.com/lib\nwill 1\n")
	writeIn(t, lib, "lib.will", "def Util() {\n}\n")
	if code, _, _ := runCLI("mod", "tidy", app); code != 0 {
		t.Fatalf("tidy failed")
	}
	if err := os.Remove(filepath.Join(app, "will.sum")); err != nil {
		t.Fatalf("remove will.sum: %v", err)
	}
	code, out, _ := runCLI("mod", "verify", app)
	if code != 1 || !strings.Contains(out, "B0003 no checksum for example.com/lib") {
		t.Fatalf("code = %d, stdout = %q", code, out)
	}
}

func TestModTidyCoversAnAliasReplace(t *testing.T) {
	parent := t.TempDir()
	app := filepath.Join(parent, "app")
	shared := filepath.Join(parent, "shared")
	writeIn(t, app, "will.mod", "module example.com/app\nwill 1\nrequire example.com/a@v1.0.0\nreplace example.com/a@v1.0.0 => ../shared\n")
	writeIn(t, app, "app.will", "def F() {\n}\n")
	writeIn(t, shared, "will.mod", "module example.com/shared\nwill 1\n")
	writeIn(t, shared, "shared.will", "def S() {\n}\n")

	if code, out, errOut := runCLI("mod", "tidy", app); code != 0 {
		t.Fatalf("tidy: code = %d, stdout = %q, stderr = %q", code, out, errOut)
	}
	sum, err := os.ReadFile(filepath.Join(app, "will.sum"))
	if err != nil {
		t.Fatalf("read will.sum: %v", err)
	}
	if !strings.Contains(string(sum), "example.com/a sha256:") {
		t.Fatalf("will.sum does not name the require path:\n%s", sum)
	}
	if code, out, _ := runCLI("mod", "verify", app); code != 0 {
		t.Fatalf("verify: code = %d, stdout = %q", code, out)
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
