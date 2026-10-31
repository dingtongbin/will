package task

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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

// compileDriver builds one C test driver with the task engine and the runtime
// modules it needs. Each driver owns its own main, so the task contract and
// the job contract can live in separate files.
func buildDriver(t *testing.T, gcc, dir, root, platform, name string, sources ...string) string {
	t.Helper()

	exe := filepath.Join(t.TempDir(), name)
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	args := []string{"-std=c11", "-Wall", "-Wextra", "-Werror", "-O2"}
	if runtime.GOOS == "windows" {
		args = append(args, "-static")
	}
	args = append(args, "-I", filepath.Join(root, "core", "gc", "src"),
		"-o", exe)
	for _, source := range sources {
		args = append(args, filepath.Join(dir, "src", source))
	}
	args = append(args,
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
		t.Fatalf("build %s: %v\n%s", name, err, output)
	}
	return exe
}

func compileDriver(t *testing.T, gcc, dir, root, platform, name string, sources ...string) []byte {
	t.Helper()
	exe := buildDriver(t, gcc, dir, root, platform, name, sources...)
	output, err := exec.Command(exe).CombinedOutput()
	if err != nil {
		t.Fatalf("run %s: %v\n%s", name, err, output)
	}
	return output
}

// TestTaskContract builds the task and channel engines together with their C
// driver and runs it. It proves a joined result, region accounting, panic
// containment, channel order, and select order. The job driver runs after it
// and proves that lane-local job queues drain every queued job on multiple
// lanes.
// TestDeadlockContract builds a driver whose only task parks forever. The
// shutdown wait must report the four counts and exit instead of hanging.
func TestDeadlockContract(t *testing.T) {
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
	exe := buildDriver(t, gcc, dir, root, platform, "deadlock_test", "task.c", "deadlock_test.c")
	output, err := exec.Command(exe).CombinedOutput()
	if err == nil {
		t.Fatalf("deadlock driver exited zero:\n%s", output)
	}
	for _, want := range []string{
		"deadlock: no runnable task",
		"  tasks: 1",
		"  jobs: 0",
		"  timers: 0",
		"  io: 0",
	} {
		if !strings.Contains(string(output), want) {
			t.Errorf("output lacks %q:\n%s", want, output)
		}
	}
}

func TestTaskContract(t *testing.T) {
	os.Setenv("WILLRT_COUNT_ALLOC", "1")
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

	output := compileDriver(t, gcc, dir, root, platform, "task_test", "task.c", "task_test.c")
	for _, want := range []string{
		"panic: task failed on purpose",
		"  at task_test.c:20:5 will.test.task",
		"region: 1 tasks failed",
	} {
		if !strings.Contains(string(output), want) {
			t.Errorf("output lacks %q:\n%s", want, output)
		}
	}

	compileDriver(t, gcc, dir, root, platform, "job_test", "task.c", "job.c", "job_test.c")
}
