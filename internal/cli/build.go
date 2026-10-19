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

// frontEnd is what the lowering step needs from the front end: the loaded
// module (or, without a module, the one file), the import path of the entry's
// package, and the packages' exported names.
type frontEnd struct {
	file       string
	modulePath string
	imports    resolve.Imports
	// build is set when the input is a directory or a file inside a module;
	// only limits which package is lowered in the file case.
	build *pkg.Build
	limit string
	// ast is set when the input is one file outside any module.
	ast *ast.File
	// litTypes is the resolved type of every literal of that file, so willc can
	// record it.
	litTypes map[*ast.LiteralExpr]string
	// aliases maps an import path that aliases a package to the package's
	// canonical path, so a qualified name matches the declaring package.
	aliases map[string]string
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
		return frontEnd{}, loadFailure(b.Err, stderr)
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
	imports := b.Imports()
	for _, p := range b.Packages {
		// Only the module being built can name the entry. A dependency that
		// happens to declare the same name is not this program's entry.
		if !withinRoot(b.Root, p.Dir) {
			continue
		}
		for _, f := range p.Files {
			if f.AST == nil {
				continue
			}
			for _, d := range f.AST.Decls {
				if declaresEntry(d, opts.entry) {
					// File.Path is relative to the module root, not to the
					// package directory.
					return frontEnd{file: filepath.Join(b.Root, f.Path), modulePath: p.Path,
						imports: imports, build: b, aliases: b.ImportAliases()}, 0
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
	imports, modulePath, names, others, aliases := packageContext(opts.path)
	res, rds := resolve.ResolveIn(f, names, imports)
	bag.AddAll(rds)
	checked, tds := types.CheckWithFull(f, res, others)
	bag.AddAll(tds)
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
	fe := frontEnd{file: opts.path, modulePath: modulePath, imports: imports, ast: f, litTypes: checked.LiteralTypes, aliases: aliases}
	// Inside a module, lower the whole package the file belongs to, so a call
	// to a sibling file resolves.
	if b, p := modulePackage(opts.path); b != nil {
		fe.build = b
		fe.limit = p.Path
	}
	return fe, 0
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

// modulePackage loads the module that contains file and returns its load plus
// the package the file belongs to.
func modulePackage(file string) (*pkg.Build, *pkg.Package) {
	abs, err := filepath.Abs(file)
	if err != nil {
		abs = file
	}
	b := pkg.Load(filepath.Dir(abs))
	if b.Err != nil || b.Mod == nil {
		return nil, nil
	}
	for _, p := range b.Packages {
		if filepath.Clean(p.Dir) == filepath.Clean(filepath.Dir(abs)) {
			return b, p
		}
	}
	return nil, nil
}

// docs emits willc for every file that takes part in lowering: every package
// of a module, in package path order, or the entry file's package in file mode.
// The texts come out in the same order, because the lowering cache is keyed on
// them.
func (fe frontEnd) docs(opts buildOptions) ([]*willc.Doc, []string, error) {
	var docs []*willc.Doc
	var texts []string
	emit := func(f *ast.File, path, pkgPath string, res *resolve.Result, lits map[*ast.LiteralExpr]string) error {
		text, err := willc.Emit(f, res, pkgPath, nil, lits, fe.aliases)
		if err != nil {
			return err
		}
		doc, err := willc.Read(text)
		if err != nil {
			return err
		}
		docs = append(docs, doc)
		texts = append(texts, string(text))
		return nil
	}
	if fe.build != nil {
		store := frontStore()
		blobs := map[string][]byte{}
		for _, p := range fe.build.Packages {
			if fe.limit != "" && p.Path != fe.limit {
				continue
			}
			var files []*ast.File
			var paths []string
			for _, f := range p.Files {
				if f.AST == nil {
					continue
				}
				files = append(files, f.AST)
				paths = append(paths, f.Path)
			}
			cacheable := store != nil && fe.limit == ""
			key := ""
			if cacheable {
				k, ok := frontKey(p, blobs, opts)
				if !ok {
					cacheable = false
				} else {
					key = k
				}
			}
			if cacheable {
				if data, hit := store.Get(key, cache.Willc); hit {
					cachedPaths, cachedTexts, ok := decodeWillc(data[cache.Willc])
					if ok && samePaths(cachedPaths, paths) {
						docsBefore, textsBefore := len(docs), len(texts)
						good := true
						for _, text := range cachedTexts {
							doc, err := willc.Read([]byte(text))
							if err != nil {
								good = false
								break
							}
							docs = append(docs, doc)
							texts = append(texts, text)
						}
						if good {
							blobs[p.Path] = data[cache.Willc]
							frontEndStats.hits++
							continue
						}
						docs, texts = docs[:docsBefore], texts[:textsBefore]
					}
				}
			}
			// The literal types come from one check of the whole package, so a
			// literal inherits the type its context gives it.
			var results []*resolve.Result
			for _, f := range files {
				res, _ := resolve.ResolveIn(f, p.Names, fe.imports)
				results = append(results, res)
			}
			lits := map[*ast.LiteralExpr]string{}
			if len(files) != 0 {
				if checked, _ := types.CheckPackageFull(files, results); checked != nil {
					if checked.LiteralTypes != nil {
						lits = checked.LiteralTypes
					}
				}
			}
			before := len(texts)
			for i := range files {
				if err := emit(files[i], paths[i], p.Path, results[i], lits); err != nil {
					return nil, nil, err
				}
			}
			if cacheable {
				blob := encodeWillc(paths, texts[before:])
				if err := store.Put(key, map[string][]byte{cache.Willc: blob}); err == nil {
					blobs[p.Path] = blob
				}
				frontEndStats.misses++
			}
		}
		if store != nil {
			_ = store.Trim()
		}
		return docs, texts, nil
	}
	if fe.ast == nil {
		return nil, nil, &willc.Error{Message: "no source to lower"}
	}
	res, _ := resolve.ResolveIn(fe.ast, nil, fe.imports)
	if err := emit(fe.ast, fe.file, fe.modulePath, res, fe.litTypes); err != nil {
		return nil, nil, err
	}
	return docs, texts, nil
}

// samePaths reports whether the cached file names match the current files.
func samePaths(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
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
	docs, texts, err := fe.docs(opts)
	if err != nil {
		reportTranslate(stdout, err)
		return nil, "", 1
	}
	store, key := loweringCacheKey(texts, opts)
	if store != nil {
		if got, ok := store.Get(key, cache.ModuleLL); ok {
			return got[cache.ModuleLL], fe.modulePath, 0
		}
	}
	ir, err := gen.EmitModule(docs, opts.entry)
	if err != nil {
		reportTranslate(stdout, err)
		return nil, "", 1
	}
	if store != nil {
		_ = store.Put(key, map[string][]byte{cache.ModuleLL: ir})
		_ = store.Trim()
	}
	return ir, fe.modulePath, 0
}

// loweringCacheKey keys the lowering stage on every willc text it reads, in
// package and file order, plus the entry name, the compiler identity, and the
// flags. Without the whole set, changing a called package would reuse an old
// module.
func loweringCacheKey(texts []string, opts buildOptions) (*cache.Store, string) {
	root, err := cache.Root()
	if err != nil {
		return nil, ""
	}
	limit, _ := cache.Limit()
	h := cache.NewHasher()
	for _, text := range texts {
		h.Add("willc", []byte(text))
	}
	h.AddStrings("entry", opts.entry)
	build := buildIdentityWith(opts.opt, opts.target)
	h.AddStrings("compiler", compilerIdentity(), build.Target, build.Opt)
	h.AddStrings("flags", fmt.Sprintf("lib=%v", opts.lib), fmt.Sprintf("debug=%v", opts.debug),
		fmt.Sprintf("freestanding=%v", opts.freestanding), "features="+opts.features,
		"disabled="+disabledList(opts.disabled))
	return cache.New(root, limit), h.Key()
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

// withinRoot reports whether dir is root or below it. The module root and its
// packages pass; a local replace target beside the root does not.
func withinRoot(root, dir string) bool {
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
