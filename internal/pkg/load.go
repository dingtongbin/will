// Package pkg loads one module directory: it groups source files by package,
// checks the package names, and reports methods that leave their type's
// package.
package pkg

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/mod"
	"github.com/will-lang/will/internal/parser"
	"github.com/will-lang/will/internal/resolve"
	"github.com/will-lang/will/internal/source"
	"github.com/will-lang/will/internal/types"
)

// File is one parsed source file with its path relative to the module root.
type File struct {
	Path string
	AST  *ast.File
}

// Package is one directory.
type Package struct {
	Path    string
	Dir     string
	Name    string
	Files   []*File
	Exports resolve.Exported
}

// Build is the loaded module.
type Build struct {
	Root     string
	Mod      *mod.Module
	Packages []*Package
	Diags    []diag.Diagnostic
	Err      error
}

// Load reads the module root, will.mod, and every .will file under it.
func Load(root string) *Build {
	// The package-name rule compares directory names, so the root is resolved
	// first; a relative root such as "." would otherwise compare as ".".
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
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

	if rootName := filepath.Base(root); rootName != lastSegment(m.Path) {
		bag.Error(source.Pos{File: rootName, Line: 1, Col: 1}, diag.CodePackageNameMismatch,
			"package name does not match the directory name", "rename one of them")
	}

	dirs := map[string][]string{}
	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if name != "." && (strings.HasPrefix(name, ".") || name == "build" || name == "desc") {
				return fs.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) == ".will" {
			dir := filepath.Dir(path)
			dirs[dir] = append(dirs[dir], path)
		}
		return nil
	})
	if _, ok := dirs[root]; !ok {
		dirs[root] = nil
	}

	var pkgs []*Package
	for dir, files := range dirs {
		name := filepath.Base(dir)
		if !validPackageName(name) {
			bag.Error(source.Pos{File: name, Line: 1, Col: 1}, diag.CodePackageNameMismatch,
				"package name does not match the directory name", "rename one of them")
			continue
		}
		sort.Strings(files)
		p := &Package{Dir: dir, Name: name, Path: importPath(m.Path, root, dir)}
		for _, path := range files {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				continue
			}
			src, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			f, fds := parser.Parse(filepath.ToSlash(rel), src)
			bag.AddAll(fds)
			p.Files = append(p.Files, &File{Path: filepath.ToSlash(rel), AST: f})
		}
		p.Exports = buildExports(p)
		pkgs = append(pkgs, p)
	}
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].Path < pkgs[j].Path })
	b.Packages = pkgs

	bag.AddAll(checkMethodPackages(pkgs))
	b.Diags = bag.Diagnostics()
	return b
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
	declared := map[string]string{}
	var methods []methodDecl
	for _, p := range pkgs {
		for _, f := range p.Files {
			if f.AST == nil {
				continue
			}
			for _, d := range f.AST.Decls {
				switch decl := d.(type) {
				case *ast.StructDecl:
					declared[decl.Name] = p.Path
				case *ast.EnumDecl:
					declared[decl.Name] = p.Path
				case *ast.InterfaceDecl:
					declared[decl.Name] = p.Path
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
		owner, ok := declared[m.typ]
		if !ok || owner == m.pkgPath {
			continue
		}
		bag.Error(m.pos, diag.CodeMethodsTwoPackages,
			"methods of "+m.typ+" are declared in two packages",
			"a method can only be declared where its type is declared")
	}
	return bag.Diagnostics()
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
	for _, f := range p.Files {
		if f.AST == nil {
			continue
		}
		for _, d := range f.AST.Decls {
			switch decl := d.(type) {
			case *ast.ConstDecl:
				addExport(out, decl.Name, resolve.ConstKind, decl.NamePos, decl)
			case *ast.VarDecl:
				addExport(out, decl.Name, resolve.VarKind, decl.NamePos, decl)
			case *ast.FuncDecl:
				if decl.Recv == nil {
					addExport(out, decl.Name, resolve.FuncKind, decl.NamePos, decl)
				}
			case *ast.StructDecl:
				addExport(out, decl.Name, resolve.StructKind, decl.NamePos, decl)
			case *ast.EnumDecl:
				addExport(out, decl.Name, resolve.EnumKind, decl.NamePos, decl)
				for _, v := range decl.Variants {
					addExport(out, v.Name, resolve.VariantKind, v.NamePos, v)
				}
			case *ast.InterfaceDecl:
				addExport(out, decl.Name, resolve.InterfaceKind, decl.NamePos, decl)
			case *ast.LaneDecl:
				addExport(out, decl.Name, resolve.LaneKind, decl.NamePos, decl)
			}
		}
	}
	return out
}

func addExport(out resolve.Exported, name string, kind resolve.Kind, pos source.Pos, node ast.Node) {
	if !isExported(name) {
		return
	}
	out[name] = &resolve.Symbol{Name: name, Kind: kind, Pos: pos, Node: node, Exported: true}
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
	imps := resolve.Imports{}
	for _, p := range b.Packages {
		imps[p.Path] = p.Exports
	}
	var out []Dispatch
	for _, p := range b.Packages {
		for _, f := range p.Files {
			if f.AST == nil {
				continue
			}
			res, _ := resolve.ResolveWith(f.AST, imps)
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

// CheckAll resolves every file with the exported names of every package and
// runs the type checker, returning the merged diagnostics.
func (b *Build) CheckAll() []diag.Diagnostic {
	var bag diag.Bag
	bag.AddAll(b.Diags)
	imps := resolve.Imports{}
	for _, p := range b.Packages {
		imps[p.Path] = p.Exports
	}
	for _, p := range b.Packages {
		for _, f := range p.Files {
			if f.AST == nil {
				continue
			}
			res, ds := resolve.ResolveWith(f.AST, imps)
			bag.AddAll(ds)
			if res != nil {
				bag.AddAll(types.Check(f.AST, res))
			}
		}
	}
	return bag.Diagnostics()
}
