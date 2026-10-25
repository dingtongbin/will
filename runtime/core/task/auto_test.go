package task

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestAutoContract runs the auto placement, wake, and result driver. It proves
// that a task is queued before run_auto returns, that a group reaches more than
// one lane, that an enqueue while a lane is running is not lost, and that a
// result larger than the inline cell survives the heap path.
func TestAutoContract(t *testing.T) {
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
	compileDriver(t, gcc, dir, root, platform, "auto_test", "task.c", "auto_test.c")
}
