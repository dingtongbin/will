package gc

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// gcSources lists every collector translation unit. Tests link the real
// collector, not a stub: allocation, barriers, marking, evacuation, cycle
// coordination, pacing, roots, and the platform layer.
func gcSources(dir string) []string {
	names := []string{
		"types.c", "region.c", "heap.c", "object.c", "roots.c",
		"safepoint.c", "barrier.c", "mark.c", "evac.c", "cycle.c",
		"pacer.c",
	}
	sources := make([]string, 0, len(names)+2)
	for _, name := range names {
		sources = append(sources, filepath.Join(dir, "src", name))
	}
	sources = append(sources,
		filepath.Join(dir, "..", "..", "core", "mem", "src", "alloc.c"))
	return sources
}

// gcPlatformSource names the platform file that supplies pages, threads, and
// the multi-view mapping.
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

func gcBuild(t *testing.T, driver string, extra ...string) string {
	t.Helper()
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("gcc is not on PATH")
	}
	platform := gcPlatformSource(runtime.GOOS)
	if platform == "" {
		t.Skipf("no platform source for %s", runtime.GOOS)
	}
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
	}
	exe := filepath.Join(t.TempDir(), driver)
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	args := []string{"-std=c11", "-Wall", "-Wextra", "-Werror", "-O2"}
	if runtime.GOOS == "windows" {
		args = append(args, "-static")
	}
	args = append(args, "-I", filepath.Join(dir, "..", "..", "include"),
		"-I", filepath.Join(dir, "src"), "-o", exe)
	args = append(args, extra...)
	args = append(args, filepath.Join(dir, "src", driver))
	args = append(args, gcSources(dir)...)
	args = append(args, filepath.Join(dir, "..", "..", "platform", platform))
	build := exec.Command(gcc, args...)
	build.Dir = dir
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	return exe
}

// TestGCCollector runs the real collector integration driver.
func TestGCCollector(t *testing.T) {
	exe := gcBuild(t, "collector_test.c")
	if output, err := exec.Command(exe).CombinedOutput(); err != nil {
		t.Fatalf("run: %v\n%s", err, output)
	}
}
