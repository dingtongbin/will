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
	"github.com/will-lang/will/internal/check"
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

// frontEnd is what the lowering step needs from the front end: the file that
// declares the entry function, the import path of its package, and the
// exported names of every package in the module.
type frontEnd struct {
	file       string
	modulePath string
	imports    resolve.Imports
}

// runFrontEnd runs the same front end will check runs and prints the same
// diagnostics, so the two commands never disagree. It returns the entry file.
func runFrontEnd(opts buildOptions, stdout, stderr io.Writer) (frontEnd, int) {
	info, err := os.Stat(opts.path)
	if err != nil {
		fmt.Fprintf(stderr, "will: %v\n", err)
		return frontEnd{}, 1
	}
	if !info.IsDir() {
		return fileFrontEnd(opts, stdout, stderr)
	}
	b := pkg.Load(opts.path)
	if b.Err != nil {
		fmt.Fprintf(stderr, "will: %v\n", b.Err)
		return frontEnd{}, 1
	}
	ds := b.CheckAll()
	if opts.freestanding {
		ds = append(ds, b.Freestanding()...)
	}
	ds = diag.FilterDisabled(ds, opts.disabled)
	reportDiagnostics(ds, opts, stdout)
	if failed(ds, opts) {
		return frontEnd{}, 1
	}
	imports := resolve.Imports{}
	for _, p := range b.Packages {
		imports[p.Path] = p.Exports
	}
	for _, p := range b.Packages {
		for _, f := range p.Files {
			if f.AST == nil {
				continue
			}
			for _, d := range f.AST.Decls {
				if declaresEntry(d, opts.entry) {
					// File.Path is relative to the module root, not to the
					// package directory.
					return frontEnd{file: filepath.Join(b.Root, f.Path), modulePath: p.Path, imports: imports}, 0
				}
			}
		}
	}
	reportNoPos(entryDiagnostic(opts.entry), opts.json, buildIdentityWith(opts.opt, opts.target), stdout)
	return frontEnd{}, 1
}

// fileFrontEnd runs the front end over one file.
func fileFrontEnd(opts buildOptions, stdout, stderr io.Writer) (frontEnd, int) {
	data, err := os.ReadFile(opts.path)
	if err != nil {
		fmt.Fprintf(stderr, "will: %v\n", err)
		return frontEnd{}, 1
	}
	f, ds := parser.Parse(filepath.ToSlash(opts.path), data)
	var bag diag.Bag
	bag.AddAll(ds)
	if f == nil {
		reportDiagnostics(diag.FilterDisabled(bag.Diagnostics(), opts.disabled), opts, stdout)
		return frontEnd{}, 1
	}
	imports, modulePath, names, others := packageContext(opts.path)
	res, rds := resolve.ResolveIn(f, names, imports)
	bag.AddAll(rds)
	bag.AddAll(types.CheckWith(f, res, others))
	if !bag.HasErrors() {
		bag.AddAll(check.Moves(f))
	}
	if opts.freestanding {
		bag.AddAll(check.Freestanding(f))
	}
	all := diag.FilterDisabled(bag.Diagnostics(), opts.disabled)
	reportDiagnostics(all, opts, stdout)
	if failed(all, opts) {
		return frontEnd{}, 1
	}
	if !fileDeclaresEntry(f, opts.entry) {
		reportNoPos(entryDiagnostic(opts.entry), opts.json, buildIdentityWith(opts.opt, opts.target), stdout)
		return frontEnd{}, 1
	}
	return frontEnd{file: opts.path, modulePath: modulePath, imports: imports}, 0
}

// reportDiagnostics prints diagnostics the way will check prints them.
func reportDiagnostics(ds []diag.Diagnostic, opts buildOptions, stdout io.Writer) {
	if len(ds) == 0 {
		return
	}
	build := buildIdentityWith(opts.opt, opts.target)
	if opts.json {
		io.WriteString(stdout, diag.JSONAll(ds, build))
		return
	}
	if opts.long {
		io.WriteString(stdout, diag.FormatAllLong(ds, sourceReader()))
		return
	}
	io.WriteString(stdout, diag.FormatAll(ds))
}

// sourceReader returns a reader that reads each file's bytes once.
func sourceReader() func(string) []byte {
	cache := map[string][]byte{}
	return func(file string) []byte {
		if data, ok := cache[file]; ok {
			return data
		}
		data, err := os.ReadFile(file)
		if err != nil {
			data = nil
		}
		cache[file] = data
		return data
	}
}

// failed reports whether the run must stop: an error, or a warning under
// -Werror.
func failed(ds []diag.Diagnostic, opts buildOptions) bool {
	for _, d := range ds {
		if d.Severity == diag.Error {
			return true
		}
		if opts.werror && d.Severity == diag.Warning {
			return true
		}
	}
	return false
}

func compileIR(opts buildOptions, stdout, stderr io.Writer) ([]byte, string, int) {
	if d, bad := targetDiagnostic(opts.target, opts.targetSet); bad {
		reportNoPos(d, opts.json, buildIdentityWith(opts.opt, opts.target), stdout)
		return nil, "", 1
	}
	fe, code := runFrontEnd(opts, stdout, stderr)
	if code != 0 {
		return nil, "", code
	}
	data, err := os.ReadFile(fe.file)
	if err != nil {
		fmt.Fprintf(stderr, "will: %v\n", err)
		return nil, "", 1
	}
	store, key := buildCacheKey(fe.file, data, fe.modulePath, opts)
	if store != nil {
		if got, ok := store.Get(key, cache.ModuleLL); ok {
			return got[cache.ModuleLL], fe.modulePath, 0
		}
	}
	f, _ := parser.Parse(filepath.ToSlash(fe.file), data)
	if f == nil {
		fmt.Fprintf(stderr, "will: %s no longer parses\n", fe.file)
		return nil, "", 1
	}
	res, _ := resolve.ResolveWith(f, fe.imports)
	text, err := willc.Emit(f, res, fe.modulePath, nil)
	if err != nil {
		reportTranslate(stdout, err)
		return nil, "", 1
	}
	doc, err := willc.Read(text)
	if err != nil {
		reportTranslate(stdout, err)
		return nil, "", 1
	}
	ir, err := gen.EmitWithEntry(doc, opts.entry)
	if err != nil {
		reportTranslate(stdout, err)
		return nil, "", 1
	}
	if store != nil {
		_ = store.Put(key, map[string][]byte{cache.Willc: text, cache.ModuleLL: ir})
		_ = store.Trim()
	}
	return ir, fe.modulePath, 0
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
	h.AddStrings("compiler", compilerIdentity(), build.Target, build.Opt)
	h.AddStrings("flags", fmt.Sprintf("lib=%v", opts.lib), fmt.Sprintf("debug=%v", opts.debug),
		fmt.Sprintf("freestanding=%v", opts.freestanding), "features="+opts.features,
		"disabled="+disabledList(opts.disabled))
	return cache.New(root, 0), h.Key()
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
	ir, modulePath, code := compileIR(opts, stdout, stderr)
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
	ir, _, code := compileIR(opts, stdout, stderr)
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
	ir, _, code := compileIR(opts, stdout, stderr)
	if code != 0 {
		return code
	}
	return writeText(opts.out, ir, stdout, stderr)
}

// runEmitAsm writes the assembly text of one build, which clang produces.
func runEmitAsm(args []string, stdout, stderr io.Writer) int {
	opts, _, code := parseBuildArgs("emit-asm", args, stderr)
	if code != 0 {
		return code
	}
	ir, _, code := compileIR(opts, stdout, stderr)
	if code != 0 {
		return code
	}
	clang, err := toolchain.FindClang()
	if err != nil {
		return missingTool(stdout, "clang")
	}
	asm, err := clang.AssembleIR(ir, clangArgs(opts)...)
	if err != nil {
		fmt.Fprintf(stderr, "will: %v\n", err)
		return 1
	}
	return writeText(opts.out, []byte(asm), stdout, stderr)
}

// writeText writes one text product to a file or to standard output. Every
// command that produces text takes --out.
func writeText(out string, data []byte, stdout, stderr io.Writer) int {
	if out == "" {
		stdout.Write(data)
		return 0
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		fmt.Fprintf(stderr, "will: %v\n", err)
		return 1
	}
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
