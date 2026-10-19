package report

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// platformSource maps a Go target to the platform file that supplies page
// allocation for smoke-testing the report build. The runtime ships one file
// per target, and the test builds only the host's file.
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

// TestReportContract builds the report layer and its C test driver, runs the
// unit assertions, and then runs the panic modes as child processes so the
// exit code and stderr bytes are observed without terminating the test.
func TestReportContract(t *testing.T) {
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
	exe := filepath.Join(t.TempDir(), "report_test")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}

	args := []string{"-std=c11", "-Wall", "-Wextra", "-Werror", "-O2",
		"-DWILLRT_REPORT_TEST"}
	if runtime.GOOS == "windows" {
		// The UCRT64 toolchain lives outside PATH in the default setup, so a
		// static link keeps the test executable runnable from any directory.
		args = append(args, "-static")
	}
	args = append(args, "-o", exe,
		filepath.Join(dir, "src", "report.c"),
		filepath.Join(dir, "src", "report_test.c"),
		filepath.Join(dir, "..", "mem", "src", "alloc.c"),
		filepath.Join(dir, "..", "..", "platform", platform))

	build := exec.Command(gcc, args...)
	build.Dir = dir
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}

	if output, err := exec.Command(exe).CombinedOutput(); err != nil {
		t.Fatalf("unit assertions: %v\n%s", err, output)
	}

	panicCases := []struct {
		name string
		arg  string
		want string
	}{
		{"site", "panic-site", "panic: msg\n  at a.will:1:2 pkg.f\n"},
		{"no-site", "panic-nosite", "panic: msg\n"},
		{"empty-message", "panic-empty", "panic: \n"},
	}
	for _, test := range panicCases {
		t.Run("panic/"+test.name, func(t *testing.T) {
			cmd := exec.Command(exe, test.arg)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr

			err := cmd.Run()
			var exit *exec.ExitError
			if !errors.As(err, &exit) {
				t.Fatalf("panic child did not fail: %v", err)
			}
			if got := exit.ExitCode(); got != 101 {
				t.Fatalf("exit code = %d, want 101", got)
			}
			if got := stderr.String(); got != test.want {
				t.Fatalf("stderr = %q, want %q", got, test.want)
			}
		})
	}
}
