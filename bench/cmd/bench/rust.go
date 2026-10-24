package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// rustCompiler resolves the Rust compiler once per run: $RUSTC when it is set,
// then rustc on the path. The identity is the version line, which is what the
// results name as the compiler of a row.
var rustOnce sync.Once
var rustPath string
var rustName string

func rustCompiler() (string, string) {
	rustOnce.Do(func() {
		path := os.Getenv("RUSTC")
		if path == "" {
			found, err := exec.LookPath("rustc")
			if err != nil {
				return
			}
			path = found
		}
		out, err := exec.Command(path, "--version").Output()
		if err != nil {
			return
		}
		rustPath = path
		rustName = strings.TrimSpace(string(out))
	})
	return rustPath, rustName
}

// buildRust compiles one twin into exe. The twin is a single main.rs, so the
// build is one rustc call with the same optimization level as the other sides.
func buildRust(rustc, dir, exe string) error {
	cmd := exec.Command(rustc, "-O", "-o", exe, "main.rs")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("the rust build failed: %v\n%s", err, out)
	}
	return nil
}

// rustSide builds and runs the Rust twin of one scenario and returns its rows.
// A scenario without a twin, or a machine without rustc, contributes nothing.
func rustSide(root, machine, buildDir string, sc scenario, runs int) ([]resultRow, error) {
	rustc, identity := rustCompiler()
	if rustc == "" || sc.rustDir == "" {
		return nil, nil
	}
	dir := filepath.Join(root, filepath.FromSlash(sc.rustDir))
	exe := filepath.Join(buildDir, "rust_"+strings.ReplaceAll(sc.name, "/", "_"))
	if err := buildRust(rustc, dir, exe); err != nil {
		return nil, err
	}
	results, err := runMany(exe, dir, runs)
	if err != nil {
		return nil, err
	}
	metrics, _ := aggregate(results)
	var rows []resultRow
	for name, s := range metrics {
		rows = append(rows, metricRow(machine, identity, sc, name, s, runs, sc.note))
	}
	return rows, nil
}
