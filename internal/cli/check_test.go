package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const goodSource = `func Add(a: i32, b: i32) -> i32 {
    return a + b
}
`

const badSource = `func F() {
    var x: i32 = "s"
    _ = x
}
`

func writeFile(t *testing.T, dir, name, src string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func runCLI(args ...string) (int, string, string) {
	var out, errBuf bytes.Buffer
	code := Run(args, &out, &errBuf)
	return code, out.String(), errBuf.String()
}

func TestCheckGoodFile(t *testing.T) {
	path := writeFile(t, t.TempDir(), "good.will", goodSource)
	code, out, errOut := runCLI("check", path)
	if code != 0 || out != "" || errOut != "" {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, out, errOut)
	}
}

func TestCheckBadFileHuman(t *testing.T) {
	path := writeFile(t, t.TempDir(), "bad.will", badSource)
	code, out, _ := runCLI("check", path)
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(out, "T0001 type mismatch: got str, want i32") {
		t.Fatalf("stdout = %q", out)
	}
	if !strings.Contains(out, "    help: convert explicitly with T(x)") {
		t.Fatalf("stdout lacks the help line: %q", out)
	}
}

func TestCheckBadFileJSON(t *testing.T) {
	path := writeFile(t, t.TempDir(), "bad.will", badSource)
	code, out, _ := runCLI("check", "--json", path)
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.HasPrefix(out, `{"file":"`) {
		t.Fatalf("stdout is not JSON Lines: %q", out)
	}
	if !strings.Contains(out, `"code":"T0001"`) || !strings.Contains(out, `"severity":"error"`) {
		t.Fatalf("stdout = %q", out)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Fatal("JSON Lines output does not end with a newline")
	}
}

func TestCheckUsageErrors(t *testing.T) {
	if code, _, errOut := runCLI("check"); code != 2 || !strings.Contains(errOut, "usage:") {
		t.Fatalf("code = %d, stderr = %q", code, errOut)
	}
	if code, _, _ := runCLI("check", "--nope", "x.will"); code != 2 {
		t.Fatalf("unknown flag code = %d, want 2", code)
	}
	if code, _, _ := runCLI("nope"); code != 2 {
		t.Fatalf("unknown command code = %d, want 2", code)
	}
}

func TestCheckMissingFile(t *testing.T) {
	code, _, errOut := runCLI("check", filepath.Join(t.TempDir(), "missing.will"))
	if code != 1 || errOut == "" {
		t.Fatalf("code = %d, stderr = %q", code, errOut)
	}
}

func TestCheckModuleDirectory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "app")
	writeModuleFile(t, filepath.Join(root, "will.mod"), "module example.com/app\nwill 1\n")
	writeModuleFile(t, filepath.Join(root, "type.will"), "struct T {\n    x: i32\n}\n")
	writeModuleFile(t, filepath.Join(root, "sub", "method.will"), "func (t: T) Move() {\n}\n")

	code, out, _ := runCLI("check", root)
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(out, "N0006 methods of T are declared in two packages") {
		t.Fatalf("stdout = %q", out)
	}
}

func TestCheckModuleDirectoryGood(t *testing.T) {
	root := filepath.Join(t.TempDir(), "app")
	writeModuleFile(t, filepath.Join(root, "will.mod"), "module example.com/app\nwill 1\n")
	writeModuleFile(t, filepath.Join(root, "main.will"), "func Main() {\n}\n")
	code, out, errOut := runCLI("check", root)
	if code != 0 || out != "" || errOut != "" {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, out, errOut)
	}
}

func writeModuleFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestCheckWerror(t *testing.T) {
	// A local name that shadows a predeclared name is a warning (N0003).
	src := "func F() {\n    var i32: i32 = 0\n    _ = i32\n}\n"
	path := writeFile(t, t.TempDir(), "warn.will", src)
	if code, out, _ := runCLI("check", path); code != 0 || !strings.Contains(out, "N0003") {
		t.Fatalf("warning-only check: code = %d, stdout = %q", code, out)
	}
	if code, _, _ := runCLI("check", "-Werror", path); code != 1 {
		t.Fatalf("-Werror code = %d, want 1", code)
	}
}
