package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// findBenchRoot walks up from the working directory to the directory that
// holds the scenarios and the shared C support. The suite is self contained,
// so that directory is the root of everything the tool reads and writes.
func findBenchRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if nested := filepath.Join(dir, "bench"); isBenchRoot(nested) {
		return nested, nil
	}
	for {
		if isBenchRoot(dir) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("the benchmark root is not found above %s", dir)
		}
		dir = parent
	}
}

func isBenchRoot(dir string) bool {
	for _, name := range []string{"scenarios", "csupport"} {
		if info, err := os.Stat(filepath.Join(dir, name)); err != nil || !info.IsDir() {
			return false
		}
	}
	return true
}

// findRepoRoot walks up from the bench root to the repository root, which
// holds the runtime tree. Each benchmark module names the standard library
// through its own require and replace lines.
func findRepoRoot(benchRoot string) (string, error) {
	dir := benchRoot
	for {
		if info, err := os.Stat(filepath.Join(dir, "runtime", "include", "willrt.h")); err == nil && !info.IsDir() {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("the repository root is not found above %s", benchRoot)
		}
		dir = parent
	}
}

// targetTriple names the target the results belong to. The list is the one
// the build volume fixes.
func targetTriple() string {
	arch := runtime.GOARCH
	switch arch {
	case "amd64":
		arch = "x86_64"
	case "arm64":
		arch = "aarch64"
	}
	switch runtime.GOOS {
	case "windows":
		return arch + "-win-msvc"
	case "linux":
		return arch + "-linux-gnu"
	case "darwin":
		return arch + "-mac-darwin"
	default:
		return arch + "-" + runtime.GOOS
	}
}
