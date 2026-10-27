// Package pkg loads one module graph: the root module and every module a local
// replace points at. It groups source files by package, checks the package
// names, and reports
// methods that leave their type's package, one package reached through two
// import paths, and import cycles.
package pkg

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/check"
	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/mod"
	"github.com/will-lang/will/internal/resolve"
	"github.com/will-lang/will/internal/source"
	"github.com/will-lang/will/internal/types"
)

// File is one parsed source file with its path relative to its module root.
type File struct {
	Path string
	AST  *ast.File
}

// Package is one directory.
type Package struct {
	Path string
	Dir  string
	// Root is the directory of the module this package belongs to. A file's
	// Path is relative to that root, not to Dir, because a package may sit in
	// a subdirectory of its module and a dependency lives in another tree.
	Root  string
	Name  string
	Files []*File
	// Names is every top-level name of the package, private names included,
	// so a file of the package can resolve a declaration made in a sibling
	// file. Exports is the subset other packages may use.
	Names   resolve.Exported
	Exports resolve.Exported
	// Diags holds the diagnostics of the package itself, such as a name
	// declared in two files.
	Diags []diag.Diagnostic
}

// Build is the loaded module graph.
type Build struct {
	Root     string
	Mod      *mod.Module
	Packages []*Package
	Diags    []diag.Diagnostic
	Err      error
	// byPath maps every import path a source file may write to its package.
	// A local replace key that differs from the module path is another name
	// for the same package, so the table has more entries than Packages.
	byPath map[string]*Package
	// LocalModules lists every module loaded through a local replace, in path
	// order. The root module and the standard library module are not included.
	LocalModules []LocalModule
	// Links are the external libraries the root module asks the linker to
	// include, in declaration order.
	Links []string
}

// LocalModule is one module a local replace selected.
type LocalModule struct {
	Path string
	Dir  string
}

// Load reads the module root, will.mod, every local replace target, and the
// modules those dependencies require.
func Load(root string) *Build {
	return load(root, false)
}

// LoadWithoutSums reads the same graph as Load but leaves the checksum
// comparison out, because will mod tidy repairs the file that comparison reads.
func LoadWithoutSums(root string) *Build {
	return load(root, true)
}

func load(root string, skipSums bool) *Build {
	// The package-name rule compares directory names, so the root is resolved
	// first; a relative root such as "." would otherwise compare as ".".
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	root = filepath.Clean(root)
	b := &Build{Root: root}
	data, err := os.ReadFile(filepath.Join(root, "will.mod"))
	if err != nil {
		b.Err = err
		return b
	}
	m, ds := mod.Parse("will.mod", data)
	var bag diag.Bag
	bag.AddAll(ds)
	if m == nil {
		b.Diags = bag.Diagnostics()
		return b
	}
	b.Mod = m
	b.Links = append(b.Links, m.Links...)

	g := resolveModules(&moduleNode{Dir: root, Path: m.Path, Mod: m})
	bag.AddAll(g.diags)
	pkgs, table, pds := g.packages()
	stdDir := ""
	for _, key := range g.order {
		if n := g.nodes[key]; n != nil && n.Path == "std" {
			stdDir = n.Dir
			break
		}
	}
	pkgs, pds = keepLoadedPackages(pkgs, table, b.Root, stdDir, pds)
	pds = append(pds, applyLoadedHeaders(pkgs)...)
	bag.AddAll(pds)
	if !skipSums {
		bag.AddAll(g.checkSums())
	}
	b.byPath = table
	for _, key := range g.order {
		n := g.nodes[key]
		if n == nil || n == g.root || n.Path == "std" || !n.Local {
			continue
		}
		for _, k := range n.Keys {
			b.LocalModules = append(b.LocalModules, LocalModule{Path: k, Dir: n.Dir})
		}
	}
	sort.Slice(b.LocalModules, func(i, j int) bool {
		if b.LocalModules[i].Path != b.LocalModules[j].Path {
			return b.LocalModules[i].Path < b.LocalModules[j].Path
		}
		return b.LocalModules[i].Dir < b.LocalModules[j].Dir
	})
	deduped := b.LocalModules[:0]
	for _, lm := range b.LocalModules {
		if len(deduped) != 0 && deduped[len(deduped)-1].Path == lm.Path {
			continue
		}
		deduped = append(deduped, lm)
	}
	b.LocalModules = deduped

	edges := importEdges(pkgs, table)
	bag.AddAll(checkTwoPaths(pkgs, table))
	bag.AddAll(checkImportCycles(pkgs, edges))
	bag.AddAll(checkMethodPackages(pkgs))
	b.Packages = orderPackages(pkgs, edges)
	b.Diags = bag.Diagnostics()
	return b
}

// Imports returns the exported names of every package, keyed by import path.
// The replace keys that name a local package under a different path are
// included, so source files can import the module the way will.mod names it.
func (b *Build) Imports() resolve.Imports {
	out := resolve.Imports{}
	if b.byPath != nil {
		for path, p := range b.byPath {
			out[path] = p.Exports
		}
		return out
	}
	for _, p := range b.Packages {
		out[p.Path] = p.Exports
	}
	return out
}

// ImportAliases maps an import path that only aliases a package to that
// package's canonical import path. A symbol reached through the alias is
// emitted under the canonical name, which is the name the declaring package
// emits, so a call and its definition always agree.
func (b *Build) ImportAliases() map[string]string {
	out := map[string]string{}
	if b.byPath == nil {
		return out
	}
	for path, p := range b.byPath {
		if path != p.Path {
			out[path] = p.Path
		}
	}
	return out
}

func importPath(modulePath, root, dir string) string {
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == "." {
		return modulePath
	}
	return modulePath + "/" + filepath.ToSlash(rel)
}

func lastSegment(path string) string {
	path = strings.TrimSuffix(path, "/")
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		return path[i+1:]
	}
	return path
}

func validPackageName(name string) bool {
	if name == "" || name[0] < 'a' || name[0] > 'z' {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c == '_' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
			continue
		}
		return false
	}
	return true
}

type methodDecl struct {
	typ     string
	name    string
	pos     source.Pos
	pkgPath string
}

// checkMethodPackages reports N0006 for a method declared outside the package
// that declares its receiver type.
func checkMethodPackages(pkgs []*Package) []diag.Diagnostic {
	declared := map[string]map[string]bool{}
	var methods []methodDecl
	for _, p := range pkgs {
		for _, f := range p.Files {
			if f.AST == nil {
				continue
			}
			for _, d := range f.AST.Decls {
				switch decl := d.(type) {
				case *ast.StructDecl:
					markDeclared(declared, decl.Name, p.Path)
				case *ast.EnumDecl:
					markDeclared(declared, decl.Name, p.Path)
				case *ast.InterfaceDecl:
					markDeclared(declared, decl.Name, p.Path)
				case *ast.FuncDecl:
					if decl.Recv != nil {
						methods = append(methods, methodDecl{
							typ:     receiverName(decl.Recv.Type),
							name:    decl.Name,
							pos:     decl.NamePos,
							pkgPath: p.Path,
						})
					}
				}
			}
		}
	}
	var bag diag.Bag
	for _, m := range methods {
		owners := declared[m.typ]
		if len(owners) == 0 || owners[m.pkgPath] {
			continue
		}
		bag.Error(m.pos, diag.CodeMethodsTwoPackages,
			"methods of "+m.typ+" are declared in two packages",
			"a method can only be declared where its type is declared")
	}
	return bag.Diagnostics()
}

// markDeclared records that pkgPath declares the type name. Types of two
// packages may share a name, so the owner is a set and not one package.
func markDeclared(declared map[string]map[string]bool, name, pkgPath string) {
	if name == "" {
		return
	}
	owners := declared[name]
	if owners == nil {
		owners = map[string]bool{}
		declared[name] = owners
	}
	owners[pkgPath] = true
}

func receiverName(t *ast.Type) string {
	if t == nil {
		return ""
	}
	return t.Name
}

// buildExports collects the exported top-level names of one package.
func buildExports(p *Package) resolve.Exported {
	out := resolve.Exported{}
	for name, sym := range p.Names {
		if isExported(name) && sym.Kind != resolve.VariantKind {
			out[name] = sym
		}
	}
	return out
}

// buildNames collects every top-level name of one package, private names
// included, and reports a name declared in more than one file: within a
// package, two declarations with the same name are an error even when they
// are written in different files.
func buildNames(p *Package) (resolve.Exported, []diag.Diagnostic) {
	out := resolve.Exported{}
	var bag diag.Bag
	seen := map[string]ast.Node{}
	// A variant is not a package-level value, so it does not collide with a
	// function or constant of the same name. Two variants with one bare name
	// still collide, because a pattern names a variant without its enum.
	variants := map[string]ast.Node{}
	for _, f := range p.Files {
		if f.AST == nil {
			continue
		}
		for _, d := range f.AST.Decls {
			add := func(name string, kind resolve.Kind, pos source.Pos, node ast.Node) {
				if name == "" {
					return
				}
				if prev, ok := seen[name]; ok {
					_ = prev
					bag.Error(pos, diag.CodeDuplicateName, "duplicate top-level name "+name,
						"names must be unique in a package: shadowing is a warning, two declarations with the same name are an error")
					return
				}
				seen[name] = node
				out[name] = &resolve.Symbol{Name: name, Kind: kind, Pos: pos, Node: node, Exported: isExported(name)}
			}
			addVariant := func(name string, pos source.Pos, node ast.Node) {
				if name == "" {
					return
				}
				if _, ok := variants[name]; ok {
					bag.Error(pos, diag.CodeDuplicateName, "duplicate top-level name "+name,
						"names must be unique in a package: shadowing is a warning, two declarations with the same name are an error")
					return
				}
				variants[name] = node
				out[name] = &resolve.Symbol{Name: name, Kind: resolve.VariantKind, Pos: pos, Node: node, Exported: isExported(name)}
			}
			switch decl := d.(type) {
			case *ast.ConstDecl:
				add(decl.Name, resolve.ConstKind, decl.NamePos, decl)
			case *ast.VarDecl:
				add(decl.Name, resolve.VarKind, decl.NamePos, decl)
			case *ast.TypeAliasDecl:
				add(decl.Name, resolve.TypeAliasKind, decl.NamePos, decl)
			case *ast.FuncDecl:
				if decl.Recv == nil {
					add(decl.Name, resolve.FuncKind, decl.NamePos, decl)
				}
			case *ast.StructDecl:
				add(decl.Name, resolve.StructKind, decl.NamePos, decl)
			case *ast.EnumDecl:
				add(decl.Name, resolve.EnumKind, decl.NamePos, decl)
				for _, v := range decl.Variants {
					addVariant(v.Name, v.NamePos, v)
				}
			case *ast.InterfaceDecl:
				add(decl.Name, resolve.InterfaceKind, decl.NamePos, decl)
			case *ast.LaneDecl:
				add(decl.Name, resolve.LaneKind, decl.NamePos, decl)
			}
		}
	}
	return out, bag.Diagnostics()
}

func isExported(name string) bool {
	return name != "" && !strings.HasPrefix(name, "__")
}

// Dispatch is one interface call site.
type Dispatch struct {
	File      string
	Pos       source.Pos
	Interface string
}

// DispatchPoints runs the type checker over every file and collects the
// interface call sites, sorted by file and position.
func (b *Build) DispatchPoints() []Dispatch {
	imps := b.Imports()
	var out []Dispatch
	for _, p := range b.Packages {
		for _, f := range p.Files {
			if f.AST == nil {
				continue
			}
			res, _ := resolve.ResolveIn(f.AST, p.Names, imps)
			tr, _ := types.CheckFull(f.AST, res)
			for _, dp := range tr.Dispatch {
				out = append(out, Dispatch{File: f.Path, Pos: dp.Pos, Interface: dp.Interface})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		if out[i].Pos.Line != out[j].Pos.Line {
			return out[i].Pos.Line < out[j].Pos.Line
		}
		return out[i].Pos.Col < out[j].Pos.Col
	})
	return out
}

// CheckAll resolves and checks every package as a unit: the files of one
// package see each other's declarations, and one declaration environment
// covers the whole package. The move analysis runs only when the front end
// found no errors, so it never piles onto a broken program.
func (b *Build) CheckAll() []diag.Diagnostic {
	var bag diag.Bag
	bag.AddAll(b.Diags)
	foreign := b.foreignInits()
	foreignFuncs := b.foreignFuncs()
	imps := b.Imports()
	checkedByFile := map[*ast.File]*types.Result{}
	for _, p := range b.Packages {
		var files []*ast.File
		var results []*resolve.Result
		for _, f := range p.Files {
			if f.AST == nil {
				continue
			}
			res, ds := resolve.ResolveIn(f.AST, p.Names, imps)
			bag.AddAll(ds)
			files = append(files, f.AST)
			results = append(results, res)
		}
		if len(files) > 0 {
			checked, tds := types.CheckPackageFullWithImported(files, results, foreign, foreignFuncs)
			bag.AddAll(tds)
			for _, f := range files {
				checkedByFile[f] = checked
			}
		}
	}
	if !bag.HasErrors() {
		var ownershipFiles []*ast.File
		for _, p := range b.Packages {
			for _, f := range p.Files {
				if f.AST != nil {
					ownershipFiles = append(ownershipFiles, f.AST)
				}
			}
		}
		ownership := check.NewOwnership(ownershipFiles)
		effects := check.NewMutEffects(ownershipFiles)
		for _, p := range b.Packages {
			for _, f := range p.Files {
				if f.AST != nil {
					bag.AddAll(check.MovesWithOwnership(f.AST, ownership))
					bag.AddAll(check.BorrowsWithEffects(f.AST, ownership, effects))
					bag.AddAll(check.TopLevelWritesWithEffects(f.AST, effects))
					bag.AddAll(check.Leaks(f.AST))
					bag.AddAll(check.CrossTask(f.AST, checkedByFile[f.AST]))
					bag.AddAll(check.Concurrency(f.AST))
					bag.AddAll(check.SuspendInParallel(f.AST))
				}
			}
		}
	}
	return suppressFeatureCascade(bag.Diagnostics())
}

// ForeignInits exposes the imported init declarations used by the type
// checker, so the build front end can type new across package boundaries.
func (b *Build) ForeignInits() map[string]map[string]*ast.FuncDecl {
	return b.foreignInits()
}

// ForeignFuncs exposes imported free functions for the same cross-package
// argument check the package checker performs.
func (b *Build) ForeignFuncs() map[string]map[string]*ast.FuncDecl {
	return b.foreignFuncs()
}

// foreignInits collects every init declaration by package name and type name.
// The type checker needs the result of an imported init before it can type new.
func (b *Build) foreignInits() map[string]map[string]*ast.FuncDecl {
	out := map[string]map[string]*ast.FuncDecl{}
	for _, p := range b.Packages {
		for _, f := range p.Files {
			if f.AST == nil {
				continue
			}
			for _, d := range f.AST.Decls {
				fn, ok := d.(*ast.FuncDecl)
				if !ok || fn.Recv == nil || fn.Name != "init" || fn.Recv.Type == nil {
					continue
				}
				if out[p.Name] == nil {
					out[p.Name] = map[string]*ast.FuncDecl{}
				}
				out[p.Name][fn.Recv.Type.Name] = fn
			}
		}
	}
	return out
}

// foreignFuncs collects exported free functions and methods by package name
// and function name. A method is keyed as "Type.Method"; a cross-package call
// needs the declaring signature for its arguments and its async bit.
func (b *Build) foreignFuncs() map[string]map[string]*ast.FuncDecl {
	out := map[string]map[string]*ast.FuncDecl{}
	for _, p := range b.Packages {
		for _, f := range p.Files {
			if f.AST == nil {
				continue
			}
			for _, d := range f.AST.Decls {
				fn, ok := d.(*ast.FuncDecl)
				if !ok {
					continue
				}
				key := fn.Name
				if fn.Recv != nil {
					if fn.Recv.Type == nil || fn.Recv.Type.Name == "" {
						continue
					}
					key = fn.Recv.Type.Name + "." + fn.Name
				}
				if out[p.Name] == nil {
					out[p.Name] = map[string]*ast.FuncDecl{}
				}
				out[p.Name][key] = fn
			}
		}
	}
	return out
}

// suppressFeatureCascade drops the name error that follows a Feature call left
// outside an if: T0010 is the root cause, and the removed std/meta import would
// otherwise also report meta as undefined at the same position.
// keepLoadedPackages drops standard-library packages that the root module
// cannot reach. The std tree is scanned before this point so a package path
// still resolves; parsing and checking an unused std package must not make a
// build fail. Diagnostics whose source belongs to a dropped package are dropped
// with it.
func keepLoadedPackages(pkgs []*Package, table map[string]*Package, root, stdDir string, ds []diag.Diagnostic) ([]*Package, []diag.Diagnostic) {
	if stdDir == "" {
		return pkgs, ds
	}
	reachable := map[*Package]bool{}
	var queue []*Package
	for _, p := range pkgs {
		if p.Root == root {
			reachable[p] = true
			queue = append(queue, p)
		}
	}
	for len(queue) != 0 {
		p := queue[0]
		queue = queue[1:]
		for _, f := range p.Files {
			if f.AST == nil {
				continue
			}
			for _, imp := range f.AST.Imports {
				if imp.C {
					continue
				}
				next := table[imp.Path]
				if next != nil && !reachable[next] {
					reachable[next] = true
					queue = append(queue, next)
				}
			}
		}
	}
	var kept []*Package
	var dropped []*Package
	for _, p := range pkgs {
		if p.Root == stdDir && !reachable[p] {
			dropped = append(dropped, p)
			continue
		}
		kept = append(kept, p)
	}
	if len(dropped) == 0 {
		return kept, ds
	}
	out := ds[:0]
	rootName := filepath.ToSlash(filepath.Base(stdDir))
	for _, d := range ds {
		if filepath.ToSlash(d.Pos.File) == rootName || droppedDiagnostic(d, dropped) {
			continue
		}
		out = append(out, d)
	}
	return kept, out
}

// droppedDiagnostic reports whether a diagnostic belongs to one of the dropped
// standard-library packages. A root package's package-name diagnostic names
// the directory itself, while a file diagnostic names a relative file path.
func droppedDiagnostic(d diag.Diagnostic, dropped []*Package) bool {
	file := filepath.ToSlash(d.Pos.File)
	for _, p := range dropped {
		rel, err := filepath.Rel(p.Root, p.Dir)
		if err != nil {
			continue
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			if file == filepath.Base(p.Dir) {
				return true
			}
			continue
		}
		if file == rel || strings.HasPrefix(file, rel+"/") {
			return true
		}
	}
	return false
}

func suppressFeatureCascade(ds []diag.Diagnostic) []diag.Diagnostic {
	root := map[source.Pos]bool{}
	for _, d := range ds {
		if d.Code == diag.CodeFeatureOutsideIf {
			root[d.Pos] = true
		}
	}
	if len(root) == 0 {
		return ds
	}
	out := ds[:0]
	for _, d := range ds {
		if d.Code == diag.CodeUndefinedName && root[d.Pos] {
			continue
		}
		out = append(out, d)
	}
	return out
}
