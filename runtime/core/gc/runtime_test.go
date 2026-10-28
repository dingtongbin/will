package gc

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestGCRuntime builds the heap, root, handle, and barrier modules with their C
// driver and runs it. It skips when gcc is not on PATH.
func TestGCRuntime(t *testing.T) {
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("gcc is not on PATH")
	}
	platform := gcPlatformSource(runtime.GOOS)
	if platform == "" {
		t.Skipf("no platform page source for %s", runtime.GOOS)
	}
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
	}
	exe := filepath.Join(t.TempDir(), "gc_runtime_test")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	args := []string{"-std=c11", "-Wall", "-Wextra", "-Werror", "-O2"}
	if runtime.GOOS == "windows" {
		args = append(args, "-static")
	}
	args = append(args, "-I", filepath.Join(dir, "..", "..", "include"),
		"-o", exe,
		filepath.Join(dir, "src", "heap.c"),
		filepath.Join(dir, "src", "roots.c"),
		filepath.Join(dir, "src", "barrier.c"),
		filepath.Join(dir, "src", "runtime_test.c"),
		filepath.Join(dir, "..", "..", "platform", platform))
	build := exec.Command(gcc, args...)
	build.Dir = dir
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	if output, err := exec.Command(exe).CombinedOutput(); err != nil {
		t.Fatalf("run: %v\n%s", err, output)
	}
}
