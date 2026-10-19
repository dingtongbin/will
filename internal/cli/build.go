package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/cache"
	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/gen"
	"github.com/will-lang/will/internal/parser"
	"github.com/will-lang/will/internal/pkg"
	"github.com/will-lang/will/internal/resolve"
	"github.com/will-lang/will/internal/toolchain"
	"github.com/will-lang/will/internal/types"
	"github.com/will-lang/will/internal/willc"
)

// defaultEntry is the entry function name. The language volume does not fix
// an entry point yet, so this is provisional.
const defaultEntry = "main"

func compileIR(opts buildOptions, stderr io.Writer) ([]byte, string, int) {
	file, modulePath, code := entryFile(opts.path, opts.entry, stderr)
	if code != 0 {
		return nil, "", code
	}
	data, err := os.ReadFile(file)
	if err != nil {
		fmt.Fprintf(stderr, "will: %v\n", err)
		return nil, "", 1
	}
	store, key := buildCacheKey(file, data, modulePath, opts)
	if store != nil {
		if got, ok := store.Get(key, cache.ModuleLL); ok {
			return got[cache.ModuleLL], modulePath, 0
		}
	}
	f, ds := parser.Parse(filepath.ToSlash(file), data)
	var bag diag.Bag
	bag.AddAll(ds)
	if f == nil {
		fmt.Fprint(stderr, diag.FormatAll(bag.Diagnostics()))
		return nil, "", 1
	}
	if !fileDeclaresEntry(f, opts.entry) {
		fmt.Fprintf(stderr, "will: entry function %s not found\n", opts.entry)
		return nil, "", 1
	}
	res, rds := resolve.Resolve(f)
	bag.AddAll(rds)
	bag.AddAll(types.Check(f, res))
	all := bag.Diagnostics()
	if hasError(all) {
		fmt.Fprint(stderr, diag.FormatAll(all))
		return nil, "", 1
	}
	text, err := willc.Emit(f, res, modulePath, all)
	if err != nil {
		fmt.Fprintf(stderr, "will: %v\n", err)
		return nil, "", 1
	}
	doc, err := willc.Read(text)
	if err != nil {
		fmt.Fprintf(stderr, "will: %v\n", err)
		return nil, "", 1
	}
	ir, err := gen.Emit(doc)
	if err != nil {
		fmt.Fprintf(stderr, "will: %v\n", err)
		return nil, "", 1
	}
	if store != nil {
		_ = store.Put(key, map[string][]byte{cache.Willc: text, cache.ModuleLL: ir})
		_ = store.Trim()
	}
	return ir, modulePath, 0
}

// buildCacheKey keys one build on everything this run reads: the source, the
// module path, the entry name, the compiler identity, and the flags.
func buildCacheKey(file string, data []byte, modulePath string, opts buildOptions) (*cache.Store, string) {
	root := os.Getenv("WILL_CACHE")
	if root == "" {
		return nil, ""
	}
	h := cache.NewHasher()
	h.Add(filepath.ToSlash(file), data)
	h.AddStrings("module", modulePath, opts.entry)
	build := buildIdentityWith(opts.opt, opts.target)
	h.AddStrings("compiler", build.Compiler, build.Target, build.Opt)
	h.AddStrings("flags", fmt.Sprintf("lib=%v", opts.lib), fmt.Sprintf("debug=%v", opts.debug),
		fmt.Sprintf("freestanding=%v", opts.freestanding), "features="+opts.features)
	return cache.New(root, 0), h.Key()
}

// entryFile finds the source file that declares the entry function and its
// module path.
func entryFile(path, entry string, stderr io.Writer) (string, string, int) {
	info, err := os.Stat(path)
	if err != nil {
		fmt.Fprintf(stderr, "will: %v\n", err)
		return "", "", 1
	}
	if !info.IsDir() {
		return path, filepath.Base(filepath.Dir(path)), 0
	}
	b := pkg.Load(path)
	if b.Err != nil {
		fmt.Fprintf(stderr, "will: %v\n", b.Err)
		return "", "", 1
	}
	for _, p := range b.Packages {
		for _, f := range p.Files {
			if f.AST == nil {
				continue
			}
			for _, d := range f.AST.Decls {
				if declaresEntry(d, entry) {
					return filepath.Join(p.Dir, f.Path), p.Path, 0
				}
			}
		}
	}
	fmt.Fprintf(stderr, "will: entry function %s not found\n", entry)
	return "", "", 1
}

func declaresEntry(d ast.Decl, entry string) bool {
	fn, ok := d.(*ast.FuncDecl)
	return ok && fn.Recv == nil && fn.Name == entry
}

func fileDeclaresEntry(f *ast.File, entry string) bool {
	for _, d := range f.Decls {
		if declaresEntry(d, entry) {
			return true
		}
	}
	return false
}

func runBuild(args []string, stdout, stderr io.Writer) int {
	opts, _, code := parseBuildArgs("build", args, stderr)
	if code != 0 {
		return code
	}
	ir, modulePath, code := compileIR(opts, stderr)
	if code != 0 {
		return code
	}
	clang, err := toolchain.FindClang()
	if err != nil {
		return missingTool(stdout, "clang")
	}
	out := opts.out
	if out == "" {
		out = defaultOutput(modulePath, opts.target, opts.lib)
	}
	output, err := clang.BuildIR(ir, out, clangArgs(opts)...)
	if err != nil {
		fmt.Fprintf(stdout, "%s", output)
		fmt.Fprintf(stderr, "will: link failed\n")
		return 1
	}
	return 0
}

// clangArgs turns the build flags into driver arguments.
func clangArgs(opts buildOptions) []string {
	var args []string
	if opts.opt != "" {
		args = append(args, opts.opt)
	}
	if opts.debug {
		args = append(args, "-g")
	}
	if opts.target != "" {
		args = append(args, "--target="+opts.target)
	}
	if opts.lib {
		args = append(args, "-shared")
	}
	return args
}

func runRun(args []string, stdout, stderr io.Writer) int {
	opts, programArgs, code := parseBuildArgs("run", args, stderr)
	if code != 0 {
		return code
	}
	ir, _, code := compileIR(opts, stderr)
	if code != 0 {
		return code
	}
	clang, err := toolchain.FindClang()
	if err != nil {
		return missingTool(stdout, "clang")
	}
	out := opts.out
	if out == "" {
		dir, err := os.MkdirTemp("", "will-run")
		if err != nil {
			fmt.Fprintf(stderr, "will: %v\n", err)
			return 1
		}
		defer os.RemoveAll(dir)
		out = filepath.Join(dir, "program")
		if isWindows() {
			out += ".exe"
		}
	}
	output, err := clang.BuildIR(ir, out, clangArgs(opts)...)
	if err != nil {
		fmt.Fprintf(stdout, "%s", output)
		fmt.Fprintf(stderr, "will: link failed\n")
		return 1
	}
	cmd := exec.Command(out, programArgs...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return exit.ExitCode()
		}
		fmt.Fprintf(stderr, "will: %v\n", err)
		return 1
	}
	return 0
}

// runEmitLLVM writes the LLVM text of one build. It needs no external tool.
func runEmitLLVM(args []string, stdout, stderr io.Writer) int {
	opts, _, code := parseBuildArgs("emit-llvm", args, stderr)
	if code != 0 {
		return code
	}
	ir, _, code := compileIR(opts, stderr)
	if code != 0 {
		return code
	}
	stdout.Write(ir)
	return 0
}

// missingTool reports X0003 for a missing external tool.
func missingTool(stdout io.Writer, name string) int {
	fmt.Fprintf(stdout, "X0003 external tool %s is missing or too old\n    help: install it, or set WILL_CLANG\n", name)
	return 1
}

// defaultOutput returns build/<target>/<module name><suffix>, the placement
// the build volume fixes for artifacts.
func defaultOutput(modulePath, target string, lib bool) string {
	if target == "" {
		target = targetTriple()
	}
	name := lastSegment(modulePath)
	if name == "" {
		name = "app"
	}
	if lib {
		switch {
		case strings.Contains(target, "win"):
			return filepath.Join("build", target, name+".dll")
		case strings.Contains(target, "mac"), strings.Contains(target, "ios"):
			return filepath.Join("build", target, "lib"+name+".dylib")
		default:
			return filepath.Join("build", target, "lib"+name+".so")
		}
	}
	suffix := ""
	if strings.Contains(target, "win") {
		suffix = ".exe"
	}
	return filepath.Join("build", target, name+suffix)
}

// lastSegment returns the last path segment, accepting both separators.
func lastSegment(path string) string {
	path = strings.TrimSuffix(path, "/")
	if i := strings.LastIndexAny(path, `/\`); i >= 0 {
		return path[i+1:]
	}
	return path
}

func isWindows() bool {
	return os.PathSeparator == '\\'
}
