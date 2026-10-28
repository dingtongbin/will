package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// buildC compiles the C baseline of a scenario. The shared support is always
// linked in, so every baseline reads the same clock and prints the same
// metric lines.
func buildC(root string, sc scenario, cc string, out string) (string, error) {
	source := filepath.Join(root, filepath.FromSlash(sc.cFile))
	if _, err := os.Stat(source); err != nil {
		return "", fmt.Errorf("%s: %v", sc.name, err)
	}
	support := filepath.Join(root, "csupport", "bench.c")
	sources := append([]string{sc.cFile}, sc.cExtra...)
	args := []string{"-O2", "-I", filepath.Join(root, "csupport")}
	for _, extra := range sc.cExtra {
		args = append(args, "-I", filepath.Dir(filepath.Join(root, filepath.FromSlash(extra))))
	}
	args = append(args, "-o", out)
	for _, name := range sources {
		args = append(args, filepath.Join(root, filepath.FromSlash(name)))
	}
	args = append(args, support)
	args = append(args, sc.cflags...)
	cmd := exec.Command(cc, args...)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: the C build failed: %v\n%s", sc.name, err, output)
	}
	return out, nil
}

// buildWill compiles the will module of a scenario. The build runs from the
// repository root, because the compiler finds the runtime tree by walking up
// from the working directory.
func buildWill(root, repo string, will string, sc scenario, out string) (string, error) {
	module := filepath.Join(root, filepath.FromSlash(sc.willDir))
	if _, err := os.Stat(filepath.Join(module, "will.mod")); err != nil {
		return "", fmt.Errorf("%s: the will module has no will.mod: %v", sc.name, err)
	}
	cmd := exec.Command(will, "build", "-O2", module, "-o", out)
	cmd.Dir = repo
	cmd.Env = append(os.Environ(),
		"WILL_RUNTIME="+filepath.Join(repo, "runtime"))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: the will build failed: %v\n%s", sc.name, err, output)
	}
	return out, nil
}

// compilerVersion returns the first line of the compiler version output.
func compilerVersion(cc string) string {
	cmd := exec.Command(cc, "--version")
	output, err := cmd.Output()
	if err != nil {
		return "unknown"
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 0 {
		return "unknown"
	}
	return strings.TrimSpace(lines[0])
}

// findCompiler returns the C compiler to use. The order is the flag, the
// environment, then the usual names.
func findCompiler(opts options) (string, error) {
	candidates := []string{}
	if opts.cc != "" {
		candidates = append(candidates, opts.cc)
	}
	if env := os.Getenv("CC"); env != "" {
		candidates = append(candidates, env)
	}
	candidates = append(candidates, "clang", "cc", "gcc")
	for _, name := range candidates {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("no C compiler is found: set -cc or CC")
}

// findWill returns the will compiler to use.
func findWill(opts options) (string, error) {
	candidates := []string{}
	if opts.will != "" {
		candidates = append(candidates, opts.will)
	}
	if env := os.Getenv("WILL_BIN"); env != "" {
		candidates = append(candidates, env)
	}
	candidates = append(candidates, "will")
	for _, name := range candidates {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("no will compiler is found: set -will or WILL_BIN")
}
