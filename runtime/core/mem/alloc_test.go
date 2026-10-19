package mem

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestAllocatorContract builds the allocator together with its C test driver
// and runs the driver. The test skips when gcc is not on PATH so that machines
// without a C toolchain still run the rest of the suite.
func TestAllocatorContract(t *testing.T) {
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("gcc is not on PATH")
	}

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
	}
	exe := filepath.Join(t.TempDir(), "alloc_test")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}

	args := []string{"-std=c11", "-Wall", "-Wextra", "-Werror", "-O2"}
	if runtime.GOOS == "windows" {
		// The UCRT64 toolchain lives outside PATH in the default setup, so a
		// static link keeps the test executable runnable from any directory.
		args = append(args, "-static")
	}
	args = append(args, "-o", exe,
		filepath.Join(dir, "src", "alloc.c"),
		filepath.Join(dir, "src", "alloc_test.c"))

	build := exec.Command(gcc, args...)
	build.Dir = dir
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}

	output, err := exec.Command(exe).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, output)
	}

	const report = "willrt: plate release rejected: bad header magic"
	if got := strings.Count(string(output), report); got != 2 {
		t.Fatalf("misuse reports = %d, want 2\n%s", got, output)
	}
}
