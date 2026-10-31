package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/will-lang/will/internal/cache"
	"github.com/will-lang/will/internal/decisions"
	"github.com/will-lang/will/internal/gcmeta"
	"github.com/will-lang/will/internal/gen"
	"github.com/will-lang/will/internal/toolchain"
	"github.com/will-lang/will/internal/willc"
)

// compilePackages lowers and compiles each package into its own object. It
// returns nil when the input has no module graph, so the caller can fall back
// to the single-module path used by file mode.
func compilePackages(opts buildOptions, stdout, stderr io.Writer) ([]string, string, int) {
	fe, code := runFrontEnd(opts, stdout, stderr)
	if code != 0 || fe.build == nil {
		return nil, "", code
	}
	docs, texts, err := fe.docs(opts)
	if err != nil {
		reportTranslate(stderr, err)
		return nil, "", 1
	}
	gcRecords, err := gcmeta.DecisionRecords(docs)
	if err != nil {
		reportTranslate(stderr, err)
		return nil, "", 1
	}
	gcKey := string(decisions.Write(gcRecords))
	if len(docs) == 0 {
		return nil, "", 1
	}
	textByPath := map[string]string{}
	for i, doc := range docs {
		path := willcModulePath(doc)
		if path != "" && i < len(texts) {
			textByPath[path] += texts[i]
		}
	}
	packages := packagePaths(fe)
	if len(packages) == 0 {
		seen := map[string]bool{}
		for path := range textByPath {
			if !seen[path] {
				seen[path] = true
				packages = append(packages, path)
			}
		}
		sort.Strings(packages)
	}
	if len(packages) == 0 {
		return nil, "", 1
	}
	clang, err := toolchain.FindClang()
	if err != nil {
		return nil, "", missingTool(stderr, "clang")
	}
	root, err := cache.Root()
	if err != nil {
		return nil, "", 1
	}
	limit, _ := cache.Limit()
	objects := make([]string, 0, len(packages))
	for _, path := range packages {
		key := packageObjectKey(fe, path, textByPath, opts, gcKey)
		ext := ".o"
		if opts.lto {
			ext = ".bc"
		}
		dir := filepath.Join(root, "pkgo", key)
		obj := filepath.Join(dir, "module"+ext)
		if _, err := os.Stat(obj); err != nil {
			ir, err := gen.EmitPackageModule(docs, path, opts.entry, fe.modulePath, fe.aliases, opts.lib)
			if err != nil {
				reportTranslate(stderr, err)
				return nil, "", 1
			}
			if err := os.MkdirAll(dir, 0o755); err != nil {
				fmt.Fprintf(stderr, "will: %v\n", err)
				return nil, "", 1
			}
			tmp := filepath.Join(dir, "module"+ext+".tmp")
			args := packageObjectArgs(opts)
			output, err := clang.BuildIR(ir, tmp, args...)
			if err != nil {
				os.Remove(tmp)
				fmt.Fprintf(stderr, "will: %s: %v\n%s", path, err, output)
				return nil, "", 1
			}
			if err := os.Rename(tmp, obj); err != nil {
				if _, statErr := os.Stat(obj); statErr != nil {
					fmt.Fprintf(stderr, "will: %v\n", err)
					return nil, "", 1
				}
			}
		}
		objects = append(objects, obj)
	}
	_ = cache.New(filepath.Join(root, "pkgo"), limit).TrimIfNeeded()
	return objects, fe.modulePath, 0
}

// packageObjectArgs are the clang flags for one package module. They mirror the
// build flags but exclude runtime C inputs; those are linked at the end.
func packageObjectArgs(opts buildOptions) []string {
	args := []string{"-c"}
	if opts.lto {
		args = append(args, "-emit-llvm")
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
	return args
}

// packageObjectKey includes the target package and its transitive imports. A
// change outside that closure cannot affect this package object; a change
// inside it rebuilds the object.
func packageObjectKey(fe frontEnd, path string, textByPath map[string]string,
	opts buildOptions, gcKeys ...string) string {
	h := cache.NewHasher()
	h.AddStrings("package", path, opts.entry, fe.modulePath, compilerIdentity())
	h.AddStrings("gc-lowering", "layout-v1", "barrier-v1", "stackmap-v1")
	for _, key := range gcKeys {
		h.AddStrings("gc-decisions", key)
	}
	h.AddStrings("flags", opts.opt, fmt.Sprintf("debug=%v", opts.debug),
		fmt.Sprintf("lib=%v", opts.lib), fmt.Sprintf("lto=%v", opts.lto),
		fmt.Sprintf("freestanding=%v", opts.freestanding), "target="+opts.target,
		"features="+opts.features, "disabled="+disabledList(opts.disabled))
	aliases := make([]string, 0, len(fe.aliases))
	for from := range fe.aliases {
		aliases = append(aliases, from)
	}
	sort.Strings(aliases)
	for _, from := range aliases {
		h.AddStrings("alias", from, fe.aliases[from])
	}
	seen := map[string]bool{}
	var visit func(string)
	visit = func(pkg string) {
		if seen[pkg] {
			return
		}
		seen[pkg] = true
		h.AddStrings("source", pkg, textByPath[pkg])
		for _, imp := range packageImports(fe, pkg) {
			visit(imp)
		}
	}
	visit(path)
	if path == fe.modulePath {
		// The entry object also owns every generic instance, so a caller's
		// type arguments are part of its key. Sort the paths so a map iteration
		// cannot make the key change between identical builds.
		paths := make([]string, 0, len(textByPath))
		for pkg := range textByPath {
			paths = append(paths, pkg)
		}
		sort.Strings(paths)
		for _, pkg := range paths {
			if !seen[pkg] {
				h.AddStrings("instance-source", pkg, textByPath[pkg])
			}
		}
	}
	return h.Key()
}

// willcModulePath reads the module record a document starts with.
func willcModulePath(doc *willc.Doc) string {
	if doc == nil || len(doc.Records) == 0 {
		return ""
	}
	for _, f := range doc.Records[0].Fields {
		if f.Name == "path" && f.Value.Kind == willc.TextValue {
			return f.Value.Text
		}
	}
	return ""
}

// packagePaths returns the package paths in module order.
func packagePaths(fe frontEnd) []string {
	if fe.build == nil {
		return nil
	}
	out := make([]string, 0, len(fe.build.Packages))
	seen := map[string]bool{}
	for _, p := range fe.build.Packages {
		if p.Path != "" && !seen[p.Path] {
			seen[p.Path] = true
			out = append(out, p.Path)
		}
	}
	sort.Strings(out)
	return out
}

// packageImports returns the canonical import paths of one package.
func packageImports(fe frontEnd, path string) []string {
	if fe.build == nil {
		return nil
	}
	for _, p := range fe.build.Packages {
		if p.Path != path {
			continue
		}
		seen := map[string]bool{}
		var out []string
		for _, f := range p.Files {
			if f.AST == nil {
				continue
			}
			for _, imp := range f.AST.Imports {
				if imp.C || imp.SideEffect {
					continue
				}
				target := imp.Path
				if canonical := fe.aliases[target]; canonical != "" {
					target = canonical
				}
				if !seen[target] {
					seen[target] = true
					out = append(out, target)
				}
			}
		}
		sort.Strings(out)
		return out
	}
	return nil
}
