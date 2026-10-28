package poll

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// platformSource maps a Go target to the platform file that supplies threads,
// locks, and the monotonic clock. The test builds only the host's file.
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

// TestPollerContract builds the poller with its C driver and runs it. Windows
// links ws2_32 because the poller and the test use WinSock sockets.
func TestPollerContract(t *testing.T) {
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("gcc is not on PATH")
	}
	platform := platformSource(runtime.GOOS)
	if platform == "" {
		t.Skipf("no platform source for %s", runtime.GOOS)
	}
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
	}
	exe := filepath.Join(t.TempDir(), "poll_test")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	args := []string{"-std=c11", "-Wall", "-Wextra", "-Werror", "-O2"}
	if runtime.GOOS == "windows" {
		args = append(args, "-static")
	} else {
		args = append(args, "-pthread")
	}
	root := filepath.Join(dir, "..", "..")
	args = append(args, "-o", exe,
		filepath.Join(dir, "src", "poll.c"),
		filepath.Join(dir, "src", "poll_test.c"),
		filepath.Join(dir, "..", "task", "src", "task.c"),
		filepath.Join(dir, "..", "timer", "src", "timer.c"),
		filepath.Join(dir, "..", "mem", "src", "alloc.c"),
		filepath.Join(dir, "..", "rc", "src", "rc.c"),
		filepath.Join(dir, "..", "report", "src", "report.c"),
		filepath.Join(root, "platform", platform))
	if runtime.GOOS == "windows" {
		args = append(args, "-lws2_32")
	}
	build := exec.Command(gcc, args...)
	build.Dir = dir
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	if output, err := exec.Command(exe).CombinedOutput(); err != nil {
		t.Fatalf("run: %v\n%s", err, output)
	}
}
