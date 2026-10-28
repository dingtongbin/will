package gc

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// gcPlatformSource names the platform file that supplies the page and thread
// primitives the heap test links against.
func gcPlatformSource(goos string) string {
	switch goos {
	case "windows":
		return "win.c"
	case "linux":
		return "linux.c"
	case "darwin":
		return "mac.c"
	case "android":
		return "android.c"
	case "ios":
		return "ios.c"
	default:
		return ""
	}
}

// TestGCHeap builds the heap foundation with its C stress driver and runs the
// driver. It skips when gcc is not on PATH.
func TestGCHeap(t *testing.T) {
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
	exe := filepath.Join(t.TempDir(), "gc_heap_test")
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
		filepath.Join(dir, "src", "heap_test.c"),
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
