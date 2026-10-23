package toolchain

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// answerIR is a complete program: an executable needs an entry point, and the
// helper proves that a function the entry calls is linked in.
const answerIR = `; answer() returns 42
define i32 @answer() {
entry:
  ret i32 42
}

define i32 @main() {
entry:
  %r = call i32 @answer()
  ret i32 %r
}
`

func TestFindClangMissing(t *testing.T) {
	t.Setenv("WILL_CLANG", "")
	t.Setenv("PATH", t.TempDir())
	if _, err := FindClang(); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestFindClangOverride(t *testing.T) {
	t.Setenv("WILL_CLANG", `C:\tools\clang.exe`)
	c, err := FindClang()
	if err != nil || c.Path != `C:\tools\clang.exe` {
		t.Fatalf("clang = %+v, err = %v", c, err)
	}
}

// TestBuildIRAndRun measures the text-IR path end to end. It skips when clang
// is not installed.
func TestBuildIRAndRun(t *testing.T) {
	c, err := FindClang()
	if err != nil {
		t.Skip("clang is not installed")
	}
	out := filepath.Join(t.TempDir(), "answer.exe")
	if runtime.GOOS != "windows" {
		out = filepath.Join(t.TempDir(), "answer")
	}
	output, err := c.BuildIR([]byte(answerIR), out)
	if err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	cmd := exec.Command(out)
	err = cmd.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("run err = %v, want exit status 42", err)
	}
	if code := exitErr.ExitCode(); code != 42 {
		t.Fatalf("exit code = %d, want 42", code)
	}
}

func TestBuildIRBadTextReportsOutput(t *testing.T) {
	c, err := FindClang()
	if err != nil {
		t.Skip("clang is not installed")
	}
	out := filepath.Join(t.TempDir(), "bad.o")
	output, err := c.BuildIR([]byte("this is not LLVM\n"), out)
	if err == nil {
		t.Fatal("bad IR was accepted")
	}
	if output == "" {
		t.Fatal("no tool output was returned for a failed build")
	}
	if _, statErr := os.Stat(out); statErr == nil {
		t.Fatal("a failed build produced an output file")
	}
}
