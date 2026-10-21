package blocking

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// platformSource maps a Go target to the platform file that supplies threads,
// locks, and the allocator's page source.
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

// TestBlockingPoolContract builds the pool with its C driver and runs it.
func TestBlockingPoolContract(t *testing.T) {
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
	exe := filepath.Join(t.TempDir(), "blocking_test")
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
		filepath.Join(dir, "src", "blocking.c"),
		filepath.Join(dir, "src", "blocking_test.c"),
		filepath.Join(dir, "..", "mem", "src", "alloc.c"),
		filepath.Join(dir, "..", "rc", "src", "rc.c"),
		filepath.Join(root, "platform", platform))
	build := exec.Command(gcc, args...)
	build.Dir = dir
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	if output, err := exec.Command(exe).CombinedOutput(); err != nil {
		t.Fatalf("run: %v\n%s", err, output)
	}
}
