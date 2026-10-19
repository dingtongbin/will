package utf8

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestUTF8Contract builds the decoder and its C test driver and runs the
// assertions.
func TestUTF8Contract(t *testing.T) {
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("gcc is not on PATH")
	}

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
	}
	exe := filepath.Join(t.TempDir(), "utf8_test")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}

	args := []string{"-std=c11", "-Wall", "-Wextra", "-Werror", "-O2"}
	if runtime.GOOS == "windows" {
		args = append(args, "-static")
	}
	args = append(args, "-o", exe,
		filepath.Join(dir, "src", "utf8.c"),
		filepath.Join(dir, "src", "utf8_test.c"))

	build := exec.Command(gcc, args...)
	build.Dir = dir
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	if output, err := exec.Command(exe).CombinedOutput(); err != nil {
		t.Fatalf("assertions: %v\n%s", err, output)
	}
}
