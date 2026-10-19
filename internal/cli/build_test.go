package cli

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const entrySource = `import c "stdio.h" {
    fast putchar
}

func main() -> i32 {
    return putchar(65)
}
`

func hasClang() bool {
	_, err := exec.LookPath("clang")
	return err == nil
}

func TestBuildEmitIR(t *testing.T) {
	path := writeFile(t, t.TempDir(), "main.will", entrySource)
	code, out, errOut := runCLI("build", "--emit-ir", path)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, errOut)
	}
	for _, want := range []string{"declare i32 @putchar(i32)", "call i32 @putchar(i32 65)", "ret i32 %t1"} {
		if !strings.Contains(out, want) {
			t.Errorf("IR lacks %q:\n%s", want, out)
		}
	}
}

func TestBuildUsesCache(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "main.will", entrySource)
	cacheDir := filepath.Join(dir, "cache")
	t.Setenv("WILL_CACHE", cacheDir)

	code1, out1, _ := runCLI("build", "--emit-ir", path)
	if code1 != 0 || out1 == "" {
		t.Fatalf("first build: code = %d, stdout = %q", code1, out1)
	}
	entries, err := os.ReadDir(cacheDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("cache entries = %v, err = %v", entries, err)
	}
	code2, out2, _ := runCLI("build", "--emit-ir", path)
	if code2 != code1 || out2 != out1 {
		t.Fatalf("cached build differed: code = %d, stdout = %q", code2, out2)
	}
	// A changed source must miss.
	writeFile(t, dir, "main.will", strings.Replace(entrySource, "65", "66", 1))
	if _, out3, _ := runCLI("build", "--emit-ir", path); out3 == out1 {
		t.Fatalf("changed source hit the old entry: %q", out3)
	}
}

func TestBuildMissingClang(t *testing.T) {
	if hasClang() {
		t.Skip("clang is installed; the missing-tool path is not reachable")
	}
	path := writeFile(t, t.TempDir(), "main.will", entrySource)
	code, out, _ := runCLI("build", path)
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(out, "X0003 external tool clang is missing or too old") {
		t.Fatalf("stdout = %q", out)
	}
}

func TestBuildEntryNotFound(t *testing.T) {
	path := writeFile(t, t.TempDir(), "main.will", "func other() {\n}\n")
	code, _, errOut := runCLI("build", "--entry", "main", path)
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(errOut, "entry function main not found") {
		t.Fatalf("stderr = %q", errOut)
	}
}

func TestBuildUsage(t *testing.T) {
	if code, _, _ := runCLI("build"); code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
}

func TestRunMissingClang(t *testing.T) {
	if hasClang() {
		t.Skip("clang is installed")
	}
	path := writeFile(t, t.TempDir(), "main.will", entrySource)
	code, out, _ := runCLI("run", path)
	if code != 1 || !strings.Contains(out, "X0003") {
		t.Fatalf("code = %d, stdout = %q", code, out)
	}
}

var _ = errors.Is
