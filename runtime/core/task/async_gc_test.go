package task

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestAsyncGCRoots links the collector and proves that a task frame and a
// channel ring keep their references alive during a full cycle, then release
// them when the owner goes away.
func TestAsyncGCRoots(t *testing.T) {
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("gcc is not on PATH")
	}
	platform := platformSource(runtime.GOOS)
	if platform == "" {
		t.Skipf("no platform page source for %s", runtime.GOOS)
	}
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
	}
	root := filepath.Join(dir, "..", "..")
	exe := buildDriver(t, gcc, dir, root, platform, "async_gc_test", "task.c", "async_gc_test.c")
	if output, err := exec.Command(exe).CombinedOutput(); err != nil {
		t.Fatalf("run async gc roots: %v\n%s", err, output)
	}
}
