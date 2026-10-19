// Package pkg loads one module graph: the root module, every module a local
// replace points at, and the bundled standard library when it is used. It
// groups source files by package, checks the package names, and reports
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
	// order. The root module and the bundled standard library are not included.
	LocalModules []LocalModule
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

	g := resolveModules(&moduleNode{Dir: root, Path: m.Path, Mod: m})
	bag.AddAll(g.diags)
	if g.stdErr != nil {
		// A missing bundled standard library is a broken toolchain. The
		// command line reports it as a startup error, not as a source error.
		b.Err = g.stdErr
		return b
	}
	pkgs, table, pds := g.packages()
	bag.AddAll(pds)
	if !skipSums {
		bag.AddAll(g.checkSums())
	}
	b.byPath = table
	for _, key := range g.order {
		n := g.nodes[key]
		if n == nil || n == g.root || n.Std || !n.Local {
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
		if isExported(name) {
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
			switch decl := d.(type) {
			case *ast.ConstDecl:
				add(decl.Name, resolve.ConstKind, decl.NamePos, decl)
			case *ast.VarDecl:
				add(decl.Name, resolve.VarKind, decl.NamePos, decl)
			case *ast.FuncDecl:
				if decl.Recv == nil {
					add(decl.Name, resolve.FuncKind, decl.NamePos, decl)
				}
			case *ast.StructDecl:
				add(decl.Name, resolve.StructKind, decl.NamePos, decl)
			case *ast.EnumDecl:
				add(decl.Name, resolve.EnumKind, decl.NamePos, decl)
				for _, v := range decl.Variants {
					add(v.Name, resolve.VariantKind, v.NamePos, v)
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
	return name != "" && name[0] >= 'A' && name[0] <= 'Z'
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
	imps := b.Imports()
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
			bag.AddAll(types.CheckPackage(files, results))
		}
	}
	if !bag.HasErrors() {
		for _, p := range b.Packages {
			for _, f := range p.Files {
				if f.AST != nil {
					bag.AddAll(check.Moves(f.AST))
				}
			}
		}
	}
	return bag.Diagnostics()
}
