// Package cli implements the will command line.
package cli

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/parser"
	"github.com/will-lang/will/internal/pkg"
	"github.com/will-lang/will/internal/resolve"
	"github.com/will-lang/will/internal/types"
)

const usage = "usage: will check [--json] [-Werror] <file>...\n"

// Run dispatches one command and returns the process exit code: 0 success,
// 1 diagnostics, 2 usage.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "check":
		return runCheck(args[1:], stdout, stderr)
	case "emit-willc":
		return runEmitWillc(args[1:], stdout, stderr)
	case "fmt":
		return runFmt(args[1:], stdout, stderr)
	case "version":
		fmt.Fprintln(stdout, buildIdentity().Compiler)
		return 0
	}
	fmt.Fprintf(stderr, "will: unknown command %s\n", args[0])
	fmt.Fprint(stderr, usage)
	return 2
}

func runCheck(args []string, stdout, stderr io.Writer) int {
	jsonOut := false
	werror := false
	var files []string
	for _, a := range args {
		switch {
		case a == "--json":
			jsonOut = true
		case a == "-Werror":
			werror = true
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(stderr, "will: unknown flag %s\n", a)
			return 2
		default:
			files = append(files, a)
		}
	}
	if len(files) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}

	var bag diag.Bag
	for _, path := range files {
		if !checkPath(&bag, path, stderr) {
			return 1
		}
	}

	ds := bag.Diagnostics()
	build := buildIdentity()
	if jsonOut {
		io.WriteString(stdout, diag.JSONAll(ds, build))
	} else {
		io.WriteString(stdout, diag.FormatAll(ds))
	}
	for _, d := range ds {
		if d.Severity == diag.Error {
			return 1
		}
		if werror && d.Severity == diag.Warning {
			return 1
		}
	}
	return 0
}

// checkPath checks one file or, for a directory, the whole module. Directory
// checking stops at the package layer because cross-package names need the
// package resolver.
func checkPath(bag *diag.Bag, path string, stderr io.Writer) bool {
	info, err := os.Stat(path)
	if err != nil {
		fmt.Fprintf(stderr, "will: %v\n", err)
		return false
	}
	if info.IsDir() {
		b := pkg.Load(path)
		if b.Err != nil {
			fmt.Fprintf(stderr, "will: %v\n", b.Err)
			return false
		}
		bag.AddAll(b.CheckAll())
		return true
	}
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(stderr, "will: %v\n", err)
		return false
	}
	checkFile(bag, path, data)
	return true
}

// checkFile runs the front end over one file and appends its diagnostics.
func checkFile(bag *diag.Bag, file string, data []byte) {
	f, ds := parser.Parse(file, data)
	bag.AddAll(ds)
	if f == nil {
		return
	}
	res, rds := resolve.Resolve(f)
	bag.AddAll(rds)
	bag.AddAll(types.Check(f, res))
}

// buildIdentity reports the build identity attached to machine-readable
// diagnostics. It is deterministic for one binary and target.
func buildIdentity() diag.Build {
	return diag.Build{Compiler: "will dev", Target: targetTriple(), Opt: "-O0"}
}

func targetTriple() string {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "linux/amd64":
		return "x86_64-linux-gnu"
	case "linux/arm64":
		return "aarch64-linux-gnu"
	case "darwin/amd64":
		return "x86_64-mac-darwin"
	case "darwin/arm64":
		return "aarch64-mac-darwin"
	case "windows/amd64":
		return "x86_64-win-msvc"
	case "android/arm64":
		return "aarch64-android"
	case "ios/arm64":
		return "aarch64-ios"
	}
	return runtime.GOARCH + "-" + runtime.GOOS
}
