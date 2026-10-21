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
	"github.com/will-lang/will/internal/decisions"
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
	// the module is lowered as a whole, and modulePath names the entry package.
	build *pkg.Build
	// ast is set when the input is one file outside any module.
	ast *ast.File
	// litTypes is the resolved type of every literal of that file, so willc can
	// record it. callTypes and nameTypes do the same for the predeclared
	// constructors and values.
	litTypes  map[*ast.LiteralExpr]string
	callTypes map[*ast.CallExpr]string
	nameTypes map[*ast.NameExpr]string
	// aliases maps an import path that aliases a package to the package's
	// canonical path, so a qualified name matches the declaring package.
	aliases map[string]string
}

// runFrontEnd runs the same front end will check runs and prints the same
// diagnostics, so the two commands never disagree. It returns the entry file.
func runFrontEnd(opts buildOptions, stdout, stderr io.Writer) (frontEnd, int) {
	pkg.SetEnabledFeatures(opts.features)
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
	diagOut := stdout
	if opts.textProduct {
		diagOut = stderr
	}
	reportDiagnostics(ds, opts, diagOut)
	if failed(ds, opts) {
		return frontEnd{}, 1
	}
	imports := b.Imports()
	if opts.lib {
		for _, p := range b.Packages {
			if withinRoot(b.Root, p.Dir) {
				return frontEnd{modulePath: p.Path, imports: imports, build: b,
					aliases: b.ImportAliases()}, 0
			}
		}
		reportNoPos(entryDiagnostic(opts.entry), opts.json, buildIdentityWith(opts.opt, opts.target), diagOut)
		return frontEnd{}, 1
	}
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
	reportNoPos(entryDiagnostic(opts.entry), opts.json, buildIdentityWith(opts.opt, opts.target), diagOut)
	return frontEnd{}, 1
}

// fileFrontEnd runs the front end over one file.
func fileFrontEnd(opts buildOptions, stdout, stderr io.Writer) (frontEnd, int) {
	data, err := os.ReadFile(opts.path)
	if err != nil {
		fmt.Fprintf(stderr, "will: %v\n", err)
		return frontEnd{}, 1
	}
	diagOut := stdout
	if opts.textProduct {
		diagOut = stderr
	}
	f, ds := parser.Parse(filepath.ToSlash(opts.path), data)
	var bag diag.Bag
	bag.AddAll(ds)
	if f == nil {
		reportDiagnostics(diag.FilterDisabled(bag.Diagnostics(), opts.disabled), opts, diagOut)
		return frontEnd{}, 1
	}
	imports, modulePath, names, others, aliases := packageContext(opts.path)
	res, rds := resolve.ResolveIn(f, names, imports)
	bag.AddAll(rds)
	checked, tds := types.CheckWithFull(f, res, others)
	bag.AddAll(tds)
	if !bag.HasErrors() {
		bag.AddAll(check.Moves(f))
		bag.AddAll(check.Borrows(f))
		bag.AddAll(check.Cycles(f))
	}
	if opts.freestanding {
		bag.AddAll(check.Freestanding(f))
	}
	all := diag.FilterDisabled(bag.Diagnostics(), opts.disabled)
	reportDiagnostics(all, opts, diagOut)
	if failed(all, opts) {
		return frontEnd{}, 1
	}
	if !opts.lib && !fileDeclaresEntry(f, opts.entry) {
		reportNoPos(entryDiagnostic(opts.entry), opts.json, buildIdentityWith(opts.opt, opts.target), diagOut)
		return frontEnd{}, 1
	}
	fe := frontEnd{file: opts.path, modulePath: modulePath, imports: imports, ast: f, litTypes: checked.LiteralTypes, callTypes: checked.CallTypes, nameTypes: checked.NameTypes, aliases: aliases}
	// Inside a module, lower the whole package the file belongs to, so a call
	// to a sibling file resolves.
	if b, p := modulePackage(opts.path); b != nil {
		fe.build = b
		_ = p
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
	dir := filepath.Dir(abs)
	root, inside := findModuleRoot(dir)
	if !inside {
		return nil, nil
	}
	b := pkg.Load(root)
	if b.Err != nil || b.Mod == nil {
		return nil, nil
	}
	for _, p := range b.Packages {
		if filepath.Clean(p.Dir) == filepath.Clean(dir) {
			return b, p
		}
	}
	return nil, nil
}

// docs emits willc for every file that takes part in lowering: every package
// of a module, in package path order. The texts come out in the same order,
// because the lowering cache is keyed on them. A file-mode build lowers the
// whole module too, because a call into another package needs that package's
// document to know the callee's signature.
func (fe frontEnd) docs(opts buildOptions) ([]*willc.Doc, []string, error) {
	var docs []*willc.Doc
	var texts []string
	emit := func(f *ast.File, path, pkgPath string, res *resolve.Result, lits map[*ast.LiteralExpr]string, calls map[*ast.CallExpr]string, names map[*ast.NameExpr]string) error {
		text, err := willc.Emit(f, res, pkgPath, nil, lits, calls, names, fe.aliases)
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
			var files []*ast.File
			var paths []string
			for _, f := range p.Files {
				if f.AST == nil {
					continue
				}
				files = append(files, f.AST)
				paths = append(paths, f.Path)
			}
			cacheable := store != nil
			key := ""
			if cacheable {
				k, ok := frontKey(p, blobs, fe.aliases, opts)
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
			var calls map[*ast.CallExpr]string
			var names map[*ast.NameExpr]string
			if len(files) != 0 {
				if checked, _ := types.CheckPackageFullWithImported(files, results, fe.build.ForeignInits(), fe.build.ForeignFuncs()); checked != nil {
					if checked.LiteralTypes != nil {
						lits = checked.LiteralTypes
					}
					calls = checked.CallTypes
					names = checked.NameTypes
				}
			}
			before := len(texts)
			for i := range files {
				if err := emit(files[i], paths[i], p.Path, results[i], lits, calls, names); err != nil {
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
	if err := emit(fe.ast, fe.file, fe.modulePath, res, fe.litTypes, fe.callTypes, fe.nameTypes); err != nil {
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
	diagOut := stdout
	if opts.textProduct {
		diagOut = stderr
	}
	docs, texts, err := fe.docs(opts)
	if err != nil {
		reportTranslate(diagOut, err)
		return nil, "", 1
	}
	record, err := moduleDecisionText(fe, opts.target)
	if err != nil {
		reportTranslate(diagOut, &willc.Error{Message: err.Error()})
		return nil, "", 1
	}
	store, key := loweringCacheKey(texts, record, opts)
	if store != nil {
		if got, ok := store.Get(key, cache.ModuleLL, cache.Decisions); ok {
			return got[cache.ModuleLL], fe.modulePath, 0
		}
	}
	ir, err := gen.EmitModuleWithOptions(docs, opts.entry, fe.modulePath, fe.aliases, opts.lib)
	if err != nil {
		reportTranslate(diagOut, err)
		return nil, "", 1
	}
	if store != nil {
		_ = store.Put(key, map[string][]byte{cache.ModuleLL: ir, cache.Decisions: record})
		_ = store.Trim()
	}
	return ir, fe.modulePath, 0
}

// moduleDecisionText is the decision record of the batch a build lowers. The
// lowering key takes it so a change in a layout decision cannot reuse an old
// module, and the entry stores it next to the LLVM text.
func moduleDecisionText(fe frontEnd, target string) ([]byte, error) {
	var units []sourceUnit
	if fe.build != nil {
		for _, p := range fe.build.Packages {
			for _, f := range p.Files {
				if f.AST != nil {
					units = append(units, sourceUnit{file: f.AST, prefix: p.Path})
				}
			}
		}
	} else if fe.ast != nil {
		prefix := ""
		if b, p := modulePackage(fe.file); b != nil {
			prefix = p.Path
		}
		units = append(units, sourceUnit{file: fe.ast, prefix: prefix})
	}
	records, err := decisionRecords(units, target)
	if err != nil {
		return nil, err
	}
	return decisions.Write(records), nil
}

// loweringCacheKey keys the lowering stage on every willc text it reads, in
// package and file order, plus the entry name, the compiler identity, and the
// flags. Without the whole set, changing a called package would reuse an old
// module.
func loweringCacheKey(texts []string, record []byte, opts buildOptions) (*cache.Store, string) {
	root, err := cache.Root()
	if err != nil {
		return nil, ""
	}
	limit, _ := cache.Limit()
	h := cache.NewHasher()
	for _, text := range texts {
		h.Add("willc", []byte(text))
	}
	h.Add("decisions", record)
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
	if dir := filepath.Dir(out); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Fprintf(stderr, "will: %v\n", err)
			return 1
		}
	}
	clangArgsList, err := clangArgs(opts)
	if err != nil {
		fmt.Fprintf(stderr, "will: %v\n", err)
		return 1
	}
	inputs, cleanup, err := packageInputs(opts)
	if err != nil {
		fmt.Fprintf(stderr, "will: %v\n", err)
		return 1
	}
	defer cleanup()
	clangArgsList = append(clangArgsList, inputs...)
	if hasBitcode(clangArgsList) {
		clangArgsList = append(clangArgsList, "-flto=thin")
	}
	output, err := clang.BuildIR(ir, out, clangArgsList...)
	if err != nil {
		fmt.Fprintf(stdout, "%s", output)
		fmt.Fprintf(stderr, "will: link failed\n")
		return 1
	}
	return 0
}

// clangArgs turns the build flags into driver arguments and appends the
// runtime sources. The IR input is already selected with -x ir; -x none lets
// the C sources return to suffix-based language selection.
func clangArgs(opts buildOptions) ([]string, error) {
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
	root, err := runtimeRoot()
	if err != nil {
		return nil, err
	}
	for _, dir := range filepath.SplitList(os.Getenv("WILL_LIBPATH")) {
		if dir != "" {
			args = append(args, "-L", dir)
		}
	}
	args = append(args, "-x", "none", "-I", filepath.Join(root, "include"))
	objects, err := runtimeObjects(root, opts)
	if err != nil {
		return nil, err
	}
	args = append(args, objects...)
	if isWindows() {
		args = append(args, "-lws2_32")
	}
	return args, nil
}

// runtimeRoot finds the runtime tree without relying on the compiler binary's
// location, because go run places that binary in a temporary directory.
func runtimeRoot() (string, error) {
	if root := os.Getenv("WILL_RUNTIME"); root != "" {
		return root, nil
	}
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		candidate := filepath.Join(dir, "runtime")
		if _, err := os.Stat(filepath.Join(candidate, "include", "willrt.h")); err == nil {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("runtime directory not found; set WILL_RUNTIME")
}

// runtimeObjects returns the cached objects of every runtime C module. The key
// covers the clang identity, target, optimization, debug/PIC choice, and the
// bytes of each source, so a runtime edit cannot reuse an old object.
func runtimeObjects(root string, opts buildOptions) ([]string, error) {
	sources := runtimeSources(root, targetName(opts.target))
	clang, err := toolchain.FindClang()
	if err != nil {
		return nil, err
	}
	h := cache.NewHasher()
	version, _ := exec.Command(clang.Path, "--version").Output()
	h.AddStrings("runtime", compilerIdentity(), clang.Path, string(version),
		targetName(opts.target), opts.opt, fmt.Sprintf("debug=%v", opts.debug),
		fmt.Sprintf("lib=%v", opts.lib), fmt.Sprintf("lto=%v", opts.lto))
	for _, source := range sources {
		data, err := os.ReadFile(source)
		if err != nil {
			return nil, err
		}
		rel, err := filepath.Rel(root, source)
		if err != nil {
			rel = source
		}
		h.Add(filepath.ToSlash(rel), data)
	}
	cacheRoot, err := cache.Root()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(cacheRoot, "runtime", h.Key())
	objects := make([]string, len(sources))
	ext := ".o"
	if opts.lto {
		ext = ".bc"
	}
	for i, source := range sources {
		objects[i] = filepath.Join(dir,
			fmt.Sprintf("%02d-%s%s", i, strings.TrimSuffix(filepath.Base(source), filepath.Ext(source)), ext))
	}
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		all := true
		for _, object := range objects {
			if _, err := os.Stat(object); err != nil {
				all = false
				break
			}
		}
		if all {
			return objects, nil
		}
	}
	base := filepath.Dir(dir)
	if err := os.MkdirAll(base, 0o755); err != nil {
		return nil, err
	}
	temporary, err := os.MkdirTemp(base, filepath.Base(dir)+"-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(temporary)
	for i, source := range sources {
		emit := []string{"-c"}
		if opts.lto {
			emit = []string{"-emit-llvm", "-c"}
		}
		args := append([]string{"-std=c11"}, emit...)
		args = append(args, source, "-o", filepath.Join(temporary, filepath.Base(objects[i])))
		args = append(args, "-I", filepath.Join(root, "include"))
		if opts.opt != "" {
			args = append(args, opts.opt)
		}
		if opts.debug {
			args = append(args, "-g")
		}
		if opts.lib {
			args = append(args, "-fPIC")
		}
		if opts.target != "" {
			args = append(args, "--target="+opts.target)
		}
		cmd := exec.Command(clang.Path, args...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("%s: %v\n%s", source, err, output)
		}
	}
	if err := os.Rename(temporary, dir); err != nil {
		// Another build may have published the same content-addressed
		// directory while this one was compiling.
		if _, statErr := os.Stat(dir); statErr != nil {
			return nil, err
		}
	}
	return objects, nil
}

// runtimeSources lists the C modules every executable needs. The platform file
// is chosen from the target triple, with the host as the default.
func runtimeSources(root, target string) []string {
	platform := "win.c"
	switch {
	case strings.Contains(target, "linux"):
		platform = "linux.c"
	case strings.Contains(target, "darwin"), strings.Contains(target, "mac"):
		platform = "mac.c"
	case strings.Contains(target, "android"):
		platform = "android.c"
	case strings.Contains(target, "ios"):
		platform = "ios.c"
	}
	core := []string{
		filepath.Join("core", "task", "src", "task.c"),
		filepath.Join("core", "chan", "src", "chan.c"),
		filepath.Join("core", "blocking", "src", "blocking.c"),
		filepath.Join("core", "poll", "src", "poll.c"),
		filepath.Join("core", "mem", "src", "alloc.c"),
		filepath.Join("core", "rc", "src", "rc.c"),
		filepath.Join("core", "random", "src", "random.c"),
		filepath.Join("core", "report", "src", "report.c"),
		filepath.Join("core", "startup", "src", "startup.c"),
		filepath.Join("core", "timer", "src", "timer.c"),
		filepath.Join("core", "utf8", "src", "utf8.c"),
		filepath.Join("core", "bytes", "src", "bytes.c"),
	}
	out := make([]string, 0, len(core)+1)
	for _, src := range core {
		out = append(out, filepath.Join(root, src))
	}
	out = append(out, filepath.Join(root, "platform", platform))
	return out
}

// compileArgs are the flags for IR-only work such as emit-asm; they must not
// include runtime C sources.
func compileArgs(opts buildOptions) []string {
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
	return args
}

// packageInputs collects the C sources of the built module graph and the
// external libraries its will.mod names. Package C sources come before the
// named libraries, so a library can satisfy a symbol the sources call.
func packageInputs(opts buildOptions) ([]string, func(), error) {
	noop := func() {}
	if info, err := os.Stat(opts.path); err != nil || !info.IsDir() {
		return nil, noop, nil
	}
	b := pkg.Load(opts.path)
	if b.Err != nil {
		return nil, noop, b.Err
	}
	var sources []string
	seen := map[string]bool{}
	for _, p := range b.Packages {
		err := filepath.Walk(p.Dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() || filepath.Ext(path) != ".c" {
				return nil
			}
			if !seen[path] {
				seen[path] = true
				sources = append(sources, path)
			}
			return nil
		})
		if err != nil {
			return nil, noop, err
		}
	}
	var out []string
	cleanup := noop
	if len(sources) != 0 {
		clang, err := toolchain.FindClang()
		if err != nil {
			return nil, noop, err
		}
		dir, err := os.MkdirTemp("", "will-c-bc")
		if err != nil {
			return nil, noop, err
		}
		cleanup = func() { os.RemoveAll(dir) }
		root, rootErr := runtimeRoot()
		for i, source := range sources {
			ext := ".o"
			emit := []string{"-c"}
			if opts.lto {
				ext = ".bc"
				emit = []string{"-emit-llvm", "-c"}
			}
			bc := filepath.Join(dir, fmt.Sprintf("c%d%s", i, ext))
			args := append(emit, source, "-o", bc)
			args = append(args, "-I", filepath.Dir(source))
			if rootErr == nil {
				args = append(args, "-I", filepath.Join(root, "include"))
			}
			for _, include := range filepath.SplitList(os.Getenv("WILL_C_INCLUDE")) {
				if include != "" {
					args = append(args, "-I", include)
				}
			}
			if opts.opt != "" {
				args = append(args, opts.opt)
			}
			if opts.debug {
				args = append(args, "-g")
			}
			if opts.lib {
				args = append(args, "-fPIC")
			}
			if opts.target != "" {
				args = append(args, "--target="+opts.target)
			}
			cmd := exec.Command(clang.Path, args...)
			output, err := cmd.CombinedOutput()
			if err != nil {
				cleanup()
				return nil, noop, fmt.Errorf("%s: %v\n%s", source, err, output)
			}
			out = append(out, bc)
		}
	}
	for _, link := range b.Links {
		if strings.ContainsAny(link, `/\`) || filepath.IsAbs(link) ||
			strings.HasSuffix(link, ".a") || strings.HasSuffix(link, ".so") ||
			strings.HasSuffix(link, ".dylib") || strings.HasSuffix(link, ".lib") {
			out = append(out, link)
			continue
		}
		out = append(out, "-l"+link)
	}
	return out, cleanup, nil
}

// hasBitcode reports whether the package inputs contain LLVM bitcode, which
// is what asks the driver to run the LTO backend.
func hasBitcode(inputs []string) bool {
	for _, input := range inputs {
		if strings.HasSuffix(input, ".bc") {
			return true
		}
	}
	return false
}

func targetName(target string) string {
	if target != "" {
		return target
	}
	return targetTriple()
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
	clangArgsList, err := clangArgs(opts)
	if err != nil {
		fmt.Fprintf(stderr, "will: %v\n", err)
		return 1
	}
	inputs, cleanup, err := packageInputs(opts)
	if err != nil {
		fmt.Fprintf(stderr, "will: %v\n", err)
		return 1
	}
	defer cleanup()
	clangArgsList = append(clangArgsList, inputs...)
	if hasBitcode(clangArgsList) {
		clangArgsList = append(clangArgsList, "-flto=thin")
	}
	output, err := clang.BuildIR(ir, out, clangArgsList...)
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
	opts.textProduct = true
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
	opts.textProduct = true
	ir, _, code := compileIR(opts, stdout, stderr)
	if code != 0 {
		return code
	}
	clang, err := toolchain.FindClang()
	if err != nil {
		return missingTool(stdout, "clang")
	}
	asm, err := clang.AssembleIR(ir, compileArgs(opts)...)
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
