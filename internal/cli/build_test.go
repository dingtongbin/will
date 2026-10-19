package cli

import (
	"errors"
	"os/exec"
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
