package mem

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// platformSource maps a Go target to the platform file that supplies
// willrt_os_alloc_pages for the contract test. The runtime ships one file per
// target, and the test builds only the host's file, matching how the runtime
// library is assembled.
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

// TestAllocatorContract builds the allocator together with its C test driver
// and runs the driver. The test skips when gcc is not on PATH so that machines
// without a C toolchain still run the rest of the suite.
func TestAllocatorContract(t *testing.T) {
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
		filepath.Join(dir, "src", "alloc_test.c"),
		filepath.Join(dir, "..", "..", "platform", platform))

	build := exec.Command(gcc, args...)
	build.Dir = dir
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}

	output, err := exec.Command(exe).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, output)
	}

	reports := []struct {
		text string
		want int
	}{
		{"willrt: plate release rejected: bad header magic", 2},
		{"willrt: plate release rejected: pointer is not in the arena", 1},
		{"willrt: free rejected: pointer was not returned by willrt_alloc", 1},
		{"willrt: free rejected: block is not live", 2},
	}
	for _, report := range reports {
		if got := strings.Count(string(output), report.text); got != report.want {
			t.Fatalf("%q reports = %d, want %d\n%s", report.text, got, report.want, output)
		}
	}
}
