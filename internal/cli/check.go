// Package cli implements the will command line.
package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/will-lang/will/internal/cache"
	"github.com/will-lang/will/internal/check"
	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/parser"
	"github.com/will-lang/will/internal/pkg"
	"github.com/will-lang/will/internal/resolve"
	"github.com/will-lang/will/internal/types"
)

const usage = "usage: will check [--json] [--long] [-Werror] [-O0..-O3] [--target <triple>] <file>...\n"

// Run dispatches one command and returns the process exit code: 0 success,
// 1 diagnostics, 2 usage.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	if code := checkCacheEnv(stderr); code != 0 {
		return code
	}
	switch args[0] {
	case "check":
		return runCheck(args[1:], stdout, stderr)
	case "emit-willc":
		return runEmitWillc(args[1:], stdout, stderr)
	case "emit-llvm":
		return runEmitLLVM(args[1:], stdout, stderr)
	case "emit-decisions":
		return runEmitDecisions(args[1:], stdout, stderr)
	case "emit-asm":
		return runEmitAsm(args[1:], stdout, stderr)
	case "fmt":
		return runFmt(args[1:], stdout, stderr)
	case "index":
		return runIndex(args[1:], stdout, stderr)
	case "new":
		return runNew(args[1:], stdout, stderr)
	case "mod":
		return runMod(args[1:], stdout, stderr)
	case "build":
		return runBuild(args[1:], stdout, stderr)
	case "run":
		return runRun(args[1:], stdout, stderr)
	case "test":
		return runTest(args[1:], stdout, stderr)
	case "lsp":
		return runLSP(args[1:], stdout, stderr)
	case "version":
		return runVersion(stdout)
	}
	fmt.Fprintf(stderr, "will: unknown command %s\n", args[0])
	fmt.Fprint(stderr, usage)
	return 2
}

// checkCacheEnv reports a bad cache environment before any command runs. The
// volume makes a relative cache path a usage error, so it must not be silently
// replaced by the default.
func checkCacheEnv(stderr io.Writer) int {
	for _, check := range []func() (string, error){cache.Root, cache.ModRoot} {
		if _, err := check(); err != nil {
			fmt.Fprintf(stderr, "will: %v\n", err)
			return 2
		}
	}
	if _, err := cache.Limit(); err != nil {
		fmt.Fprintf(stderr, "will: %v\n", err)
		return 2
	}
	return 0
}

// checkFlags are the switches shared by will check, will build, and the
// directory cache.
type checkFlags struct {
	json     bool
	long     bool
	werror   bool
	opt      string
	target   string
	features string
	// targetSet records that --target was written, so only an explicit
	// target is validated.
	targetSet bool
	// freestanding turns on the --freestanding rule of the build volume.
	freestanding bool
	// disabled holds the warning families that -Wno-<family> switched off.
	disabled map[string]bool
}

func runCheck(args []string, stdout, stderr io.Writer) int {
	flags := checkFlags{opt: "-O0", target: targetTriple(), disabled: map[string]bool{}}
	var files []string
	for i := 0; i < len(args); i++ {
		name, inline, hasInline := splitFlag(args[i])
		switch name {
		case "--json":
			flags.json = true
		case "--long":
			flags.long = true
		case "-Werror":
			flags.werror = true
		case "-O0", "-O1", "-O2", "-O3":
			flags.opt = name
		case "--freestanding":
			flags.freestanding = true
		case "--target":
			value, next, ok := flagValue(args, i, inline, hasInline, "--target", stderr)
			if !ok {
				return 2
			}
			i, flags.target, flags.targetSet = next, value, true
		case "--features":
			value, next, ok := flagValue(args, i, inline, hasInline, "--features", stderr)
			if !ok {
				return 2
			}
			i, flags.features = next, value
		default:
			switch {
			case strings.HasPrefix(args[i], "-Wno-"):
				family := strings.TrimPrefix(args[i], "-Wno-")
				if !diag.KnownFamily(family) {
					fmt.Fprintf(stderr, "will: unknown warning family %s\n", family)
					return 2
				}
				flags.disabled[family] = true
			case strings.HasPrefix(args[i], "-W"):
				family := strings.TrimPrefix(args[i], "-W")
				if !diag.KnownFamily(family) {
					fmt.Fprintf(stderr, "will: unknown warning family %s\n", family)
					return 2
				}
				delete(flags.disabled, family)
			case strings.HasPrefix(args[i], "-"):
				fmt.Fprintf(stderr, "will: unknown flag %s\n", args[i])
				return 2
			default:
				files = append(files, args[i])
			}
		}
	}
	if len(files) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	pkg.SetEnabledFeatures(flags.features)
	if d, bad := targetDiagnostic(flags.target, flags.targetSet); bad {
		reportNoPos(d, flags.json, buildIdentityWith(flags.opt, flags.target), stdout)
		return 1
	}

	// A single directory argument is cached with the key that lists the whole
	// tree; anything else falls back to the file key.
	store, key, contents := checkCache(files, flags)
	if store == nil && len(files) == 1 {
		if info, err := os.Stat(files[0]); err == nil && info.IsDir() {
			b := pkg.Load(files[0])
			if b.Err != nil {
				return loadFailure(b.Err, stderr)
			}
			return runCachedBuild(b, flags, stdout)
		}
	}
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
			ok, code := checkPath(&bag, path, flags.freestanding, stderr)
			if !ok {
				return code
			}
		}
	} else {
		for _, f := range contents {
			checkFile(&bag, f.path, f.data, flags.freestanding)
		}
	}

	ds := diag.FilterDisabled(bag.Diagnostics(), flags.disabled)
	build := buildIdentityWith(flags.opt, flags.target)
	var out []byte
	switch {
	case flags.json:
		out = []byte(diag.JSONAll(ds, build))
	case flags.long:
		out = []byte(diag.FormatAllLong(ds, sourceReader()))
	default:
		out = []byte(diag.FormatAll(ds))
	}
	stdout.Write(out)

	code := exitCode(ds, flags.werror)
	if store != nil {
		_ = store.Put(key, map[string][]byte{cache.Diag: encodeDiag(code, out)})
		_ = store.TrimIfNeeded()
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
func checkCache(files []string, flags checkFlags) (*cache.Store, string, []fileData) {
	root, err := cache.Root()
	if err != nil {
		return nil, "", nil
	}
	limit, _ := cache.Limit()
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
		contents = append(contents, fileData{path: filepath.ToSlash(path), data: data})
		h.Add(filepath.ToSlash(path), data)
	}
	build := buildIdentityWith(flags.opt, flags.target)
	h.AddStrings("compiler", compilerIdentity(), build.Target, build.Opt)
	h.AddStrings("flags", fmt.Sprintf("json=%v", flags.json), fmt.Sprintf("werror=%v", flags.werror),
		"features="+flags.features, fmt.Sprintf("freestanding=%v", flags.freestanding),
		"disabled="+disabledList(flags.disabled))
	return cache.New(root, limit), h.Key(), contents
}

// disabledList renders the switched-off warning families in a fixed order, so
// the cache key does not depend on the order the flags were written.
func disabledList(disabled map[string]bool) string {
	var out []string
	for _, family := range diag.WarningFamilies {
		if disabled[family] {
			out = append(out, family)
		}
	}
	return strings.Join(out, ",")
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
// package resolver. It returns false and the exit code when the check cannot
// start.
func checkPath(bag *diag.Bag, path string, freestanding bool, stderr io.Writer) (bool, int) {
	info, err := os.Stat(path)
	if err != nil {
		fmt.Fprintf(stderr, "will: %v\n", err)
		return false, 1
	}
	if info.IsDir() {
		b := pkg.Load(path)
		if b.Err != nil {
			return false, loadFailure(b.Err, stderr)
		}
		bag.AddAll(b.CheckAll())
		if freestanding {
			bag.AddAll(b.Freestanding())
		}
		return true, 0
	}
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(stderr, "will: %v\n", err)
		return false, 1
	}
	checkFile(bag, filepath.ToSlash(path), data, freestanding)
	return true, 0
}

// checkFile runs the same front end as the build over one file, so the two
// commands never disagree. Inside a module the file sees its package's sibling
// declarations and the module's import table.
func checkFile(bag *diag.Bag, file string, data []byte, freestanding bool) {
	f, ds := parser.Parse(file, data)
	bag.AddAll(ds)
	if f == nil {
		return
	}
	imps, _, names, others, _ := packageContext(file)
	var own diag.Bag
	if freestanding {
		own.AddAll(check.Freestanding(f))
	}
	res, rds := resolve.ResolveIn(f, names, imps)
	own.AddAll(rds)
	_, tds := types.CheckWithFull(f, res, others)
	own.AddAll(tds)
	if !own.HasErrors() {
		own.AddAll(check.Moves(f))
		own.AddAll(check.Borrows(f))
		own.AddAll(check.Cycles(f))
	}
	bag.AddAll(own.Diagnostics())
}

// buildIdentity reports the build identity attached to machine-readable
// diagnostics for the host target at -O0.
func buildIdentity() diag.Build {
	return buildIdentityWith("-O0", targetTriple())
}

// buildIdentityWith reports the build identity for one optimization level and
// target. It is deterministic for one binary, level, and target.
func buildIdentityWith(opt, target string) diag.Build {
	if opt == "" {
		opt = "-O0"
	}
	if target == "" {
		target = targetTriple()
	}
	return diag.Build{Compiler: "will " + CompilerVersion, Target: target, Opt: opt}
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
		return "x86_64-win-gnu"
	case "android/arm64":
		return "aarch64-android"
	case "ios/arm64":
		return "aarch64-ios"
	}
	return runtime.GOARCH + "-" + runtime.GOOS
}
