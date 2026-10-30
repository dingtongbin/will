package sync

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// platformSource maps a Go target to the platform file that supplies pages,
// clocks, threads, and locks. The test builds only the host's file.
func platformSource(goos string) string {
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

// TestSyncContract builds the sync primitives with the task engine and runs
// the C driver. It proves exact serialization under contention, overlapping
// shared holders with a waiting writer, cancel-safe and deadline-safe waits
// that leave the token intact, permit accounting that never leaks, count
// re-arming, gate generations, and the blocking fallback's scoped deadline.
func TestSyncContract(t *testing.T) {
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
	exe := filepath.Join(t.TempDir(), "sync_test")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	args := []string{"-std=c11", "-Wall", "-Wextra", "-Werror", "-O2"}
	if runtime.GOOS == "windows" {
		args = append(args, "-static")
	}
	args = append(args, "-o", exe,
		filepath.Join(dir, "src", "sync.c"),
		filepath.Join(dir, "src", "sync_test.c"),
		filepath.Join(dir, "..", "task", "src", "task.c"),
		filepath.Join(dir, "..", "chan", "src", "chan.c"),
		filepath.Join(dir, "..", "mem", "src", "alloc.c"),
		filepath.Join(dir, "..", "report", "src", "report.c"),
		filepath.Join(dir, "..", "startup", "src", "startup.c"),
		filepath.Join(dir, "..", "timer", "src", "timer.c"),
		filepath.Join(dir, "..", "blocking", "src", "blocking.c"),
		filepath.Join(dir, "..", "poll", "src", "poll.c"),
		filepath.Join(root, "platform", platform))
	for _, source := range []string{
		"types.c", "region.c", "chunks.c", "heap.c", "object.c", "roots.c",
		"safepoint.c", "barrier.c", "mark.c", "evac.c", "cycle.c", "pacer.c",
	} {
		args = append(args, filepath.Join(root, "core", "gc", "src", source))
	}
	if runtime.GOOS == "windows" {
		args = append(args, "-lws2_32")
	}
	build := exec.Command(gcc, args...)
	build.Dir = dir
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	output, err := exec.Command(exe).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, output)
	}
}
