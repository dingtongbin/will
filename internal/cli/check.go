// Package cli implements the will command line.
package cli

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"github.com/will-lang/will/internal/cache"
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

	// Only file arguments are cached; a directory run depends on the whole
	// tree and its key would have to list every input.
	store, key, contents := checkCache(files, jsonOut, werror)
	if store != nil {
		if data, ok := store.Get(key, cache.Diag); ok {
			out, code := decodeDiag(data[cache.Diag])
			stdout.Write(out)
			return code
		}
	}

	var bag diag.Bag
	if contents == nil {
		for _, path := range files {
			if !checkPath(&bag, path, stderr) {
				return 1
			}
		}
	} else {
		for _, f := range contents {
			checkFile(&bag, f.path, f.data)
		}
	}

	ds := bag.Diagnostics()
	build := buildIdentity()
	var out []byte
	if jsonOut {
		out = []byte(diag.JSONAll(ds, build))
	} else {
		out = []byte(diag.FormatAll(ds))
	}
	stdout.Write(out)

	code := 0
	for _, d := range ds {
		if d.Severity == diag.Error {
			code = 1
		}
		if werror && d.Severity == diag.Warning {
			code = 1
		}
	}
	if store != nil {
		_ = store.Put(key, map[string][]byte{cache.Diag: encodeDiag(code, out)})
		_ = store.Trim()
	}
	return code
}

// fileData is one file argument read for the cache key.
type fileData struct {
	path string
	data []byte
}

// checkCache returns a store and a key when WILL_CACHE is set and every
// argument is a file. contents carries the files it already read.
func checkCache(files []string, jsonOut, werror bool) (*cache.Store, string, []fileData) {
	root := os.Getenv("WILL_CACHE")
	if root == "" {
		return nil, "", nil
	}
	var contents []fileData
	h := cache.NewHasher()
	for _, path := range files {
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			return nil, "", nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, "", nil
		}
		contents = append(contents, fileData{path: path, data: data})
		h.Add(path, data)
	}
	build := buildIdentity()
	h.AddStrings("compiler", build.Compiler, build.Target, build.Opt)
	h.AddStrings("flags", fmt.Sprintf("json=%v", jsonOut), fmt.Sprintf("werror=%v", werror))
	return cache.New(root, 0), h.Key(), contents
}

func encodeDiag(code int, out []byte) []byte {
	prefix := fmt.Sprintf("%d\n", code)
	return append([]byte(prefix), out...)
}

func decodeDiag(data []byte) ([]byte, int) {
	i := strings.IndexByte(string(data), '\n')
	if i < 0 {
		return data, 1
	}
	code := 0
	fmt.Sscanf(string(data[:i]), "%d", &code)
	return data[i+1:], code
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
