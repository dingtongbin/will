package task

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestSingleLaneJobContract forces one lane and submits jobs from a task. The
// parent must queue them on its own lane and park at the region barrier; an
// inline job would replace the parent task and detach the rest of the jobs.
func TestSingleLaneJobContract(t *testing.T) {
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
	compileDriver(t, gcc, dir, root, platform, "job_lane_test", "task.c",
		"job.c", "job_lane_test.c")
}
