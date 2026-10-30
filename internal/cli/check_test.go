package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const goodSource = `def add(a: i32, b: i32) -> i32 {
    return a + b
}
`

const badSource = `def f() {
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

// warningSource declares a local name that shadows a predeclared type, which
// is a warning and not an error.
const warningSource = `def f() {
    var i32: i32 = 0
    _ = i32
}
`

func TestWarningFamilyOff(t *testing.T) {
	path := writeFile(t, t.TempDir(), "shadow.will", warningSource)
	code, out, _ := runCLI("check", path)
	if code != 0 || !strings.Contains(out, "N0003 this name shadows i32") {
		t.Fatalf("code = %d, stdout = %q", code, out)
	}
	code, out, _ = runCLI("check", "-Wno-shadow", path)
	if code != 0 || out != "" {
		t.Fatalf("with -Wno-shadow: code = %d, stdout = %q", code, out)
	}
}

func TestWarningBecomesError(t *testing.T) {
	path := writeFile(t, t.TempDir(), "shadow.will", warningSource)
	code, out, _ := runCLI("check", "-Werror", path)
	if code != 1 || !strings.Contains(out, "N0003") {
		t.Fatalf("code = %d, stdout = %q", code, out)
	}
}

func TestUnknownWarningFamily(t *testing.T) {
	path := writeFile(t, t.TempDir(), "shadow.will", warningSource)
	code, _, errOut := runCLI("check", "-Wno-nosuch", path)
	if code != 2 || !strings.Contains(errOut, "unknown warning family nosuch") {
		t.Fatalf("code = %d, stderr = %q", code, errOut)
	}
}

func TestCheckTargetFlagReachesJSON(t *testing.T) {
	path := writeFile(t, t.TempDir(), "bad.will", badSource)
	code, out, _ := runCLI("check", "--json", "-O2", "--target", "aarch64-linux-gnu", path)
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(out, `"target":"aarch64-linux-gnu"`) || !strings.Contains(out, `"opt":"-O2"`) {
		t.Fatalf("build identity lacks the flags: %q", out)
	}
}

func TestCheckLongEchoesTheSource(t *testing.T) {
	path := writeFile(t, t.TempDir(), "bad.will", badSource)
	code, out, _ := runCLI("check", "--long", path)
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(out, "\n        var x: i32 = \"s\"\n") {
		t.Fatalf("stdout lacks the source line: %q", out)
	}
	if !strings.Contains(out, "^\n") {
		t.Fatalf("stdout lacks the caret: %q", out)
	}
	// The canonical form stays free of the echo.
	_, plain, _ := runCLI("check", path)
	if strings.Contains(plain, "var n: i32") {
		t.Fatalf("the canonical form echoes source: %q", plain)
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

func TestCheckCacheHits(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "bad.will", badSource)
	cacheDir := filepath.Join(dir, "cache")
	t.Setenv("WILL_CACHE", cacheDir)

	code1, out1, _ := runCLI("check", path)
	if code1 != 1 || out1 == "" {
		t.Fatalf("first run: code = %d, stdout = %q", code1, out1)
	}
	if !strings.HasPrefix(out1, "T") && !strings.Contains(out1, "T0001") {
		t.Fatalf("first run output = %q", out1)
	}
	entries, err := os.ReadDir(cacheDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("cache entries = %v, err = %v", entries, err)
	}

	code2, out2, _ := runCLI("check", path)
	if code2 != code1 || out2 != out1 {
		t.Fatalf("second run differed: code = %d, stdout = %q", code2, out2)
	}
}

func TestCheckCacheMissAfterChange(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "bad.will", badSource)
	t.Setenv("WILL_CACHE", filepath.Join(dir, "cache"))

	if code, _, _ := runCLI("check", path); code != 1 {
		t.Fatalf("first run code = %d", code)
	}
	writeFile(t, dir, "bad.will", goodSource)
	code, out, _ := runCLI("check", path)
	if code != 0 || out != "" {
		t.Fatalf("changed file hit the old entry: code = %d, stdout = %q", code, out)
	}
}

func TestCheckModuleDirectoryGood(t *testing.T) {
	root := filepath.Join(t.TempDir(), "app")
	writeModuleFile(t, filepath.Join(root, "will.mod"), "module example.com/app\nwill 1\n")
	writeModuleFile(t, filepath.Join(root, "main.will"), "def main() {\n}\n")
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
	src := "def f() {\n    var i32: i32 = 0\n    _ = i32\n}\n"
	path := writeFile(t, t.TempDir(), "warn.will", src)
	if code, out, _ := runCLI("check", path); code != 0 || !strings.Contains(out, "N0003") {
		t.Fatalf("warning-only check: code = %d, stdout = %q", code, out)
	}
	if code, _, _ := runCLI("check", "-Werror", path); code != 1 {
		t.Fatalf("-Werror code = %d, want 1", code)
	}
}
