package startup

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestLifecycleContract builds the hooks with their driver and runs it. The
// test skips when gcc is not on PATH so a machine without a C toolchain still
// runs the rest of the suite.
func TestLifecycleContract(t *testing.T) {
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("gcc is not on PATH")
	}
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
	}
	exe := filepath.Join(t.TempDir(), "startup_test")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	args := []string{"-std=c11", "-Wall", "-Wextra", "-Werror", "-O2"}
	if runtime.GOOS == "windows" {
		args = append(args, "-static")
	}
	args = append(args, "-o", exe,
		filepath.Join(dir, "src", "startup.c"),
		filepath.Join(dir, "src", "startup_test.c"))
	build := exec.Command(gcc, args...)
	build.Dir = dir
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	if output, err := exec.Command(exe).CombinedOutput(); err != nil {
		t.Fatalf("run: %v\n%s", err, output)
	}
}
